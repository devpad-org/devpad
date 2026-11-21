// Package projecttools gives the assistant in a project's workspace tools
// to look at the project's tasks and merge requests, open and comment on
// merge requests, and hand work to background agents.
//
// The tools run on the host, and the project is always the one the
// conversation's workspace belongs to, never one named in a tool argument.
// They cannot merge, close, approve, push, delete or move the default
// branch: those stay with the user. Anything that starts an agent run or
// opens a merge request waits for the user's approval first; comments do
// not, since they change nothing but the discussion.
package projecttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/domain"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	"github.com/devpad-org/devpad/internal/mergerequest"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/task"
	"github.com/devpad-org/devpad/internal/workspace"
)

const (
	listTasksTool          = "list_tasks"
	getTaskTool            = "get_task"
	createTaskTool         = "create_task"
	linkTasksTool          = "link_tasks"
	startTaskTool          = "start_task"
	requestTaskChangesTool = "request_task_changes"
	listMergeRequestsTool  = "list_merge_requests"
	getMergeRequestTool    = "get_merge_request"
	createMRTool           = "create_merge_request"
	// The merge request service already offers task agents a tool named
	// comment_on_merge_request, and it claims every call by that name.
	commentTool = "add_merge_request_comment"

	// Caps that keep tool results small enough for the model's context.
	maxListedItems     = 100
	maxTaskBodyBytes   = 8 * 1024
	maxMRTextBytes     = 4 * 1024
	maxReviewBodyBytes = 2 * 1024
	maxCommentBytes    = 1024
	maxListedComments  = 50
	maxSummaryBytes    = 300
)

var (
	// promptStatusTimeout bounds reading the workspace's branch for the
	// prompt, so a slow workspace cannot hold up the start of a run.
	promptStatusTimeout = 2 * time.Second
	// branchStatusTimeout bounds comparing the workspace's copy of a branch
	// with the project's after opening a merge request, which only adds a
	// hint to the result.
	branchStatusTimeout = 10 * time.Second
)

var definitions = []domain.ToolDefinition{
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        listTasksTool,
			Description: "List the tasks on this project's board. Tasks are units of work that background AI agents implement on their own branches, each opening a merge request when done.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["open","all","todo","queued","in_progress","in_review","changes_requested","merged","cancelled","failed"],"description":"Which tasks to list. open (the default) leaves out merged and cancelled tasks."}}}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        getTaskTool,
			Description: "Get one task: its description, status, branch, the agent working on it, any error, its merge request, and the tasks it is linked to.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"integer","minimum":1}},"required":["task_id"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        createTaskTool,
			Description: "Add a task to the project's board. With start=true a background agent starts on it in its own sandbox, which needs the user's approval; a task blocked by tasks not merged yet starts once they are. Write the body as complete instructions: the agent sees only the task, not this conversation.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"A short title."},"body":{"type":"string","description":"What to do and why, in Markdown, with everything the agent needs."},"start":{"type":"boolean","description":"Start a background agent on the task now, or once the tasks it is blocked by are merged. Defaults to false."},"blocked_by":{"type":"array","items":{"type":"integer","minimum":1},"description":"Optional ids of tasks whose work this one builds on: its agent starts only once they are all merged. Tasks merge into the default branch, so only a task starting from it can be blocked."},"related":{"type":"array","items":{"type":"integer","minimum":1},"description":"Optional ids of related tasks, shown with the task and to its agent. A related task holds nothing back."},"base_branch":{"type":"string","description":"Optional project branch to start from instead of the default branch. It must already be published to the project."},"agent_id":{"type":"string","description":"Optional agent id. Defaults to the built-in coding agent."},"model":{"type":"string","description":"Optional model id. Defaults to the model the user set for the agent, else the current model."},"effort":{"type":"string","description":"Optional thinking effort, such as low, medium or high."}},"required":["title","body"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        linkTasksTool,
			Description: "Link two tasks on the board. blocked_by makes task_id wait for other_task_id: its agent starts only once the other is merged, so only a task that has not started, and starts from the default branch that tasks merge into, can be blocked. blocks is the same the other way round. related only points the two at each other. Two tasks have at most one link; only the user removes one.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"integer","minimum":1},"kind":{"type":"string","enum":["blocked_by","blocks","related"]},"other_task_id":{"type":"integer","minimum":1}},"required":["task_id","kind","other_task_id"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        startTaskTool,
			Description: "Start a background agent on a task that is to do, failed, or waiting for changes. A task blocked by tasks not merged yet starts once they are. Needs the user's approval.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"integer","minimum":1},"message":{"type":"string","description":"Optional extra instructions for the agent."},"agent_id":{"type":"string"},"model":{"type":"string","description":"Optional model id. Defaults to the task's model, then the model the user set for the agent, then the current model."},"effort":{"type":"string"}},"required":["task_id"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        requestTaskChangesTool,
			Description: "Send feedback on a task in review back to its agent, which starts working on it again. Needs the user's approval.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"integer","minimum":1},"feedback":{"type":"string","description":"What to change, in Markdown."}},"required":["task_id","feedback"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        listMergeRequestsTool,
			Description: "List the project's merge requests. Each proposes merging a branch into one of the project's protected branches, usually the default branch; only the user can merge.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["open","merged","closed","all"],"description":"Defaults to open."}}}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        getMergeRequestTool,
			Description: "Get a merge request with its reviews and comments. A comment without replyTo starts a thread, and resolved says whether that thread is resolved. It does not include the diff: check out the branch to read the code.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"merge_request_id":{"type":"integer","minimum":1}},"required":["merge_request_id"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        createMRTool,
			Description: "Open a merge request to merge a branch published to the project into one of its protected branches. Needs the user's approval. Publish the branch first (git push devpad <branch>): the merge request takes the project's copy of the branch, not your workspace's. The branch of a task that is not merged or cancelled gets its merge request from devpad when its agent finishes, so it cannot be the source here.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"source_branch":{"type":"string","description":"The published branch to merge."},"target_branch":{"type":"string","description":"The protected branch to merge it into. There is no default: name it, and ask the user when it is not clear which one they want."},"title":{"type":"string","description":"A short title."},"description":{"type":"string","description":"What the change does and why, in Markdown, for the user and reviewers."}},"required":["source_branch","target_branch","title"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        commentTool,
			Description: "Comment on an open merge request, or reply to one of its comments, such as a review comment you addressed. The comment is shown as the assistant's. A reply sits with the comment it answers; use the comment ids from get_merge_request.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"merge_request_id":{"type":"integer","minimum":1},"reply_to":{"type":"integer","minimum":1,"description":"The id of the comment you are answering. Leave it out to comment on the whole change."},"body":{"type":"string","description":"The comment, in Markdown. Keep it short and factual."}},"required":["merge_request_id","body"]}`),
		},
	},
}

// Workspaces is the workspace surface the tools need.
type Workspaces interface {
	GitStatus(ctx context.Context, userID, workspaceID int64) (*agent.GitStatus, error)
}

// Projects is the project surface the tools need.
type Projects interface {
	Get(ctx context.Context, userID, projectID int64) (*project.Project, error)
	Branches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error)
	ListSandboxes(ctx context.Context, userID, projectID int64) ([]*workspace.Workspace, error)
	WorkspaceBranchStatus(ctx context.Context, userID, projectID int64, branch string) (*project.WorkspaceBranchStatus, error)
}

// AgentDefaults looks up the model and effort a user set for a built-in
// agent, or nil when it has none a run could use. An effort picked without
// a model is kept when the default model supports it.
type AgentDefaults interface {
	DefaultsFor(ctx context.Context, userID int64, agentID, effort string) (*domain.AgentDefaults, error)
}

// Tasks is the task surface the tools need.
type Tasks interface {
	List(ctx context.Context, userID, projectID int64) ([]*task.Task, error)
	Get(ctx context.Context, userID, projectID, taskID int64) (*task.Task, error)
	Create(ctx context.Context, userID, projectID int64, in task.CreateInput) (*task.Task, error)
	Links(ctx context.Context, userID, projectID int64) (map[int64]task.Links, error)
	Link(ctx context.Context, userID, projectID, taskID int64, kind task.LinkKind, otherID int64) (*task.Task, error)
	Start(ctx context.Context, userID, projectID, taskID int64, in task.StartInput) (*task.Task, error)
	RequestChanges(ctx context.Context, userID, projectID, taskID int64, feedback string, start bool) (*task.Task, error)
}

// MergeRequests is the merge request surface the tools need.
type MergeRequests interface {
	List(ctx context.Context, userID, projectID int64) ([]*mergerequest.MergeRequest, error)
	Get(ctx context.Context, userID, projectID, mrID int64) (*mergerequest.Detail, error)
	AssistantCreate(ctx context.Context, userID, projectID int64, in mergerequest.CreateInput) (*mergerequest.MergeRequest, error)
	AssistantComment(ctx context.Context, userID, projectID, mrID int64, in mergerequest.AgentCommentInput) (*mergerequest.Comment, error)
}

// Provider offers the project tools to runs in project workspaces.
type Provider struct {
	workspaces Workspaces
	projects   Projects
	tasks      Tasks
	mrs        MergeRequests
	defaults   AgentDefaults
}

var (
	_ aitools.ToolProvider = (*Provider)(nil)
	_ aitools.ApprovalGate = (*Provider)(nil)
)

// NewProvider creates the project tools provider.
func NewProvider(workspaces Workspaces, projects Projects, tasks Tasks, mrs MergeRequests, defaults AgentDefaults) *Provider {
	return &Provider{workspaces: workspaces, projects: projects, tasks: tasks, mrs: mrs, defaults: defaults}
}

var errNotProjectWorkspace = errors.New("not a project workspace")

// project returns the project whose workspace the run is in. Sandboxes and
// standalone workspaces have none: agents in sandboxes work on one task, and
// standalone workspaces have no project.
func (p *Provider) project(ctx context.Context, scope aitools.Scope) (*project.Project, error) {
	if !scope.ProjectWorkspace() {
		return nil, errNotProjectWorkspace
	}
	return p.projects.Get(ctx, scope.UserID, scope.ProjectID)
}

func owns(name string) bool {
	return slices.ContainsFunc(definitions, func(d domain.ToolDefinition) bool { return d.Function.Name == name })
}

// outsideProject reports whether a lookup failed because the run is not in
// a project's own workspace, as opposed to failing to find out.
func outsideProject(err error) bool {
	return errors.Is(err, errNotProjectWorkspace) || errors.Is(err, project.ErrNotFound)
}

// Contribute gives the assistant in a project's workspace the project tools,
// and tells it which project and branch it is on and what the tools are for.
// A project that cannot be read leaves them out of the run, which is logged:
// unlike a sandbox, the assistant was meant to have them.
func (p *Provider) Contribute(ctx context.Context, scope aitools.Scope) aitools.Contribution {
	proj, err := p.project(ctx, scope)
	if err != nil {
		if !outsideProject(err) {
			log.Printf("project tools for workspace %d: %v", scope.WorkspaceID, err)
		}
		return aitools.Contribution{}
	}
	return aitools.Contribution{Tools: definitions, Prompt: p.prompt(ctx, scope, proj)}
}

// prompt tells the assistant which project and branch it is on and what the
// project tools are for.
func (p *Provider) prompt(ctx context.Context, scope aitools.Scope, proj *project.Project) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project context:\nYou are working in the user's own workspace for the project %q. /workspace is a git checkout of the project; its default branch is `%s`.", proj.Name, proj.DefaultBranch)
	if len(proj.ProtectedBranches) > 0 {
		fmt.Fprintf(&b, " Its protected branches, which change only through merge requests, are %s.", codeList(proj.Protected()))
	}
	// The branch is a nicety: a slow workspace must not hold up the run.
	statusCtx, cancel := context.WithTimeout(ctx, promptStatusTimeout)
	defer cancel()
	if status, err := p.workspaces.GitStatus(statusCtx, scope.UserID, scope.WorkspaceID); err == nil && status != nil && status.IsRepo {
		if branch := cleanBranch(status.Branch); branch != "" {
			fmt.Fprintf(&b, " The checkout is on the branch `%s`.", branch)
		}
	}
	b.WriteString(`
You are the user's assistant in this workspace, not one of the project's background agents: you share the checkout with the user and work one step at a time at their direction.
- Answer questions without changing anything. Edit files when the user asks for a change and leave the edits uncommitted for them to review; commit only when they ask you to. A branch can take several rounds of changes and commits before the user publishes it or opens a merge request.
- The repo has a devpad remote. git fetch devpad refreshes the project's branches as devpad/<branch>, and git push devpad <branch> publishes a branch exactly as the IDE's Publish button does: never a protected branch, fast-forward only, and an open merge request for the branch moves to the new commit. Push only when the user asks you to publish. A plain git push goes to devpad too.
- An origin remote, when there is one, is the project's upstream and read-only: git fetch origin fetches its latest branches as origin/<branch>, and pushing to it is refused. The project's protected branches reach the upstream only from the project panel, after a merge.
- Background agents implement tasks from the project's board on their own branches. Use list_tasks, get_task, list_merge_requests and get_merge_request to see the board, and create_task to add work for them. Starting an agent (create_task with start, start_task, request_task_changes) asks the user first.
- When work builds on another task's, make it blocked by that task (create_task's blocked_by, or link_tasks): its agent then starts only once that task is merged, from a branch that has its work. Tasks merge into the default branch, so only a task that starts from it can be blocked. Split a large change into tasks this way, rather than one task whose agent does it all.
- create_merge_request opens a merge request from a published branch into a protected branch, and asks the user first. Open one only when the user asks, and name the target branch they want: if they haven't said which and the project has more than one protected branch, ask. The merge request takes the project's copy of the branch, so publish it first: asking you to open a merge request for a branch is asking you to publish it too, unless the user says otherwise. Publishing to it later moves the merge request to the new commit.
- add_merge_request_comment comments on an open merge request or replies to one of its comments, for example to say how you addressed a review comment. Comments are shown as yours, not the user's.
- Only the user merges, closes or approves merge requests. You have no tools for those.`)
	return b.String()
}

// cleanBranch makes a branch name reported by the workspace safe to show in
// the prompt. The workspace is untrusted, so anything that could break out
// of the code span or reorder the text, such as control, format (bidi) or
// line separator characters, drops the name.
func cleanBranch(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || !utf8.ValidString(name) || strings.ContainsFunc(name, func(r rune) bool {
		return r == '`' || unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp)
	}) {
		return ""
	}
	return name
}

type taskArgs struct {
	TaskID int64 `json:"task_id"`
	// BlockedBy and Related are create_task's links.
	BlockedBy []int64 `json:"blocked_by"`
	Related   []int64 `json:"related"`
	// Kind and OtherTaskID are link_tasks' link from TaskID.
	Kind        string `json:"kind"`
	OtherTaskID int64  `json:"other_task_id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Start       bool   `json:"start"`
	BaseBranch  string `json:"base_branch"`
	Message     string `json:"message"`
	Feedback    string `json:"feedback"`
	AgentID     string `json:"agent_id"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	// Status filters the list tools.
	Status         string `json:"status"`
	MergeRequestID int64  `json:"merge_request_id"`
	// ReplyTo is the comment add_merge_request_comment answers; Body is
	// its comment.
	ReplyTo int64 `json:"reply_to"`
	// The merge request create_merge_request opens, with Title.
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Description  string `json:"description"`
}

// needsApproval reports whether a call waits for the user: one that starts
// an agent run, which costs money, or opens a merge request, which puts a
// change up for merging and can start a review agent.
func needsApproval(tool string, args taskArgs) bool {
	if tool == createTaskTool {
		return args.Start
	}
	return mayNeedApproval(tool)
}

// mayNeedApproval reports whether some calls of a tool wait for the user,
// before its arguments are read.
func mayNeedApproval(tool string) bool {
	switch tool {
	case createTaskTool, startTaskTool, requestTaskChangesTool, createMRTool:
		return true
	default:
		return false
	}
}

func (p *Provider) ApprovalFor(ctx context.Context, req aitools.ExecutionRequest) (aitools.Approval, bool, error) {
	if !mayNeedApproval(req.ToolName) {
		return aitools.Approval{}, false, nil
	}
	// Outside a project workspace the call is refused without asking, so an
	// agent in a sandbox cannot put an approval card in front of the user.
	// Any other failure leaves the call unchecked, and it must not run.
	proj, err := p.project(ctx, req.Scope)
	if err != nil {
		if outsideProject(err) {
			return aitools.Approval{}, false, nil
		}
		return aitools.Approval{}, false, fmt.Errorf("checking the workspace: %w", err)
	}
	var args taskArgs
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		// ExecuteTool rejects the same arguments without starting anything.
		return aitools.Approval{}, false, nil
	}
	if !needsApproval(req.ToolName, args) {
		return aitools.Approval{}, false, nil
	}

	switch req.ToolName {
	case createTaskTool:
		details := []domain.ApprovalDetail{
			textDetail("Task title", args.Title),
			textDetail("Instructions", args.Body),
			{Label: "Starts from", Value: branchLabel(args.BaseBranch, proj.DefaultBranch)},
		}
		model, effort, err := p.agentSettings(ctx, req, args.AgentID, args.Model, args.Effort, nil)
		if err != nil {
			return aitools.Approval{}, false, err
		}
		details = append(details, agentDetails(args.AgentID, model, effort)...)
		summary := "Create a task on the board and start a background agent on it, in its own sandbox."
		if waitsFor := p.unmergedTasks(ctx, req, proj, args.BlockedBy); len(waitsFor) > 0 {
			details = append(details, domain.ApprovalDetail{Label: "Waits for", Value: strings.Join(waitsFor, "\n")})
			summary = "Create a task on the board, and start a background agent on it in its own sandbox once the tasks it waits for are merged."
		}
		return aitools.Approval{
			Title:   "Start a background agent",
			Summary: summary,
			Details: details,
		}, true, nil

	case startTaskTool:
		t, err := p.tasks.Get(ctx, req.UserID, proj.ID, args.TaskID)
		if err != nil {
			if errors.Is(err, task.ErrNotFound) || errors.Is(err, project.ErrNotFound) {
				// ExecuteTool reports the task as not in this project.
				return aitools.Approval{}, false, nil
			}
			return aitools.Approval{}, false, fmt.Errorf("reading task #%d: %w", args.TaskID, err)
		}
		details := []domain.ApprovalDetail{
			textDetail("Task title", t.Title),
			textDetail("Task description", t.Body),
		}
		if msg := strings.TrimSpace(args.Message); msg != "" {
			details = append(details, textDetail("Extra instructions", args.Message))
		}
		agentID := firstNonEmpty(strings.TrimSpace(args.AgentID), t.AgentID)
		model, effort, err := p.agentSettings(ctx, req, agentID, args.Model, args.Effort, t)
		if err != nil {
			return aitools.Approval{}, false, err
		}
		details = append(details, agentDetails(agentID, model, effort)...)
		summary := fmt.Sprintf("Start a background agent on task #%d, in its own sandbox.", t.ID)
		if t.Status == task.StatusTodo {
			if links, err := p.tasks.Links(ctx, req.UserID, proj.ID); err == nil && links[t.ID].Blocked {
				details = append(details, domain.ApprovalDetail{Label: "Waits for", Value: strings.Join(p.unmergedTasks(ctx, req, proj, links[t.ID].BlockedBy), "\n")})
				summary = fmt.Sprintf("Start a background agent on task #%d, in its own sandbox, once the tasks it waits for are merged.", t.ID)
			}
		}
		return aitools.Approval{
			Title:   "Start a background agent",
			Summary: summary,
			Details: details,
		}, true, nil

	case createMRTool:
		in, refusal, err := p.mergeRequestInput(ctx, req, proj, args)
		if err != nil {
			return aitools.Approval{}, false, err
		}
		if refusal != "" {
			// ExecuteTool reports why the call cannot run.
			return aitools.Approval{}, false, nil
		}
		target := in.TargetBranch
		if target == proj.DefaultBranch {
			target += " (the default branch)"
		}
		details := []domain.ApprovalDetail{
			{Label: "Merge", Value: in.SourceBranch},
			{Label: "Into", Value: target},
			textDetail("Title", in.Title),
		}
		if strings.TrimSpace(in.Description) != "" {
			details = append(details, textDetail("Description", in.Description))
		}
		// The merge request takes the project's copy of the branch, so the
		// user should know what it leaves out before approving.
		if ahead := p.unpublishedCommits(ctx, req, proj, in.SourceBranch); ahead > 0 {
			details = append(details, domain.ApprovalDetail{Label: "Left out", Value: unpublishedLabel(ahead)})
		}
		summary := "Open a merge request in this project."
		review := "Off for this project"
		if proj.AutoReview {
			summary += " The project reviews new merge requests automatically, so a review agent starts on it."
			review = "Starts on the new merge request"
		}
		details = append(details, domain.ApprovalDetail{Label: "Automatic review", Value: review})
		return aitools.Approval{Title: "Open a merge request", Summary: summary, Details: details}, true, nil

	default:
		t, err := p.tasks.Get(ctx, req.UserID, proj.ID, args.TaskID)
		if err != nil {
			if errors.Is(err, task.ErrNotFound) || errors.Is(err, project.ErrNotFound) {
				return aitools.Approval{}, false, nil
			}
			return aitools.Approval{}, false, fmt.Errorf("reading task #%d: %w", args.TaskID, err)
		}
		details := []domain.ApprovalDetail{
			textDetail("Task title", t.Title),
			textDetail("Feedback", args.Feedback),
		}
		details = append(details, agentDetails(t.AgentID, t.Model, t.Effort)...)
		return aitools.Approval{
			Title:   "Send a task back to its agent",
			Summary: fmt.Sprintf("Send this feedback on task #%d to its agent, which starts working again.", t.ID),
			Details: details,
		}, true, nil
	}
}

// unmergedTasks names those of the given tasks that are not merged, as the
// approval of a start they hold back lists them.
func (p *Provider) unmergedTasks(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, ids []int64) []string {
	var out []string
	for _, id := range ids {
		t, err := p.tasks.Get(ctx, req.UserID, proj.ID, id)
		if err != nil {
			// The call itself is refused for a task not in the project.
			continue
		}
		if t.Status != task.StatusMerged {
			out = append(out, fmt.Sprintf("#%d %s", t.ID, clip(t.Title, maxSummaryBytes)))
		}
	}
	return out
}

func unpublishedLabel(ahead int) string {
	if ahead == 1 {
		return "1 commit in your workspace that isn't published"
	}
	return fmt.Sprintf("%d commits in your workspace that aren't published", ahead)
}

// textDetail is model-written text, shown in full and set apart.
func textDetail(label, value string) domain.ApprovalDetail {
	return domain.ApprovalDetail{Label: label, Value: value, Text: true}
}

func agentDetails(agentID, model, effort string) []domain.ApprovalDetail {
	agent := "The built-in coding agent"
	if agentID = strings.TrimSpace(agentID); agentID != "" {
		agent = agentID
	}
	if model == "" {
		model = "Not set"
	}
	if effort == "" {
		effort = "Default"
	}
	return []domain.ApprovalDetail{
		{Label: "Agent", Value: agent},
		{Label: "Model", Value: model},
		{Label: "Effort", Value: effort},
	}
}

func branchLabel(base, defaultBranch string) string {
	if base = strings.TrimSpace(base); base != "" && base != defaultBranch {
		return base
	}
	return defaultBranch + " (the default branch)"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (p *Provider) ExecuteTool(ctx context.Context, req aitools.ExecutionRequest) (domain.ToolResultPart, bool) {
	if !owns(req.ToolName) {
		return domain.ToolResultPart{}, false
	}
	// The tools are re-checked on every call, not just when offered.
	proj, err := p.project(ctx, req.Scope)
	if err != nil {
		if outsideProject(err) {
			return toolError(req.ToolName, "project tools only work in a project's own workspace"), true
		}
		return toolError(req.ToolName, "could not read the project, try again later"), true
	}
	// Arguments are decoded exactly as ApprovalFor decoded them, so the call
	// that runs is the one the user approved.
	var args taskArgs
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		return toolError(req.ToolName, "invalid arguments: %v", err), true
	}

	switch req.ToolName {
	case listTasksTool:
		return p.listTasks(ctx, req, proj, args.Status), true
	case getTaskTool:
		return p.getTask(ctx, req, proj, args.TaskID), true
	case createTaskTool:
		return p.createTask(ctx, req, proj, args), true
	case linkTasksTool:
		return p.linkTasks(ctx, req, proj, args), true
	case startTaskTool:
		return p.startTask(ctx, req, proj, args), true
	case requestTaskChangesTool:
		return p.requestChanges(ctx, req, proj, args), true
	case listMergeRequestsTool:
		return p.listMergeRequests(ctx, req, proj, args.Status), true
	case createMRTool:
		return p.createMergeRequest(ctx, req, proj, args), true
	case commentTool:
		return p.comment(ctx, req, proj, args), true
	default:
		return p.getMergeRequest(ctx, req, proj, args.MergeRequestID), true
	}
}

type mrRef struct {
	ID     int64               `json:"id"`
	Title  string              `json:"title,omitempty"`
	Status mergerequest.Status `json:"status"`
}

type taskSummary struct {
	ID           int64       `json:"id"`
	Title        string      `json:"title"`
	Status       task.Status `json:"status"`
	Branch       string      `json:"branch"`
	BaseBranch   string      `json:"baseBranch,omitempty"`
	Error        string      `json:"error,omitempty"`
	MergeRequest *mrRef      `json:"mergeRequest,omitempty"`
	taskLinks
}

// taskLinks are a task's links as the tools show them.
type taskLinks struct {
	BlockedBy []int64 `json:"blockedBy,omitempty"`
	Blocks    []int64 `json:"blocks,omitempty"`
	Related   []int64 `json:"related,omitempty"`
	// Blocked says that a task it is blocked by is not merged yet, and
	// StartsWhenUnblocked that its agent starts once none is.
	Blocked             bool `json:"blocked,omitempty"`
	StartsWhenUnblocked bool `json:"startsWhenUnblocked,omitempty"`
}

func linksOf(t *task.Task, l task.Links) taskLinks {
	return taskLinks{BlockedBy: l.BlockedBy, Blocks: l.Blocks, Related: l.Related, Blocked: l.Blocked, StartsWhenUnblocked: t.StartWhenUnblocked}
}

func summarizeTask(t *task.Task, mr *mergerequest.MergeRequest, links task.Links) taskSummary {
	s := taskSummary{ID: t.ID, Title: t.Title, Status: t.Status, Branch: t.Branch, BaseBranch: t.BaseBranch, Error: clip(t.Error, maxSummaryBytes), taskLinks: linksOf(t, links)}
	if mr != nil {
		s.MergeRequest = &mrRef{ID: mr.ID, Status: mr.Status}
	}
	return s
}

// links are the project's task links, or none when they cannot be read:
// they only add to what the tools show.
func (p *Provider) links(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project) map[int64]task.Links {
	links, err := p.tasks.Links(ctx, req.UserID, proj.ID)
	if err != nil {
		log.Printf("project tools: reading task links of project %d: %v", proj.ID, err)
		return nil
	}
	return links
}

// mergeRequestsByTask maps each task to its newest merge request, open ones
// first, as the board shows it.
func (p *Provider) mergeRequestsByTask(ctx context.Context, userID, projectID int64) map[int64]*mergerequest.MergeRequest {
	mrs, err := p.mrs.List(ctx, userID, projectID)
	if err != nil {
		return nil
	}
	byTask := make(map[int64]*mergerequest.MergeRequest)
	for _, mr := range mrs {
		if mr.TaskID == 0 {
			continue
		}
		current := byTask[mr.TaskID]
		if current == nil || (current.Status != mergerequest.StatusOpen && (mr.Status == mergerequest.StatusOpen || mr.ID > current.ID)) {
			byTask[mr.TaskID] = mr
		}
	}
	return byTask
}

func (p *Provider) listTasks(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, status string) domain.ToolResultPart {
	if status == "" {
		status = "open"
	}
	tasks, err := p.tasks.List(ctx, req.UserID, proj.ID)
	if err != nil {
		return toolError(req.ToolName, "listing tasks: %v", err)
	}
	byTask := p.mergeRequestsByTask(ctx, req.UserID, proj.ID)
	links := p.links(ctx, req, proj)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID > tasks[j].ID })
	items := make([]taskSummary, 0)
	total := 0
	for _, t := range tasks {
		switch status {
		case "all":
		case "open":
			if t.Status.Closed() {
				continue
			}
		default:
			if string(t.Status) != status {
				continue
			}
		}
		total++
		if len(items) < maxListedItems {
			items = append(items, summarizeTask(t, byTask[t.ID], links[t.ID]))
		}
	}
	return toolResult(req.ToolName, map[string]any{"status": status, "count": total, "tasks": items})
}

func (p *Provider) getTask(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, taskID int64) domain.ToolResultPart {
	t, err := p.tasks.Get(ctx, req.UserID, proj.ID, taskID)
	if err != nil {
		return taskError(req.ToolName, taskID, err)
	}
	return toolResult(req.ToolName, map[string]any{"task": taskDetail(t, p.mergeRequestsByTask(ctx, req.UserID, proj.ID)[t.ID], p.links(ctx, req, proj)[t.ID])})
}

func taskDetail(t *task.Task, mr *mergerequest.MergeRequest, links task.Links) map[string]any {
	detail := map[string]any{
		"id":        t.ID,
		"title":     t.Title,
		"body":      clip(t.Body, maxTaskBodyBytes),
		"status":    t.Status,
		"branch":    t.Branch,
		"agentId":   t.AgentID,
		"model":     t.Model,
		"effort":    t.Effort,
		"headSha":   t.HeadSHA,
		"createdAt": t.CreatedAt,
		"updatedAt": t.UpdatedAt,
	}
	if t.BaseBranch != "" {
		detail["baseBranch"] = t.BaseBranch
	}
	if t.Error != "" {
		detail["error"] = clip(t.Error, maxTaskBodyBytes)
	}
	if t.PendingMessage != "" {
		detail["pendingFeedback"] = clip(t.PendingMessage, maxTaskBodyBytes)
	}
	if mr != nil {
		detail["mergeRequest"] = mrRef{ID: mr.ID, Title: mr.Title, Status: mr.Status}
	}
	if l := linksOf(t, links); l.BlockedBy != nil || l.Blocks != nil || l.Related != nil {
		detail["links"] = l
	}
	return detail
}

// errNotApproved refuses a call that starts an agent without the user's
// approval for it.
func errNotApproved(name string) domain.ToolResultPart {
	return toolError(name, "this action starts an agent and needs the user's approval, which it did not get")
}

func (p *Provider) createTask(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, args taskArgs) domain.ToolResultPart {
	if needsApproval(req.ToolName, args) && !req.Approved {
		return errNotApproved(req.ToolName)
	}
	in := task.CreateInput{
		Title:      args.Title,
		Body:       args.Body,
		Start:      args.Start,
		BaseBranch: args.BaseBranch,
		BlockedBy:  args.BlockedBy,
		Related:    args.Related,
		AgentSettings: task.AgentSettings{
			AgentID: strings.TrimSpace(args.AgentID),
			Model:   strings.TrimSpace(args.Model),
			Effort:  strings.TrimSpace(args.Effort),
		},
	}
	if in.Start {
		model, effort, err := p.agentSettings(ctx, req, in.AgentID, in.Model, in.Effort, nil)
		if err != nil {
			return toolError(req.ToolName, "choosing the agent's model: %v", err)
		}
		in.Model, in.Effort = model, effort
	}
	t, err := p.tasks.Create(ctx, req.UserID, proj.ID, in)
	if err != nil {
		return toolError(req.ToolName, "creating the task: %v", err)
	}
	message := fmt.Sprintf("Created task #%d.", t.ID)
	switch {
	case t.Status == task.StatusQueued:
		message = fmt.Sprintf("Created task #%d and queued an agent on it. It works in its own sandbox and opens a merge request when done.", t.ID)
	case t.StartWhenUnblocked:
		message = fmt.Sprintf("Created task #%d. Its agent starts once the tasks it is blocked by are merged.", t.ID)
	}
	return toolResult(req.ToolName, map[string]any{"message": message, "task": summarizeTask(t, nil, p.links(ctx, req, proj)[t.ID])})
}

func (p *Provider) linkTasks(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, args taskArgs) domain.ToolResultPart {
	kind := task.LinkKind(args.Kind)
	switch kind {
	case task.LinkBlockedBy, task.LinkBlocks, task.LinkRelated:
	default:
		return toolError(req.ToolName, "kind must be blocked_by, blocks or related")
	}
	t, err := p.tasks.Link(ctx, req.UserID, proj.ID, args.TaskID, kind, args.OtherTaskID)
	if err != nil {
		if errors.Is(err, task.ErrAlreadyLinked) {
			return toolError(req.ToolName, "tasks #%d and #%d are already linked; only the user can remove a link", args.TaskID, args.OtherTaskID)
		}
		return taskError(req.ToolName, args.TaskID, err)
	}
	var message string
	switch kind {
	case task.LinkBlockedBy:
		message = fmt.Sprintf("Task #%d now waits for task #%d to be merged.", args.TaskID, args.OtherTaskID)
	case task.LinkBlocks:
		message = fmt.Sprintf("Task #%d now waits for task #%d to be merged.", args.OtherTaskID, args.TaskID)
	default:
		message = fmt.Sprintf("Tasks #%d and #%d are now related.", args.TaskID, args.OtherTaskID)
	}
	return toolResult(req.ToolName, map[string]any{"message": message, "task": summarizeTask(t, nil, p.links(ctx, req, proj)[t.ID])})
}

func (p *Provider) startTask(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, args taskArgs) domain.ToolResultPart {
	current, err := p.tasks.Get(ctx, req.UserID, proj.ID, args.TaskID)
	if err != nil {
		return taskError(req.ToolName, args.TaskID, err)
	}
	if !req.Approved {
		return errNotApproved(req.ToolName)
	}
	agentID := strings.TrimSpace(args.AgentID)
	model, effort, err := p.agentSettings(ctx, req, firstNonEmpty(agentID, current.AgentID), args.Model, args.Effort, current)
	if err != nil {
		return toolError(req.ToolName, "choosing the agent's model: %v", err)
	}
	t, err := p.tasks.Start(ctx, req.UserID, proj.ID, current.ID, task.StartInput{
		Message:       args.Message,
		AgentSettings: task.AgentSettings{AgentID: agentID, Model: model, Effort: effort},
	})
	if err != nil {
		return taskError(req.ToolName, args.TaskID, err)
	}
	message := fmt.Sprintf("Queued an agent on task #%d.", t.ID)
	if t.StartWhenUnblocked {
		message = fmt.Sprintf("Task #%d is blocked by tasks not merged yet. Its agent starts once they are.", t.ID)
	}
	return toolResult(req.ToolName, map[string]any{
		"message": message,
		"task":    summarizeTask(t, nil, p.links(ctx, req, proj)[t.ID]),
	})
}

func (p *Provider) requestChanges(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, args taskArgs) domain.ToolResultPart {
	if strings.TrimSpace(args.Feedback) == "" {
		return toolError(req.ToolName, "feedback is required")
	}
	if _, err := p.tasks.Get(ctx, req.UserID, proj.ID, args.TaskID); err != nil {
		return taskError(req.ToolName, args.TaskID, err)
	}
	if !req.Approved {
		return errNotApproved(req.ToolName)
	}
	t, err := p.tasks.RequestChanges(ctx, req.UserID, proj.ID, args.TaskID, args.Feedback, true)
	if err != nil {
		return taskError(req.ToolName, args.TaskID, err)
	}
	message := fmt.Sprintf("Sent the feedback to task #%d's agent, which is starting again.", t.ID)
	if t.OverBudget() {
		message = fmt.Sprintf("Saved the feedback on task #%d, but it has spent $%.2f of its $%.2f budget, so its agent gets the feedback only once the user raises the budget and starts it.", t.ID, t.CostUSD, t.BudgetUSD)
	}
	return toolResult(req.ToolName, map[string]any{
		"message": message,
		"task":    summarizeTask(t, nil, p.links(ctx, req, proj)[t.ID]),
	})
}

type mrSummary struct {
	ID           int64               `json:"id"`
	Title        string              `json:"title"`
	Status       mergerequest.Status `json:"status"`
	SourceBranch string              `json:"sourceBranch"`
	TargetBranch string              `json:"targetBranch"`
	TaskID       int64               `json:"taskId,omitempty"`
	HeadSHA      string              `json:"headSha,omitempty"`
}

func summarizeMR(mr *mergerequest.MergeRequest) mrSummary {
	return mrSummary{ID: mr.ID, Title: mr.Title, Status: mr.Status, SourceBranch: mr.SourceBranch, TargetBranch: mr.TargetBranch, TaskID: mr.TaskID, HeadSHA: shortSHA(mr.HeadSHA)}
}

func (p *Provider) listMergeRequests(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, status string) domain.ToolResultPart {
	if status == "" {
		status = "open"
	}
	mrs, err := p.mrs.List(ctx, req.UserID, proj.ID)
	if err != nil {
		return toolError(req.ToolName, "listing merge requests: %v", err)
	}
	sort.Slice(mrs, func(i, j int) bool { return mrs[i].ID > mrs[j].ID })
	items := make([]mrSummary, 0)
	total := 0
	for _, mr := range mrs {
		if status != "all" && string(mr.Status) != status {
			continue
		}
		total++
		if len(items) < maxListedItems {
			items = append(items, summarizeMR(mr))
		}
	}
	return toolResult(req.ToolName, map[string]any{"status": status, "count": total, "mergeRequests": items})
}

func (p *Provider) getMergeRequest(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, mrID int64) domain.ToolResultPart {
	d, err := p.mrs.Get(ctx, req.UserID, proj.ID, mrID)
	if err != nil {
		if errors.Is(err, mergerequest.ErrNotFound) || errors.Is(err, project.ErrNotFound) {
			return toolError(req.ToolName, "merge request !%d is not in this project", mrID)
		}
		return toolError(req.ToolName, "reading the merge request: %v", err)
	}
	mr := d.MergeRequest
	reviews := make([]map[string]any, 0, len(d.Reviews))
	for _, rv := range d.Reviews {
		review := map[string]any{
			"id":       rv.ID,
			"author":   rv.Author,
			"status":   rv.Status,
			"verdict":  rv.Verdict,
			"body":     clip(rv.Body, maxReviewBodyBytes),
			"headSha":  shortSHA(rv.HeadSHA),
			"outdated": rv.HeadSHA != "" && rv.HeadSHA != mr.HeadSHA,
		}
		if rv.Model != "" {
			review["model"] = rv.Model
		}
		if rv.Error != "" {
			review["error"] = clip(rv.Error, maxSummaryBytes)
		}
		reviews = append(reviews, review)
	}
	comments := d.Comments
	omitted := 0
	if len(comments) > maxListedComments {
		omitted = len(comments) - maxListedComments
		comments = comments[omitted:]
	}
	listed := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		comment := map[string]any{
			"id":       c.ID,
			"author":   c.Author,
			"body":     clip(c.Body, maxCommentBytes),
			"outdated": c.HeadSHA != "" && c.HeadSHA != mr.HeadSHA,
		}
		if c.ReplyTo != 0 {
			comment["replyTo"] = c.ReplyTo
		} else {
			comment["resolved"] = c.Resolved()
		}
		if c.Path != "" {
			comment["path"], comment["line"] = c.Path, c.Line
		}
		listed = append(listed, comment)
	}
	result := map[string]any{
		"mergeRequest": map[string]any{
			"id":           mr.ID,
			"title":        mr.Title,
			"description":  clip(mr.Description, maxMRTextBytes),
			"status":       mr.Status,
			"sourceBranch": mr.SourceBranch,
			"targetBranch": mr.TargetBranch,
			"taskId":       mr.TaskID,
			"headSha":      shortSHA(mr.HeadSHA),
			"ahead":        d.Ahead,
			"behind":       d.Behind,
		},
		"reviews":  reviews,
		"comments": listed,
	}
	if omitted > 0 {
		result["olderCommentsLeftOut"] = omitted
	}
	return toolResult(req.ToolName, result)
}

// mergeRequestInput checks a create_merge_request call and returns the
// merge request it would open, or why it is refused. ApprovalFor and
// ExecuteTool both use it, so a call that would be refused shows no
// approval card, short of the branches changing in between, which
// mergerequest.Create checks again under its own lock. An error means it
// could not tell.
//
// The target must be named, never left to the default branch, so the user
// sees the assistant's choice on the approval card. The source must be
// published and not already proposed. A branch an agent's sandbox owns, or
// an unfinished task works on, is refused: devpad opens a task's merge
// request itself, linked to the task so reviews can send changes to its
// agent.
func (p *Provider) mergeRequestInput(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, args taskArgs) (mergerequest.CreateInput, string, error) {
	in := mergerequest.CreateInput{
		Title:        strings.TrimSpace(args.Title),
		Description:  args.Description,
		SourceBranch: strings.TrimSpace(args.SourceBranch),
		TargetBranch: strings.TrimSpace(args.TargetBranch),
	}
	protected := codeList(proj.Protected())
	switch {
	case in.Title == "":
		return in, "title is required", nil
	case in.TargetBranch == "":
		return in, "target_branch is required: name the protected branch to merge into, one of " + protected, nil
	case !proj.IsProtected(in.TargetBranch):
		return in, fmt.Sprintf("target_branch must be one of the project's protected branches: %s", protected), nil
	case in.SourceBranch == "":
		return in, "source_branch is required", nil
	case cleanBranch(in.SourceBranch) == "":
		return in, "source_branch is not a valid branch name", nil
	case in.SourceBranch == in.TargetBranch:
		return in, "source_branch must be a branch other than target_branch", nil
	}

	sandboxes, err := p.projects.ListSandboxes(ctx, req.UserID, proj.ID)
	if err != nil {
		return in, "", fmt.Errorf("listing the project's sandboxes: %w", err)
	}
	for _, sb := range sandboxes {
		if sb.IsSandbox() && sb.Branch == in.SourceBranch {
			return in, fmt.Sprintf("%s belongs to an agent's sandbox, so it cannot be the source of a merge request opened here", in.SourceBranch), nil
		}
	}
	tasks, err := p.tasks.List(ctx, req.UserID, proj.ID)
	if err != nil {
		return in, "", fmt.Errorf("listing the project's tasks: %w", err)
	}
	for _, t := range tasks {
		if t.Branch == in.SourceBranch && !t.Status.Closed() {
			return in, fmt.Sprintf("%s is task #%d's branch, and devpad opens its merge request when the agent finishes; use request_task_changes to send it more work", in.SourceBranch, t.ID), nil
		}
	}

	mrs, err := p.mrs.List(ctx, req.UserID, proj.ID)
	if err != nil {
		return in, "", fmt.Errorf("listing the project's merge requests: %w", err)
	}
	for _, mr := range mrs {
		if mr.SourceBranch == in.SourceBranch && mr.Status == mergerequest.StatusOpen {
			return in, fmt.Sprintf("%s already has an open merge request, !%d; publishing new commits to the branch moves it to them", in.SourceBranch, mr.ID), nil
		}
	}
	refs, err := p.projects.Branches(ctx, req.UserID, proj.ID)
	if errors.Is(err, project.ErrNotReady) {
		return in, err.Error(), nil
	}
	if err != nil {
		return in, "", fmt.Errorf("listing the project's branches: %w", err)
	}
	if !slices.ContainsFunc(refs.Branches, func(b agent.RepoRef) bool { return b.Name == in.SourceBranch }) {
		return in, fmt.Sprintf("%s is not published to the project: publish it with git push devpad %s first", in.SourceBranch, in.SourceBranch), nil
	}
	return in, "", nil
}

func (p *Provider) createMergeRequest(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, args taskArgs) domain.ToolResultPart {
	in, refusal, err := p.mergeRequestInput(ctx, req, proj, args)
	if err != nil {
		return toolError(req.ToolName, "checking the branches: %v", err)
	}
	if refusal != "" {
		return toolError(req.ToolName, "%s", refusal)
	}
	if !req.Approved {
		return toolError(req.ToolName, "opening a merge request needs the user's approval, which it did not get")
	}
	mr, err := p.mrs.AssistantCreate(ctx, req.UserID, proj.ID, in)
	switch {
	case errors.Is(err, mergerequest.ErrInvalidInput), errors.Is(err, mergerequest.ErrInvalidState), errors.Is(err, project.ErrNotReady):
		return toolError(req.ToolName, "%v", err)
	case err != nil:
		return toolError(req.ToolName, "opening the merge request: %v", err)
	}

	message := fmt.Sprintf("Opened merge request !%d to merge %s into %s.", mr.ID, mr.SourceBranch, mr.TargetBranch)
	if proj.AutoReview {
		message += " The project reviews new merge requests automatically, so a review agent is starting on it."
	}
	result := map[string]any{"message": message, "mergeRequest": summarizeMR(mr)}
	if ahead := p.unpublishedCommits(ctx, req, proj, mr.SourceBranch); ahead > 0 {
		result["unpublishedCommits"] = ahead
		unpublished := fmt.Sprintf(" Your workspace has %d commit(s) on %s that the project does not, so they are not in it", ahead, mr.SourceBranch)
		if proj.IsProtected(mr.SourceBranch) {
			// A protected branch is never published: its commits arrive
			// only through merge requests.
			unpublished += fmt.Sprintf(". %s is protected, so they can reach it only through a merge request of their own.", mr.SourceBranch)
		} else {
			unpublished += fmt.Sprintf(": publishing them with git push devpad %s adds them.", mr.SourceBranch)
		}
		result["message"] = message + unpublished
	}
	return toolResult(req.ToolName, result)
}

// unpublishedCommits counts the workspace's commits on branch that the
// project's copy lacks, and so a merge request from it would leave out. The
// count is only a hint, so failing to make it counts none.
func (p *Provider) unpublishedCommits(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, branch string) int {
	statusCtx, cancel := context.WithTimeout(ctx, branchStatusTimeout)
	defer cancel()
	status, err := p.projects.WorkspaceBranchStatus(statusCtx, req.UserID, proj.ID, branch)
	if err != nil || status == nil {
		return 0
	}
	return status.Ahead
}

func (p *Provider) comment(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, args taskArgs) domain.ToolResultPart {
	c, err := p.mrs.AssistantComment(ctx, req.UserID, proj.ID, args.MergeRequestID, mergerequest.AgentCommentInput{ReplyTo: args.ReplyTo, Body: args.Body})
	switch {
	case errors.Is(err, mergerequest.ErrNotFound), errors.Is(err, project.ErrNotFound):
		return toolError(req.ToolName, "merge request !%d is not in this project", args.MergeRequestID)
	case errors.Is(err, mergerequest.ErrCommentLimit):
		return toolError(req.ToolName, "you have left as many comments on merge request !%d as you can", args.MergeRequestID)
	case errors.Is(err, mergerequest.ErrInvalidInput), errors.Is(err, mergerequest.ErrInvalidState):
		return toolError(req.ToolName, "%v", err)
	case err != nil:
		return toolError(req.ToolName, "commenting on the merge request: %v", err)
	case c.ReplyTo != 0:
		return toolResult(req.ToolName, map[string]any{"commentId": c.ID, "replyTo": c.ReplyTo})
	default:
		return toolResult(req.ToolName, map[string]any{"commentId": c.ID})
	}
}

func taskError(name string, taskID int64, err error) domain.ToolResultPart {
	switch {
	case errors.Is(err, task.ErrNotFound), errors.Is(err, project.ErrNotFound):
		return toolError(name, "task #%d is not in this project", taskID)
	case errors.Is(err, task.ErrInvalidState), errors.Is(err, task.ErrInvalidInput), errors.Is(err, project.ErrNotReady):
		return toolError(name, "%v", err)
	default:
		return toolError(name, "task #%d: %v", taskID, err)
	}
}

// agentSettings picks the model and effort a task's agent runs with: what
// the call asks for, else what the task last ran with, else what the user
// set for the agent, else the assistant's own. Without an effort in the call
// a model takes the effort chosen for it, the task's or the assistant's, and
// any other its own default. The approval shows the same choice the call
// then makes. t is nil for a task not created yet.
func (p *Provider) agentSettings(ctx context.Context, req aitools.ExecutionRequest, agentID, model, effort string, t *task.Task) (string, string, error) {
	model, effort = strings.TrimSpace(model), strings.TrimSpace(effort)
	var taskModel, taskEffort string
	if t != nil {
		taskModel, taskEffort = t.Model, t.Effort
	}
	switch {
	case model != "":
	case taskModel != "":
		model = taskModel
	default:
		d, err := p.defaults.DefaultsFor(ctx, req.UserID, strings.TrimSpace(agentID), effort)
		if err != nil {
			return "", "", fmt.Errorf("looking up the agent's default model: %w", err)
		}
		if d != nil {
			return d.Model, d.Effort, nil
		}
		model = req.Model
	}
	if effort == "" {
		switch {
		case model == taskModel:
			effort = taskEffort
		case model == req.Model && req.Thinking != nil:
			effort = req.Thinking.Effort
		}
	}
	return model, effort, nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// clip shortens s to at most n bytes on a rune boundary, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func toolResult(name string, v any) domain.ToolResultPart {
	data, err := json.Marshal(v)
	if err != nil {
		return toolError(name, "encoding the result: %v", err)
	}
	return domain.ToolResultPart{Name: name, Content: string(data)}
}

// toolError reports a failed call. The "Error:" prefix is what the IDE uses
// to show a tool call as failed.
func toolError(name, format string, args ...any) domain.ToolResultPart {
	return domain.ToolResultPart{Name: name, Content: "Error: " + fmt.Sprintf(format, args...), IsError: true}
}

// codeList formats names as a list of code spans.
func codeList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	return strings.Join(quoted, ", ")
}
