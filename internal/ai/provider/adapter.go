package provider

import (
	"context"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// Credentials contains the secrets needed to call an upstream AI provider.
type Credentials struct {
	APIKey string
}

// StreamRequest is the provider-layer request shape used during Phase 3.
// It mirrors the current chat contract while moving the boundary away from the root package.
type StreamRequest struct {
	Model       string
	Turns       []domain.Turn
	Thinking    *domain.ThinkingConfig
	WorkspaceID int64
	Tools       []domain.ToolDefinition
}

// Adapter streams chat responses from a concrete upstream provider.
type Adapter interface {
	ProviderID() string
	ProviderName() string
	Protocol() Protocol
	Models() []domain.Model
	Stream(ctx context.Context, creds Credentials, req StreamRequest) (<-chan domain.ProviderEvent, error)
}
