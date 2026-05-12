package app

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/devpad-org/devpad/internal/agent"
)

const (
	agentsInstructionPath     = "AGENTS.md"
	maxAgentsInstructionBytes = 16 * 1024
)

type workspaceInstructionFiles interface {
	ListFiles(ctx context.Context, userID, workspaceID int64, path string, opts agent.ListFilesOptions) (*agent.FileList, error)
	ReadFile(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error)
}

type agentsInstructionSource struct {
	files workspaceInstructionFiles
}

// NewAGENTSInstructionSource reads root AGENTS.md instructions from a workspace.
func NewAGENTSInstructionSource(files workspaceInstructionFiles) WorkspaceInstructionSource {
	return agentsInstructionSource{files: files}
}

func (s agentsInstructionSource) Instructions(ctx context.Context, userID, workspaceID int64) (string, error) {
	if s.files == nil {
		return "", nil
	}
	exists, err := s.hasRootAGENTS(ctx, userID, workspaceID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	data, err := s.files.ReadFile(ctx, userID, workspaceID, agentsInstructionPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", agentsInstructionPath, err)
	}
	return formatAGENTSInstructions(data), nil
}

func (s agentsInstructionSource) hasRootAGENTS(ctx context.Context, userID, workspaceID int64) (bool, error) {
	list, err := s.files.ListFiles(ctx, userID, workspaceID, "", agent.ListFilesOptions{MaxEntries: 2000})
	if err != nil {
		return false, fmt.Errorf("checking for %s: %w", agentsInstructionPath, err)
	}
	if list == nil {
		return false, fmt.Errorf("checking for %s: empty file list response", agentsInstructionPath)
	}
	for _, entry := range list.Entries {
		if entry.Name == agentsInstructionPath && entry.Type == "file" {
			return true, nil
		}
	}
	return false, nil
}

func formatAGENTSInstructions(data []byte) string {
	if len(data) <= maxAgentsInstructionBytes {
		return strings.TrimSpace(string(data))
	}
	truncated := string(data[:maxAgentsInstructionBytes])
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return strings.TrimSpace(truncated) + fmt.Sprintf("\n\n[AGENTS.md truncated to %d bytes.]", maxAgentsInstructionBytes)
}
