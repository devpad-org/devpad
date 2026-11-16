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
	s := &chatService{catalog: keyedCatalog(t, 1), agent: agent, workspaces: fakeWorkspaceChecker{err: refused}}
	if _, err := s.StreamAgent(context.Background(), AgentChatRequest{UserID: 1, WorkspaceID: 2, Model: "big"}); !errors.Is(err, refused) {
		t.Fatalf("expected the workspace's error, got %v", err)
	}
	if agent.started != 0 {
		t.Fatal("a refused run must not reach the model")
	}

	s.workspaces = fakeWorkspaceChecker{}
	if _, err := s.StreamAgent(context.Background(), AgentChatRequest{UserID: 1, WorkspaceID: 2, Model: "big"}); err != nil {
		t.Fatalf("expected the run to start, got %v", err)
	}
	if agent.started != 1 {
		t.Fatalf("expected one run started, got %d", agent.started)
	}
}

// A user without a key for the model's provider is refused before the run
// starts.
func TestStreamAgentRefusesAUserWithoutAKey(t *testing.T) {
	agent := &fakeAgentOrchestrator{}
	s := &chatService{catalog: keyedCatalog(t, 1), agent: agent, workspaces: fakeWorkspaceChecker{}}
	if _, err := s.StreamAgent(context.Background(), AgentChatRequest{UserID: 2, WorkspaceID: 2, Model: "big"}); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected a missing key refused, got %v", err)
	}
	if _, err := s.StreamAgent(context.Background(), AgentChatRequest{UserID: 1, WorkspaceID: 2, Model: "tiny"}); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected a provider without the user's key refused, got %v", err)
	}
	if agent.started != 0 {
		t.Fatalf("expected no run started, got %d", agent.started)
	}
}

// keyedCatalog is a catalog where userID has a key for anthropic only.
func keyedCatalog(t *testing.T, userID int64) CatalogService {
	t.Helper()
	catalog := newTwoProviderCatalog(t, memoryProviderKeys{}, newTestCipher(t, "a"))
	if err := catalog.SetProviderKey(context.Background(), userID, "anthropic", "k"); err != nil {
		t.Fatalf("set key: %v", err)
	}
	return catalog
}
