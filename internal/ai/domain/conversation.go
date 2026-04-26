package domain

import "time"

// Conversation is a named chat session.
type Conversation struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"userId"`
	WorkspaceID int64     `json:"workspaceId"`
	Title       string    `json:"title"`
	Model       string    `json:"model"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

