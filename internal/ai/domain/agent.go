package domain

import "time"

const DefaultAgentID = "default"

// Agent describes a user-selectable AI persona/instruction set.
type Agent struct {
	ID           string    `json:"id"`
	UserID       int64     `json:"userId,omitempty"`
	WorkspaceID  int64     `json:"workspaceId,omitempty"`
	Name         string    `json:"name"`
	Purpose      string    `json:"purpose"`
	Instructions string    `json:"instructions"`
	IsGlobal     bool      `json:"isGlobal"`
	IsDefault    bool      `json:"isDefault"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// DefaultAgent is baked into Devpad and available to every user/workspace.
func DefaultAgent() Agent {
	now := time.Unix(0, 0).UTC()
	return Agent{
		ID:           DefaultAgentID,
		Name:         "Devpad Agent",
		Purpose:      "General coding assistant for workspace tasks",
		Instructions: "You are Devpad's default AI agent. Help with coding, debugging, code review, shell-safe workspace tasks, and project navigation. Be concise, verify assumptions with tools when needed, and preserve user work.",
		IsGlobal:     true,
		IsDefault:    true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}
