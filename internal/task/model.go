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
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// BranchName is the branch a task's sandbox works on.
func BranchName(taskID int64) string {
	return fmt.Sprintf("task/%d", taskID)
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
}

// UpdateInput holds the editable task fields.
type UpdateInput struct {
	Title string
	Body  string
}

// StartInput starts or continues an agent on a task. Message is sent to the
// agent as extra instructions, such as review feedback.
type StartInput struct {
	AgentSettings
	Message string
}
