package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/ai/question"
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

type stubAgentRunService struct {
	startRunFn        func(ctx context.Context, req app.StartAgentRunRequest) (*domain.AgentRun, error)
	getRunFn          func(ctx context.Context, userID, runID int64) (*domain.AgentRun, error)
	listRunsFn        func(ctx context.Context, userID, workspaceID int64) ([]domain.AgentRun, error)
	subscribeEventsFn func(ctx context.Context, userID, runID, afterSequence int64) (<-chan domain.AgentRunEvent, error)
	cancelRunFn       func(ctx context.Context, userID, runID int64) error
	listAttentionFn   func(ctx context.Context, userID int64) ([]app.AttentionItem, error)
}

func (s stubAgentRunService) ListAttention(ctx context.Context, userID int64) ([]app.AttentionItem, error) {
	if s.listAttentionFn != nil {
		return s.listAttentionFn(ctx, userID)
	}
	return nil, nil
}

func (s stubAgentRunService) StartRun(ctx context.Context, req app.StartAgentRunRequest) (*domain.AgentRun, error) {
	if s.startRunFn != nil {
		return s.startRunFn(ctx, req)
	}
	return &domain.AgentRun{ID: 1, UserID: req.UserID, WorkspaceID: req.WorkspaceID, Model: req.Model, Status: domain.AgentRunQueued, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}

func (s stubAgentRunService) StartChildRun(ctx context.Context, parentRunID int64, req app.StartAgentRunRequest) (*domain.AgentRun, error) {
	req.ParentRunID = parentRunID
	return s.StartRun(ctx, req)
}

func (s stubAgentRunService) GetRun(ctx context.Context, userID, runID int64) (*domain.AgentRun, error) {
	if s.getRunFn != nil {
		return s.getRunFn(ctx, userID, runID)
	}
	return &domain.AgentRun{ID: runID, UserID: userID, WorkspaceID: 42, Model: "gpt-5.4", Status: domain.AgentRunRunning, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}

func (s stubAgentRunService) ListRuns(ctx context.Context, userID, workspaceID int64) ([]domain.AgentRun, error) {
	if s.listRunsFn != nil {
		return s.listRunsFn(ctx, userID, workspaceID)
	}
	return []domain.AgentRun{{ID: 1, UserID: userID, WorkspaceID: workspaceID, Model: "gpt-5.4", Status: domain.AgentRunRunning, CreatedAt: time.Now(), UpdatedAt: time.Now()}}, nil
}

func (s stubAgentRunService) ListEvents(context.Context, int64, int64, int64) ([]domain.AgentRunEvent, error) {
	return nil, nil
}

func (s stubAgentRunService) SubscribeEvents(ctx context.Context, userID, runID, afterSequence int64) (<-chan domain.AgentRunEvent, error) {
	if s.subscribeEventsFn != nil {
		return s.subscribeEventsFn(ctx, userID, runID, afterSequence)
	}
	ch := make(chan domain.AgentRunEvent)
	close(ch)
	return ch, nil
}

func (s stubAgentRunService) CancelRun(ctx context.Context, userID, runID int64) error {
	if s.cancelRunFn != nil {
		return s.cancelRunFn(ctx, userID, runID)
	}
	return nil
}

func (s stubAgentRunService) Shutdown(context.Context) error {
	return nil
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

func TestHandleAnswerQuestion_ResolvesPendingQuestion(t *testing.T) {
	questions := question.NewMemoryBroker()
	request, err := questions.Open(context.Background(), 1, domain.UserQuestionRequest{
		Questions: []domain.UserQuestion{{
			ID:     "stack",
			Prompt: "Which stack?",
			Type:   domain.UserQuestionSingleChoice,
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	h := &Handler{questions: questions}

	body, err := json.Marshal(map[string]any{
		"id": request.ID,
		"answers": []map[string]any{{
			"questionId": "stack",
			"values":     []string{"go"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/ai/agent/questions/answer", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 1}))
	w := httptest.NewRecorder()

	h.HandleAnswerQuestion(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	answers, err := questions.Await(ctx, request.ID)
	if err != nil {
		t.Fatalf("unexpected await error: %v", err)
	}
	if len(answers) != 1 || answers[0].QuestionID != "stack" || len(answers[0].Values) != 1 || answers[0].Values[0] != "go" {
		t.Fatalf("unexpected answers: %+v", answers)
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

func TestHandleCreateAgentRun_StartsBackgroundRun(t *testing.T) {
	called := false
	runs := stubAgentRunService{startRunFn: func(_ context.Context, req app.StartAgentRunRequest) (*domain.AgentRun, error) {
		called = true
		if req.UserID != 11 {
			t.Fatalf("expected user id 11, got %d", req.UserID)
		}
		if req.WorkspaceID != 42 {
			t.Fatalf("expected workspace id 42, got %d", req.WorkspaceID)
		}
		if req.ConversationID != 77 {
			t.Fatalf("expected conversation id 77, got %d", req.ConversationID)
		}
		if req.ParentRunID != 9 {
			t.Fatalf("expected parent run id 9, got %d", req.ParentRunID)
		}
		if req.Model != "gpt-5.4" {
			t.Fatalf("expected model gpt-5.4, got %q", req.Model)
		}
		return &domain.AgentRun{
			ID:             123,
			ParentRunID:    req.ParentRunID,
			UserID:         req.UserID,
			WorkspaceID:    req.WorkspaceID,
			ConversationID: req.ConversationID,
			Model:          req.Model,
			Status:         domain.AgentRunRunning,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}, nil
	}}

	h := &Handler{runs: runs}
	body := bytes.NewReader([]byte(`{"model":"gpt-5.4","workspaceId":42,"conversationId":77,"parentRunId":9,"turns":[{"role":"user","parts":[{"kind":"text","text":"hello"}]}]}`))
	req := httptest.NewRequest(http.MethodPost, "/api/ai/agent/runs", body)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 11}))
	w := httptest.NewRecorder()

	h.HandleCreateAgentRun(w, req)

	if !called {
		t.Fatal("expected agent run service to be called")
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"id":123`) {
		t.Fatalf("expected run response, got %s", w.Body.String())
	}
}

func TestHandleListAgentRuns_ListsWorkspaceRuns(t *testing.T) {
	runs := stubAgentRunService{listRunsFn: func(_ context.Context, userID, workspaceID int64) ([]domain.AgentRun, error) {
		if userID != 5 {
			t.Fatalf("expected user id 5, got %d", userID)
		}
		if workspaceID != 42 {
			t.Fatalf("expected workspace id 42, got %d", workspaceID)
		}
		return []domain.AgentRun{{
			ID:          88,
			UserID:      userID,
			WorkspaceID: workspaceID,
			Model:       "gpt-5.4",
			Status:      domain.AgentRunCompleted,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}}, nil
	}}

	h := &Handler{runs: runs}
	req := httptest.NewRequest(http.MethodGet, "/api/ai/agent/runs?workspaceId=42", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 5}))
	w := httptest.NewRecorder()

	h.HandleListAgentRuns(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"id":88`) || !strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatalf("expected run list response, got %s", w.Body.String())
	}
}

func TestHandleAgentRunEvents_ReplaysRunEvents(t *testing.T) {
	runs := stubAgentRunService{subscribeEventsFn: func(_ context.Context, userID, runID, afterSequence int64) (<-chan domain.AgentRunEvent, error) {
		if userID != 5 {
			t.Fatalf("expected user id 5, got %d", userID)
		}
		if runID != 10 {
			t.Fatalf("expected run id 10, got %d", runID)
		}
		if afterSequence != 1 {
			t.Fatalf("expected after sequence 1, got %d", afterSequence)
		}
		ch := make(chan domain.AgentRunEvent, 2)
		ch <- domain.AgentRunEvent{ID: 2, RunID: 10, Sequence: 2, Event: domain.ClientEvent{TextDelta: "hello"}}
		ch <- domain.AgentRunEvent{ID: 3, RunID: 10, Sequence: 3, Event: domain.ClientEvent{Done: true}}
		close(ch)
		return ch, nil
	}}

	h := &Handler{runs: runs}
	req := httptest.NewRequest(http.MethodGet, "/api/ai/agent/runs/10/events?after=1", nil)
	req.SetPathValue("id", "10")
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 5}))
	w := httptest.NewRecorder()

	h.HandleAgentRunEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"sequence":2`) || !strings.Contains(body, `"content":"hello"`) || !strings.Contains(body, `"sequence":3`) || !strings.Contains(body, `"done":true`) {
		t.Fatalf("unexpected SSE body: %s", body)
	}
}

func TestHandleListAttention_ReturnsPendingPrompts(t *testing.T) {
	runs := stubAgentRunService{listAttentionFn: func(_ context.Context, userID int64) ([]app.AttentionItem, error) {
		if userID != 5 {
			t.Fatalf("expected user id 5, got %d", userID)
		}
		return []app.AttentionItem{
			{Run: domain.AgentRun{ID: 1, WorkspaceID: 9, Status: domain.AgentRunWaitingApproval}, Approval: &domain.ApprovalRequest{ID: "a1", Command: "sudo reboot"}},
			{Run: domain.AgentRun{ID: 2, WorkspaceID: 9, Status: domain.AgentRunWaitingUser}, Question: &domain.UserQuestionRequest{ID: "q1", Title: "Which DB?"}},
		}, nil
	}}
	h := &Handler{runs: runs}
	req := httptest.NewRequest(http.MethodGet, "/api/ai/agent/attention", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 5}))
	w := httptest.NewRecorder()
	h.HandleListAttention(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var body struct {
		Items []struct {
			Run      AgentRunDTO         `json:"run"`
			Approval *ApprovalRequestDTO `json:"approval"`
			Question *QuestionRequestDTO `json:"question"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(body.Items) != 2 || body.Items[0].Approval == nil || body.Items[0].Approval.Command != "sudo reboot" || body.Items[0].Question != nil ||
		body.Items[1].Question == nil || body.Items[1].Question.Title != "Which DB?" || body.Items[1].Approval != nil {
		t.Fatalf("unexpected items: %s", w.Body)
	}
}

type stubAgentDefaultsService struct {
	defaults map[string]domain.AgentDefaults
	setFn    func(domain.AgentDefaults) (*domain.AgentDefaults, error)
}

func (s stubAgentDefaultsService) ListAgentDefaults(context.Context) (map[string]domain.AgentDefaults, error) {
	return s.defaults, nil
}

func (s stubAgentDefaultsService) SetAgentDefaults(_ context.Context, d domain.AgentDefaults) (*domain.AgentDefaults, error) {
	return s.setFn(d)
}

func TestHandleListModels_IncludesAgentDefaults(t *testing.T) {
	h := &Handler{catalog: stubCatalogService{}, defaults: stubAgentDefaultsService{defaults: map[string]domain.AgentDefaults{
		domain.ReviewAgentID: {AgentID: domain.ReviewAgentID, Model: "gpt-5.4", Effort: "high"},
	}}}
	w := httptest.NewRecorder()

	h.HandleListModels(w, httptest.NewRequest(http.MethodGet, "/api/ai/models", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"agentDefaults":{"review":{"agentId":"review","model":"gpt-5.4","effort":"high"}}`) {
		t.Fatalf("expected review defaults, got %s", w.Body.String())
	}
}

func TestHandleListAgentDefaults_ListsEveryBuiltInAgent(t *testing.T) {
	h := &Handler{defaults: stubAgentDefaultsService{defaults: map[string]domain.AgentDefaults{
		domain.ReviewAgentID: {AgentID: domain.ReviewAgentID, Model: "gpt-5.4"},
	}}}
	w := httptest.NewRecorder()

	h.HandleListAgentDefaults(w, httptest.NewRequest(http.MethodGet, "/api/ai/agent-defaults", nil))

	var resp struct {
		Agents []agentDefaultsDTO `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Agents) != len(domain.BuiltInAgents()) {
		t.Fatalf("expected every built-in agent, got %+v", resp.Agents)
	}
	if resp.Agents[0].AgentID != domain.CodingAgentID || resp.Agents[0].Model != "" {
		t.Fatalf("expected the coding agent first without defaults, got %+v", resp.Agents[0])
	}
	if resp.Agents[1].AgentID != domain.ReviewAgentID || resp.Agents[1].Model != "gpt-5.4" || resp.Agents[1].Name == "" {
		t.Fatalf("expected the review agent's defaults, got %+v", resp.Agents[1])
	}
}

func TestHandleSetAgentDefaults(t *testing.T) {
	tests := []struct {
		name    string
		agentID string
		setErr  error
		want    int
	}{
		{"saves", domain.ReviewAgentID, nil, http.StatusOK},
		{"custom agent", "12", nil, http.StatusNotFound},
		{"unknown model", domain.ReviewAgentID, domain.ErrModelNotFound, http.StatusBadRequest},
		{"bad effort", domain.ReviewAgentID, domain.ErrThinkingEffortInvalid, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{defaults: stubAgentDefaultsService{setFn: func(d domain.AgentDefaults) (*domain.AgentDefaults, error) {
				if d.AgentID != tt.agentID || d.Model != "gpt-5.4" || d.Effort != "low" {
					t.Fatalf("unexpected defaults %+v", d)
				}
				if tt.setErr != nil {
					return nil, tt.setErr
				}
				return &d, nil
			}}}
			req := httptest.NewRequest(http.MethodPut, "/api/ai/agent-defaults/"+tt.agentID, strings.NewReader(`{"model":"gpt-5.4","effort":"low"}`))
			req.SetPathValue("id", tt.agentID)
			w := httptest.NewRecorder()

			h.HandleSetAgentDefaults(w, req)

			if w.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}
