package mergerequest

import (
	"errors"
	"time"

	"github.com/devpad-org/devpad/internal/task"
)

// Status is where a merge request is in its lifecycle.
type Status string

const (
	StatusOpen   Status = "open"
	StatusMerged Status = "merged"
	StatusClosed Status = "closed"
)

// Author is who wrote a review or comment.
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
	ID           int64
	ProjectID    int64
	UserID       int64
	TaskID       int64
	Title        string
	Description  string
	SourceBranch string
	TargetBranch string
	Status       Status
	// HeadSHA is the source commit under review; only it can be merged.
	HeadSHA string
	// BaseSHA is the merge base, recorded at merge time so the diff of a
	// merged request can still be shown.
	BaseSHA   string
	MergeSHA  string
	MergedAt  *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	// AgentReviewing says a review agent is at work on the merge request.
	// Only listing a project's merge requests sets it.
	AgentReviewing bool
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
	WorkspaceID    int64
	RunID          int64
	AgentID        string
	Model          string
	Effort         string
	// AutoFix sends a request-changes verdict straight to the task's agent.
	AutoFix   bool
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
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
}

// MergeResult is a merged request and, when a push was asked for, why it
// failed if it did.
type MergeResult struct {
	MergeRequest *MergeRequest
	Pushed       bool
	PushError    string
}
