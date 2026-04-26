package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/auth"
)

type stubCatalogService struct{}

func (stubCatalogService) ListModels(context.Context) ([]domain.ModelInfo, error) {
	return nil, nil
}

func (stubCatalogService) ListProviders(context.Context) ([]app.ProviderInfo, error) {
	return nil, nil
}

func (stubCatalogService) UpdateProvider(context.Context, string, string, bool) error {
	return nil
}

func (stubCatalogService) ResolveChatTarget(context.Context, string) (app.ResolvedTarget, error) {
	return app.ResolvedTarget{}, nil
}

func (stubCatalogService) FindModel(string) (domain.Model, error) {
	return domain.Model{}, nil
}

type stubChatService struct {
	streamSimpleFn func(ctx context.Context, req app.SimpleChatRequest) (<-chan domain.ClientEvent, error)
	streamAgentFn  func(ctx context.Context, req app.AgentChatRequest) (<-chan domain.ClientEvent, error)
}

func (s stubChatService) StreamSimple(ctx context.Context, req app.SimpleChatRequest) (<-chan domain.ClientEvent, error) {
	if s.streamSimpleFn != nil {
		return s.streamSimpleFn(ctx, req)
	}
	ch := make(chan domain.ClientEvent)
	close(ch)
	return ch, nil
}

func (s stubChatService) StreamAgent(ctx context.Context, req app.AgentChatRequest) (<-chan domain.ClientEvent, error) {
	if s.streamAgentFn != nil {
		return s.streamAgentFn(ctx, req)
	}
	ch := make(chan domain.ClientEvent)
	close(ch)
	return ch, nil
}

func TestHandleApproveCommand_ResolvesPendingRequest(t *testing.T) {
	approvals := approval.NewMemoryBroker()
	request, err := approvals.Open(context.Background(), 1, "sudo apt update")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	h := &Handler{approvals: approvals}

	body, err := json.Marshal(map[string]any{"id": request.ID, "approved": true})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/ai/agent/approve", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 1}))
	w := httptest.NewRecorder()

	h.HandleApproveCommand(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	approved, err := approvals.Await(ctx, request.ID)
	if err != nil {
		t.Fatalf("unexpected await error: %v", err)
	}
	if !approved {
		t.Fatal("expected approval decision to be true")
	}
}

func TestHandleChat_UsesAppChatService(t *testing.T) {
	called := false
	chat := stubChatService{streamSimpleFn: func(_ context.Context, req app.SimpleChatRequest) (<-chan domain.ClientEvent, error) {
		called = true
		if req.UserID != 7 {
			t.Fatalf("expected user id 7, got %d", req.UserID)
		}
		if req.Model != "gpt-5.4" {
			t.Fatalf("expected model gpt-5.4, got %q", req.Model)
		}
		if len(req.Turns) != 1 || req.Turns[0].Text() != "hello" {
			t.Fatalf("unexpected turns: %+v", req.Turns)
		}

		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	}}

	h := &Handler{catalog: stubCatalogService{}, chat: chat}
	body := bytes.NewReader([]byte(`{"model":"gpt-5.4","turns":[{"role":"user","parts":[{"kind":"text","text":"hello"}]}]}`))
	req := httptest.NewRequest(http.MethodPost, "/api/ai/chat", body)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 7}))
	w := httptest.NewRecorder()

	h.HandleChat(w, req)

	if !called {
		t.Fatal("expected app chat service to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleAgentChat_UsesAppChatService(t *testing.T) {
	called := false
	chat := stubChatService{streamAgentFn: func(_ context.Context, req app.AgentChatRequest) (<-chan domain.ClientEvent, error) {
		called = true
		if req.UserID != 11 {
			t.Fatalf("expected user id 11, got %d", req.UserID)
		}
		if req.WorkspaceID != 42 {
			t.Fatalf("expected workspace id 42, got %d", req.WorkspaceID)
		}
		if req.Model != "gpt-5.4" {
			t.Fatalf("expected model gpt-5.4, got %q", req.Model)
		}

		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	}}

	h := &Handler{catalog: stubCatalogService{}, chat: chat}
	body := bytes.NewReader([]byte(`{"model":"gpt-5.4","workspaceId":42,"turns":[{"role":"user","parts":[{"kind":"text","text":"hello"}]}]}`))
	req := httptest.NewRequest(http.MethodPost, "/api/ai/agent", body)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 11}))
	w := httptest.NewRecorder()

	h.HandleAgentChat(w, req)

	if !called {
		t.Fatal("expected app agent chat service to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
