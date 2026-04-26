package tools

import (
	"context"
	"encoding/json"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// Executor executes AI agent tools against a workspace.
type Executor interface {
	ExecuteTool(ctx context.Context, userID, workspaceID int64, toolName string, args json.RawMessage) domain.ToolResultPart
}
