// Package projecttools gives the assistant in a project's workspace tools
// to look at the project's tasks and merge requests, comment on merge
// requests, and hand work to background agents.
//
// The tools run on the host, and the project is always the one the
// conversation's workspace belongs to, never one named in a tool argument.
// They cannot merge, close, approve, push, delete or move the default
// branch: those stay with the user. Anything that starts an agent run waits
// for the user's approval first; comments do not, since they change nothing
// but the discussion.
package projecttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	startTaskTool          = "start_task"
	requestTaskChangesTool = "request_task_changes"
	listMergeRequestsTool  = "list_merge_requests"
	getMergeRequestTool    = "get_merge_request"
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

// promptStatusTimeout bounds reading the workspace's branch for the prompt,
// so a slow workspace cannot hold up the start of a run.
var promptStatusTimeout = 2 * time.Second

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
			Description: "Get one task: its description, status, branch, the agent working on it, any error, and its merge request.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"integer","minimum":1}},"required":["task_id"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        createTaskTool,
			Description: "Add a task to the project's board. With start=true a background agent starts on it in its own sandbox, which needs the user's approval. Write the body as complete instructions: the agent sees only the task, not this conversation.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"A short title."},"body":{"type":"string","description":"What to do and why, in Markdown, with everything the agent needs."},"start":{"type":"boolean","description":"Start a background agent on the task now. Defaults to false."},"base_branch":{"type":"string","description":"Optional project branch to start from instead of the default branch. It must already be published to the project."},"agent_id":{"type":"string","description":"Optional agent id. Defaults to the built-in coding agent."},"model":{"type":"string","description":"Optional model id. Defaults to the model the admin set for the agent, else the current model."},"effort":{"type":"string","description":"Optional thinking effort, such as low, medium or high."}},"required":["title","body"]}`),
		},
	},
	{
		Type: "function",
		Function: domain.ToolFunction{
			Name:        startTaskTool,
			Description: "Start a background agent on a task that is to do, failed, or waiting for changes. Needs the user's approval.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"integer","minimum":1},"message":{"type":"string","description":"Optional extra instructions for the agent."},"agent_id":{"type":"string"},"model":{"type":"string","description":"Optional model id. Defaults to the task's model, then the model the admin set for the agent, then the current model."},"effort":{"type":"string"}},"required":["task_id"]}`),
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
			Name:        commentTool,
			Description: "Comment on an open merge request, or reply to one of its comments, such as a review comment you addressed. The comment is shown as the assistant's. A reply sits with the comment it answers; use the comment ids from get_merge_request.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"merge_request_id":{"type":"integer","minimum":1},"reply_to":{"type":"integer","minimum":1,"description":"The id of the comment you are answering. Leave it out to comment on the whole change."},"body":{"type":"string","description":"The comment, in Markdown. Keep it short and factual."}},"required":["merge_request_id","body"]}`),
		},
	},
}

// Workspaces is the workspace surface the tools need.
type Workspaces interface {
	Get(ctx context.Context, userID, workspaceID int64) (*workspace.Workspace, error)
	GitStatus(ctx context.Context, userID, workspaceID int64) (*agent.GitStatus, error)
}

// Projects is the project surface the tools need.
type Projects interface {
	Get(ctx context.Context, userID, projectID int64) (*project.Project, error)
}

// AgentDefaults looks up the model and effort an admin set for a built-in
// agent, or nil when it has none a run could use. An effort picked without
// a model is kept when the default model supports it.
type AgentDefaults interface {
	DefaultsFor(ctx context.Context, agentID, effort string) (*domain.AgentDefaults, error)
}

// Tasks is the task surface the tools need.
type Tasks interface {
	List(ctx context.Context, userID, projectID int64) ([]*task.Task, error)
	Get(ctx context.Context, userID, projectID, taskID int64) (*task.Task, error)
	Create(ctx context.Context, userID, projectID int64, in task.CreateInput) (*task.Task, error)
	Start(ctx context.Context, userID, projectID, taskID int64, in task.StartInput) (*task.Task, error)
	RequestChanges(ctx context.Context, userID, projectID, taskID int64, feedback string, start bool) (*task.Task, error)
}

// MergeRequests is the merge request surface the tools need.
type MergeRequests interface {
	List(ctx context.Context, userID, projectID int64) ([]*mergerequest.MergeRequest, error)
	Get(ctx context.Context, userID, projectID, mrID int64) (*mergerequest.Detail, error)
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
	_ aitools.ToolProvider   = (*Provider)(nil)
	_ aitools.ApprovalGate   = (*Provider)(nil)
	_ aitools.PromptProvider = (*Provider)(nil)
)

// NewProvider creates the project tools provider.
func NewProvider(workspaces Workspaces, projects Projects, tasks Tasks, mrs MergeRequests, defaults AgentDefaults) *Provider {
	return &Provider{workspaces: workspaces, projects: projects, tasks: tasks, mrs: mrs, defaults: defaults}
}

var errNotProjectWorkspace = errors.New("not a project workspace")

// project returns the project whose workspace the run is in. Sandboxes and
// standalone workspaces have none: agents in sandboxes work on one task, and
// standalone workspaces have no project.
func (p *Provider) project(ctx context.Context, userID, workspaceID int64) (*project.Project, *workspace.Workspace, error) {
	ws, err := p.workspaces.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if ws.ProjectID == 0 || ws.Kind != workspace.KindInteractive {
		return nil, nil, errNotProjectWorkspace
	}
	proj, err := p.projects.Get(ctx, userID, ws.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	return proj, ws, nil
}

func (p *Provider) Tools(ctx context.Context, userID, workspaceID int64) []domain.ToolDefinition {
	if _, _, err := p.project(ctx, userID, workspaceID); err != nil {
		return nil
	}
	return definitions
}

func owns(name string) bool {
	return slices.ContainsFunc(definitions, func(d domain.ToolDefinition) bool { return d.Function.Name == name })
}

// outsideProject reports whether a lookup failed because the run is not in
// a project's own workspace, as opposed to failing to find out.
func outsideProject(err error) bool {
	return errors.Is(err, errNotProjectWorkspace) || errors.Is(err, workspace.ErrNotFound) || errors.Is(err, project.ErrNotFound)
}

// Prompt tells the assistant which project and branch it is on and what the
// project tools are for.
func (p *Provider) Prompt(ctx context.Context, userID, workspaceID int64) string {
	proj, ws, err := p.project(ctx, userID, workspaceID)
	if err != nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Project context:\nYou are working in the user's own workspace for the project %q. /workspace is a git checkout of the project; its default branch is `%s`.", proj.Name, proj.DefaultBranch)
	if len(proj.ProtectedBranches) > 0 {
		fmt.Fprintf(&b, " Its protected branches, which change only through merge requests, are %s.", codeList(proj.Protected()))
	}
	// The branch is a nicety: a slow workspace must not hold up the run.
	statusCtx, cancel := context.WithTimeout(ctx, promptStatusTimeout)
	defer cancel()
	if status, err := p.workspaces.GitStatus(statusCtx, userID, ws.ID); err == nil && status != nil && status.IsRepo {
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
- add_merge_request_comment comments on an open merge request or replies to one of its comments, for example to say how you addressed a review comment. Comments are shown as yours, not the user's.
- Only the user opens, merges, closes or approves merge requests. You have no tools for those.`)
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
	TaskID     int64  `json:"task_id"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Start      bool   `json:"start"`
	BaseBranch string `json:"base_branch"`
	Message    string `json:"message"`
	Feedback   string `json:"feedback"`
	AgentID    string `json:"agent_id"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
	// Status filters the list tools.
	Status         string `json:"status"`
	MergeRequestID int64  `json:"merge_request_id"`
	// ReplyTo is the comment add_merge_request_comment answers; Body is
	// its comment.
	ReplyTo int64 `json:"reply_to"`
}

// needsApproval reports whether a call starts an agent run, which costs
// money and so waits for the user.
func needsApproval(tool string, args taskArgs) bool {
	switch tool {
	case createTaskTool:
		return args.Start
	case startTaskTool, requestTaskChangesTool:
		return true
	default:
		return false
	}
}

func (p *Provider) ApprovalFor(ctx context.Context, req aitools.ExecutionRequest) (aitools.Approval, bool, error) {
	if req.ToolName != createTaskTool && req.ToolName != startTaskTool && req.ToolName != requestTaskChangesTool {
		return aitools.Approval{}, false, nil
	}
	// Outside a project workspace the call is refused without asking, so an
	// agent in a sandbox cannot put an approval card in front of the user.
	// Any other failure leaves the call unchecked, and it must not run.
	proj, _, err := p.project(ctx, req.UserID, req.WorkspaceID)
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
		return aitools.Approval{
			Title:   "Start a background agent",
			Summary: "Create a task on the board and start a background agent on it, in its own sandbox.",
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
		return aitools.Approval{
			Title:   "Start a background agent",
			Summary: fmt.Sprintf("Start a background agent on task #%d, in its own sandbox.", t.ID),
			Details: details,
		}, true, nil

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
	proj, _, err := p.project(ctx, req.UserID, req.WorkspaceID)
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
	case startTaskTool:
		return p.startTask(ctx, req, proj, args), true
	case requestTaskChangesTool:
		return p.requestChanges(ctx, req, proj, args), true
	case listMergeRequestsTool:
		return p.listMergeRequests(ctx, req, proj, args.Status), true
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
}

func summarizeTask(t *task.Task, mr *mergerequest.MergeRequest) taskSummary {
	s := taskSummary{ID: t.ID, Title: t.Title, Status: t.Status, Branch: t.Branch, BaseBranch: t.BaseBranch, Error: clip(t.Error, maxSummaryBytes)}
	if mr != nil {
		s.MergeRequest = &mrRef{ID: mr.ID, Status: mr.Status}
	}
	return s
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
			items = append(items, summarizeTask(t, byTask[t.ID]))
		}
	}
	return toolResult(req.ToolName, map[string]any{"status": status, "count": total, "tasks": items})
}

func (p *Provider) getTask(ctx context.Context, req aitools.ExecutionRequest, proj *project.Project, taskID int64) domain.ToolResultPart {
	t, err := p.tasks.Get(ctx, req.UserID, proj.ID, taskID)
	if err != nil {
		return taskError(req.ToolName, taskID, err)
	}
	return toolResult(req.ToolName, map[string]any{"task": taskDetail(t, p.mergeRequestsByTask(ctx, req.UserID, proj.ID)[t.ID])})
}

func taskDetail(t *task.Task, mr *mergerequest.MergeRequest) map[string]any {
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
	if t.Status == task.StatusQueued {
		message = fmt.Sprintf("Created task #%d and queued an agent on it. It works in its own sandbox and opens a merge request when done.", t.ID)
	}
	return toolResult(req.ToolName, map[string]any{"message": message, "task": summarizeTask(t, nil)})
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
	return toolResult(req.ToolName, map[string]any{
		"message": fmt.Sprintf("Queued an agent on task #%d.", t.ID),
		"task":    summarizeTask(t, nil),
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
		"task":    summarizeTask(t, nil),
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
// the call asks for, else what the task last ran with, else what the admin
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
		d, err := p.defaults.DefaultsFor(ctx, strings.TrimSpace(agentID), effort)
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
