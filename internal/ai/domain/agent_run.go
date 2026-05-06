package domain

import "time"

// AgentRunStatus captures the lifecycle of a background AI agent run.
type AgentRunStatus string

const (
	AgentRunQueued          AgentRunStatus = "queued"
	AgentRunRunning         AgentRunStatus = "running"
	AgentRunWaitingApproval AgentRunStatus = "waiting_approval"
	AgentRunCompleted       AgentRunStatus = "completed"
	AgentRunFailed          AgentRunStatus = "failed"
	AgentRunCancelled       AgentRunStatus = "cancelled"
)

// AgentRun is the persisted metadata for a browser-independent agent execution.
type AgentRun struct {
	ID             int64
	ParentRunID    int64
	UserID         int64
	WorkspaceID    int64
	ConversationID int64
	Model          string
	Status         AgentRunStatus
	Error          string
	InputTurns     []Turn
	Thinking       *ThinkingConfig
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

// AgentRunEvent is one ordered event emitted by a background run.
type AgentRunEvent struct {
	ID        int64
	RunID     int64
	Sequence  int64
	Event     ClientEvent
	CreatedAt time.Time
}

// AgentRunStatusTerminal reports whether the run has reached a final state.
func AgentRunStatusTerminal(status AgentRunStatus) bool {
	switch status {
	case AgentRunCompleted, AgentRunFailed, AgentRunCancelled:
		return true
	default:
		return false
	}
}
