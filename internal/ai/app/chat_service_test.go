package app

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/ai/orchestrator"
)

type fakeWorkspaceChecker struct{ err error }

func (f fakeWorkspaceChecker) CheckRunning(context.Context, int64, int64) error { return f.err }

type fakeAgentOrchestrator struct{ started int }

func (f *fakeAgentOrchestrator) Stream(context.Context, orchestrator.AgentChatRequest) (<-chan domain.ClientEvent, error) {
	f.started++
	out := make(chan domain.ClientEvent)
	close(out)
	return out, nil
}

func TestStreamAgentRefusesAWorkspaceItCannotRunIn(t *testing.T) {
	refused := errors.New("workspace is not running")
	agent := &fakeAgentOrchestrator{}
	s := &chatService{agent: agent, workspaces: fakeWorkspaceChecker{err: refused}}
	if _, err := s.StreamAgent(context.Background(), AgentChatRequest{UserID: 1, WorkspaceID: 2}); !errors.Is(err, refused) {
		t.Fatalf("expected the workspace's error, got %v", err)
	}
	if agent.started != 0 {
		t.Fatal("a refused run must not reach the model")
	}

	s.workspaces = fakeWorkspaceChecker{}
	if _, err := s.StreamAgent(context.Background(), AgentChatRequest{UserID: 1, WorkspaceID: 2}); err != nil {
		t.Fatalf("expected the run to start, got %v", err)
	}
	if agent.started != 1 {
		t.Fatalf("expected one run started, got %d", agent.started)
	}
}
