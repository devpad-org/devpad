package task

import (
	"errors"
	"fmt"
	"time"
)

// Status is where a task is in its implement → review → merge lifecycle.
type Status string

const (
	StatusTodo             Status = "todo"
	StatusQueued           Status = "queued"
	StatusInProgress       Status = "in_progress"
	StatusInReview         Status = "in_review"
	StatusChangesRequested Status = "changes_requested"
	StatusMerged           Status = "merged"
	StatusCancelled        Status = "cancelled"
	StatusFailed           Status = "failed"
)

// Startable reports whether an agent can be (re)started on a task in status s.
// A task in review can be sent back with feedback.
func (s Status) Startable() bool {
	switch s {
	case StatusTodo, StatusFailed, StatusChangesRequested, StatusInReview:
		return true
	}
	return false
}

// Closed reports whether the task is finished for good.
func (s Status) Closed() bool {
	return s == StatusMerged || s == StatusCancelled
}

var (
	ErrNotFound     = errors.New("task not found")
	ErrInvalidInput = errors.New("invalid input")
	ErrInvalidState = errors.New("task cannot do that in its current status")
	// ErrAlreadyLinked refuses a second link between two tasks.
	ErrAlreadyLinked = errors.New("the tasks are already linked")
)

// Task is a unit of work in a project. An AI agent implements it in a
// sandbox that owns the task's branch.
type Task struct {
	ID             int64
	ProjectID      int64
	UserID         int64
	Title          string
	Body           string
	Status         Status
	Branch         string
	WorkspaceID    int64
	ConversationID int64
	RunID          int64
	AgentID        string
	Model          string
	Effort         string
	// PendingMessage is sent to the agent when the queued task launches.
	PendingMessage string
	HeadSHA        string
	Error          string
	// BaseBranch is the canonical branch the task's branch starts from when
	// it is not the project's default branch.
	BaseBranch string
	// BaseSHA is the commit the task's branch was first made from, read
	// from the canonical repo when its first sandbox is seeded. Empty for
	// tasks from before it was recorded.
	BaseSHA string
	// Handoff is the conversation the task was handed off from, given to its
	// agent as context when a conversation starts.
	Handoff string
	// BudgetUSD is what every round of the task's agent, and every review
	// of its merge request, with the sub-agents they start, may spend in
	// all; zero means no limit. CostUSD is what they have spent on requests
	// to models with known prices.
	BudgetUSD float64
	CostUSD   float64
	// StartWhenUnblocked marks a task to do that the user asked to start
	// while tasks it is blocked by were not merged: its agent is queued once
	// the last of them is.
	StartWhenUnblocked bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// BranchName is the branch a task's sandbox works on.
func BranchName(taskID int64) string {
	return fmt.Sprintf("task/%d", taskID)
}

// OverBudget reports whether the task's agents and reviews have spent its
// budget.
func (t *Task) OverBudget() bool {
	return t.BudgetUSD > 0 && t.CostUSD >= t.BudgetUSD
}

// budgetError refuses to start the agent of a task that is over budget.
func (t *Task) budgetError() error {
	return fmt.Errorf("%w: %s", ErrInvalidState, t.budgetMessage())
}

// budgetMessage says that the task is over budget and what to do about it.
func (t *Task) budgetMessage() string {
	return fmt.Sprintf("the task has spent $%.2f of its $%.2f budget; raise its budget to continue", t.CostUSD, t.BudgetUSD)
}

// AgentSettings selects the agent, model and thinking effort for a run.
// Empty fields keep the task's previous choice.
type AgentSettings struct {
	AgentID string
	Model   string
	Effort  string
}

// CreateInput holds the fields for a new task.
type CreateInput struct {
	Title string
	Body  string
	AgentSettings
	// Start launches an agent on the task right away.
	Start bool
	// BaseBranch is an existing canonical branch to start from instead of
	// the default branch.
	BaseBranch string
	// BudgetUSD is what the task's agents may spend in all; nil takes the
	// user's default and zero means no limit.
	BudgetUSD *float64
	// Handoff is conversation context for the task's agent.
	Handoff string
	// BlockedBy are tasks of the project the new task waits for: its agent
	// starts only once all of them are merged. With Start set and any of
	// them not merged yet, the task is left to start when they are.
	BlockedBy []int64
	// Related are tasks of the project the new task is related to.
	Related []int64
}

// HandoffInput hands a conversation from the project workspace off to a new
// task, which starts at once.
type HandoffInput struct {
	ConversationID int64
	Title          string
	// Instructions tell the agent what to do with the conversation.
	Instructions string
	BaseBranch   string
	AgentSettings
}

// UpdateInput holds the editable task fields.
type UpdateInput struct {
	Title string
	Body  string
	// BudgetUSD, when set, replaces the task's budget. Zero means no limit.
	BudgetUSD *float64
}

// StartInput starts or continues an agent on a task. Message is sent to the
// agent as extra instructions, such as review feedback.
type StartInput struct {
	AgentSettings
	Message string
}

// LinkKind is how one task is linked to another, from the first task's side.
type LinkKind string

const (
	// LinkBlockedBy: the task waits for the other to be merged.
	LinkBlockedBy LinkKind = "blocked_by"
	// LinkBlocks: the other task waits for this one to be merged.
	LinkBlocks LinkKind = "blocks"
	// LinkRelated: the tasks are related, which holds neither back.
	LinkRelated LinkKind = "related"
)

// Link is a stored link between two tasks of a project. Blocks is false for
// a related link, which is stored with the lower id as From.
type Link struct {
	From, To int64
	Blocks   bool
	// FromStatus is the status of the From task, which says whether a
	// blocking link still holds To back.
	FromStatus Status
}

// Links are one task's links to the project's other tasks.
type Links struct {
	BlockedBy []int64
	Blocks    []int64
	Related   []int64
	// Blocked reports whether a task this one is blocked by is not merged.
	Blocked bool
}

// linksOf gathers task id's side of the links.
func linksOf(id int64, links []Link) Links {
	var l Links
	for _, link := range links {
		switch {
		case link.Blocks && link.To == id:
			l.BlockedBy = append(l.BlockedBy, link.From)
			if link.FromStatus != StatusMerged {
				l.Blocked = true
			}
		case link.Blocks && link.From == id:
			l.Blocks = append(l.Blocks, link.To)
		case link.From == id:
			l.Related = append(l.Related, link.To)
		case link.To == id:
			l.Related = append(l.Related, link.From)
		}
	}
	return l
}
