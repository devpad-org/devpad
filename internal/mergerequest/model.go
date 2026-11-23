package mergerequest

import (
	"errors"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/task"
)

// Status is where a merge request is in its lifecycle.
type Status string

const (
	StatusOpen   Status = "open"
	StatusMerged Status = "merged"
	StatusClosed Status = "closed"
)

// Author is who wrote a review or comment, or opened a merge request.
type Author string

const (
	AuthorUser Author = "user"
	// AuthorAgent is a review agent.
	AuthorAgent Author = "agent"
	// AuthorTaskAgent is the agent working on the merge request's task.
	AuthorTaskAgent Author = "task_agent"
	// AuthorAssistant is the assistant in the project's own workspace.
	AuthorAssistant Author = "assistant"
)

// MergedBy is who merged a merge request.
type MergedBy string

const (
	MergedByUser MergedBy = "user"
	// MergedByAuto is devpad merging a task's merge request once a review
	// agent approved it, into a branch the project merges into
	// automatically.
	MergedByAuto MergedBy = "auto"
)

// ReviewStatus tracks an agent review from start to submission.
type ReviewStatus string

const (
	ReviewPending   ReviewStatus = "pending"
	ReviewSubmitted ReviewStatus = "submitted"
	ReviewFailed    ReviewStatus = "failed"
)

// Verdict is a review's conclusion.
type Verdict string

const (
	VerdictApprove        Verdict = "approve"
	VerdictRequestChanges Verdict = "request_changes"
	VerdictComment        Verdict = "comment"
)

// Valid reports whether v is a known verdict.
func (v Verdict) Valid() bool {
	return v == VerdictApprove || v == VerdictRequestChanges || v == VerdictComment
}

const (
	maxCommentBytes      = 10_000
	maxReviewBodyBytes   = 50_000
	maxCommentsPerReview = 100
	// maxTaskAgentComments bounds the comments a task's agent can leave on
	// one merge request over all its rounds.
	maxTaskAgentComments = 500
	// maxAssistantComments bounds the comments the project workspace's
	// assistant can leave on one merge request.
	maxAssistantComments = 500
	// maxSendBackBytes bounds the feedback sent back to a task's agent; the
	// agent can read the rest with its merge request tools.
	maxSendBackBytes = 100_000
)

var (
	ErrNotFound     = errors.New("merge request not found")
	ErrInvalidInput = errors.New("invalid input")
	ErrInvalidState = errors.New("merge request cannot do that in its current status")
	// ErrCommentLimit is returned when an agent has left as many comments on
	// a merge request as it can.
	ErrCommentLimit = errors.New("too many comments on this merge request")
)

// MergeRequest proposes merging a branch into one of the project's protected
// branches.
type MergeRequest struct {
	ID        int64
	ProjectID int64
	UserID    int64
	TaskID    int64
	// OpenedBy is AuthorUser, AuthorTaskAgent for a task's merge request,
	// or AuthorAssistant.
	OpenedBy     Author
	Title        string
	Description  string
	SourceBranch string
	TargetBranch string
	Status       Status
	// HeadSHA is the source commit under review; only it can be merged.
	HeadSHA string
	// BaseSHA is the merge base, recorded at merge time so the diff of a
	// merged request can still be shown.
	BaseSHA  string
	MergeSHA string
	MergedAt *time.Time
	// MergedBy is empty until the merge request is merged.
	MergedBy MergedBy
	// PipelineOverride says the user merged it although its pipeline had
	// not let it through, nil when nothing was overridden.
	PipelineOverride *PipelineOverride
	// ReviewAwaitsPipeline says the project's automatic review of the merge
	// request waits for the pipeline guarding it to pass. Only
	// SetReviewAwaitsPipeline writes it.
	ReviewAwaitsPipeline bool
	// LookClosely is what it keeps of the look-closely mark of its change
	// at HeadSHA, how many files it lists for each reason, nil while devpad
	// has not worked it out for that head. Only SetLookClosely writes it.
	LookClosely *LookCloselySummary
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// AgentReviewing says a review agent is at work on the merge request.
	// Only listing a project's merge requests sets it.
	AgentReviewing bool
}

// PipelineOverride is the user merging a merge request although its
// pipeline gate was shut. Only the user overrides the gate: agents and
// automatic merges never do.
type PipelineOverride struct {
	// State is the gate's state the user merged over, such as "failed" or
	// "running".
	State string
	// PipelineID is the pipeline that stood then, 0 when none had run.
	PipelineID int64
}

// Review is a user's or agent's review of a merge request at one head.
type Review struct {
	ID             int64
	MergeRequestID int64
	Author         Author
	Status         ReviewStatus
	Verdict        Verdict
	Body           string
	HeadSHA        string
	// TargetSHA is the commit of the target branch an agent review compares
	// the change against, set when its sandbox starts. Its instructions and
	// skills are read there, so the branch moving mid-review changes
	// neither. Empty for a user's review and for agent reviews from before
	// it was recorded.
	TargetSHA   string
	WorkspaceID int64
	// ConversationID is the review agent's transcript, which a continued
	// review picks up; RunID is its latest run.
	ConversationID int64
	RunID          int64
	AgentID        string
	Model          string
	Effort         string
	// AutoFix sends a request-changes verdict straight to the task's agent,
	// and reviews the commits it answers with, up to maxReviewRounds.
	AutoFix bool
	// Round is which review of the code and review loop this is, from 1.
	Round int
	// BudgetUSD is what the review's agent, with the sub-agents it starts,
	// may spend; zero means no limit. CostUSD is what it has spent on
	// requests to models with known prices. A review of a task's merge
	// request spends from the task's budget too.
	BudgetUSD float64
	CostUSD   float64
	Error     string
	// AutoMergeError says why an agent review's approval did not merge its
	// merge request into a branch the project merges into automatically.
	AutoMergeError string
	// RunSettled says devpad has settled the end of a submitted agent
	// review's run: stopped its sandbox and decided whether its approval
	// merges the change. An approval waiting for the merge request's
	// pipeline to pass is not settled yet.
	RunSettled bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Finishing reports whether the run of a submitted agent review has not
// ended, devpad is still settling it, or its approval waits for the merge
// request's pipeline, so its approval may yet merge the change.
func (rv *Review) Finishing() bool {
	return rv.Author == AuthorAgent && rv.Status == ReviewSubmitted && !rv.RunSettled
}

// OverBudget reports whether the review's agent has spent its own budget.
func (rv *Review) OverBudget() bool {
	return rv.BudgetUSD > 0 && rv.CostUSD >= rv.BudgetUSD
}

// Comment is a remark on a line of a changed file, or on the whole change
// when Path is empty. A comment without ReplyTo starts a thread, which can be
// replied to and resolved.
type Comment struct {
	ID             int64
	MergeRequestID int64
	ReviewID       int64
	// ReplyTo is the first comment of the thread this one answers. A reply
	// sits where that comment does: same path, line and head.
	ReplyTo int64
	Author  Author
	Path    string
	Line    int
	Body    string
	HeadSHA string
	// Blocking says the thread this comment starts must be addressed before
	// the change can merge. A review agent's thread without it is a
	// suggestion. Replies never block themselves.
	Blocking  bool
	CreatedAt time.Time
	// ResolvedBy and ResolvedAt say who resolved the thread this comment
	// starts, and when; ResolvedBy is empty while it is open.
	ResolvedBy Author
	ResolvedAt *time.Time
}

// Resolved reports whether the thread c starts has been resolved.
func (c *Comment) Resolved() bool {
	return c.ResolvedBy != ""
}

// PushSource is what moved a merge request's source branch.
type PushSource string

const (
	// PushTask is a task's agent finishing a round.
	PushTask PushSource = "task"
	// PushWorkspace is a publish from the project workspace, by the user or
	// its assistant.
	PushWorkspace PushSource = "workspace"
	// PushMerge is another merge request merged into the source branch.
	PushMerge PushSource = "merge"
	// PushUpstream is devpad fast-forwarding the source branch, a protected
	// branch, to commits that landed in the project's upstream.
	PushUpstream PushSource = "upstream"
)

// Push records an open merge request's source branch moving from BeforeSHA
// to HeadSHA, at the time devpad moved the merge request.
type Push struct {
	ID             int64
	MergeRequestID int64
	Source         PushSource
	BeforeSHA      string
	HeadSHA        string
	CreatedAt      time.Time
}

// Detail is a merge request with its reviews, comments and pushes.
type Detail struct {
	MergeRequest *MergeRequest
	Reviews      []*Review
	Comments     []*Comment
	// Pushes are the moves of the source branch after it was opened, oldest
	// first.
	Pushes []*Push
	// Task is the merge request's task, nil when it has none or the task is
	// gone.
	Task *task.Task
	// Ahead and Behind count commits relative to the target branch; they are
	// only filled in for open merge requests.
	Ahead, Behind int
}

// Change is what a merge request changes: the files of its diff, and the
// look-closely mark devpad works out from them.
type Change struct {
	*agent.RepoDiff
	LookClosely LookClosely `json:"lookClosely"`
}

// CreateInput opens a merge request for a published branch.
type CreateInput struct {
	Title        string
	Description  string
	SourceBranch string
	// TargetBranch is a protected branch; empty means the default branch.
	TargetBranch string
}

// CommentInput is a comment in a user review.
type CommentInput struct {
	Path string
	Line int
	Body string
}

// AgentCommentInput is an agent's comment on the whole change, or its reply
// to the comment ReplyTo.
type AgentCommentInput struct {
	ReplyTo int64
	Body    string
}

// UserCommentInput is a comment the user leaves at once, outside a review:
// a reply to the thread of ReplyTo, else a new thread on Line of Path, or on
// the whole change when Path is empty.
type UserCommentInput struct {
	ReplyTo int64
	Path    string
	Line    int
	Body    string
}

// ReviewInput is a user's review.
type ReviewInput struct {
	Verdict  Verdict
	Body     string
	Comments []CommentInput
	// SendToAgent starts the task's agent on a request-changes verdict.
	SendToAgent bool
}

// AgentReviewInput starts a review agent. Empty settings fall back to the
// task's.
type AgentReviewInput struct {
	AgentID string
	Model   string
	Effort  string
	AutoFix bool
}

// ContinueInput continues an agent review that stopped before submitting,
// such as one that ran out of budget. Each budget, when set, is raised first;
// zero means no limit.
type ContinueInput struct {
	// BudgetUSD replaces the review's own budget.
	BudgetUSD *float64
	// TaskBudgetUSD replaces the budget of the merge request's task, which
	// the review spends from too.
	TaskBudgetUSD *float64
}

// SendBackInput sends a merge request back to its task's agent.
type SendBackInput struct {
	// Message is the user's note to the agent, kept on the merge request as
	// a comment.
	Message string
}

// MergeInput merges an open merge request.
type MergeInput struct {
	Strategy string
	Message  string
	// Push sends the target branch upstream after merging.
	Push bool
	// OverridePipeline merges although the merge request's pipeline gate is
	// shut: how the gate stood when the user confirmed merging over it,
	// which it must still be in exactly when the merge is made. Nil leaves
	// the gate to decide.
	OverridePipeline *GateConfirmation
}

// GateConfirmation is the pipeline gate as the user saw it when they
// confirmed merging over it: its state, the pipeline it was about (0 for
// none), and the head and target commits that pipeline was of.
type GateConfirmation struct {
	State      string
	PipelineID int64
	HeadSHA    string
	TargetSHA  string
}

// MergeResult is a merged request and, when a push was asked for, why it
// failed if it did.
type MergeResult struct {
	MergeRequest *MergeRequest
	Pushed       bool
	PushError    string
}
