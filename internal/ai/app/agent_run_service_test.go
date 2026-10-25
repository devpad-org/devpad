package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type fakeAgentRunRepository struct {
	mu          sync.Mutex
	nextRunID   int64
	nextEventID int64
	runs        map[int64]*domain.AgentRun
	events      map[int64][]domain.AgentRunEvent
}

func newFakeAgentRunRepository() *fakeAgentRunRepository {
	return &fakeAgentRunRepository{
		nextRunID:   1,
		nextEventID: 1,
		runs:        make(map[int64]*domain.AgentRun),
		events:      make(map[int64][]domain.AgentRunEvent),
	}
}

func (r *fakeAgentRunRepository) CreateRun(_ context.Context, run *domain.AgentRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	run.ID = r.nextRunID
	r.nextRunID++
	run.CreatedAt = now
	run.UpdatedAt = now
	r.runs[run.ID] = cloneRun(run)
	return nil
}

func (r *fakeAgentRunRepository) GetRun(_ context.Context, id int64) (*domain.AgentRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	run := r.runs[id]
	if run == nil {
		return nil, nil
	}
	return cloneRun(run), nil
}

func (r *fakeAgentRunRepository) ListRuns(_ context.Context, userID, workspaceID int64) ([]domain.AgentRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	runs := make([]domain.AgentRun, 0)
	for _, run := range r.runs {
		if run.UserID == userID && run.WorkspaceID == workspaceID {
			runs = append(runs, *cloneRun(run))
		}
	}
	return runs, nil
}

func (r *fakeAgentRunRepository) ListEvents(_ context.Context, runID, afterSequence int64) ([]domain.AgentRunEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var events []domain.AgentRunEvent
	for _, event := range r.events[runID] {
		if event.Sequence > afterSequence {
			events = append(events, event)
		}
	}
	return events, nil
}

func (r *fakeAgentRunRepository) AppendEvent(_ context.Context, runID int64, event domain.ClientEvent) (domain.AgentRunEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.runs[runID] == nil {
		return domain.AgentRunEvent{}, domain.ErrAgentRunNotFound
	}
	stored := domain.AgentRunEvent{
		ID:        r.nextEventID,
		RunID:     runID,
		Sequence:  int64(len(r.events[runID]) + 1),
		Event:     event,
		CreatedAt: time.Now().UTC(),
	}
	r.nextEventID++
	r.events[runID] = append(r.events[runID], stored)
	return stored, nil
}

func (r *fakeAgentRunRepository) UpdateStatus(_ context.Context, runID int64, status domain.AgentRunStatus, errorMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	run := r.runs[runID]
	if run == nil {
		return domain.ErrAgentRunNotFound
	}
	now := time.Now().UTC()
	run.Status = status
	run.Error = errorMessage
	run.UpdatedAt = now
	if (status == domain.AgentRunRunning || status == domain.AgentRunWaitingApproval || status == domain.AgentRunWaitingUser) && run.StartedAt == nil {
		run.StartedAt = &now
	}
	if domain.AgentRunStatusTerminal(status) {
		run.CompletedAt = &now
	}
	return nil
}

func (r *fakeAgentRunRepository) ListRunsByStatus(_ context.Context, userID int64, statuses ...domain.AgentRunStatus) ([]domain.AgentRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var runs []domain.AgentRun
	for id := int64(1); id < r.nextRunID; id++ {
		run := r.runs[id]
		if run == nil || (userID != 0 && run.UserID != userID) {
			continue
		}
		for _, status := range statuses {
			if run.Status == status {
				runs = append(runs, *cloneRun(run))
			}
		}
	}
	return runs, nil
}

func (r *fakeAgentRunRepository) MarkActiveRunsFailed(_ context.Context, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, run := range r.runs {
		if !domain.AgentRunStatusTerminal(run.Status) {
			run.Status = domain.AgentRunFailed
			run.Error = message
		}
	}
	return nil
}

type fakeRunnerChatService struct {
	streamAgentFn func(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error)
}

func (s fakeRunnerChatService) StreamSimple(context.Context, SimpleChatRequest) (<-chan domain.ClientEvent, error) {
	ch := make(chan domain.ClientEvent)
	close(ch)
	return ch, nil
}

func (s fakeRunnerChatService) StreamAgent(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
	if s.streamAgentFn != nil {
		return s.streamAgentFn(ctx, req)
	}
	ch := make(chan domain.ClientEvent)
	close(ch)
	return ch, nil
}

type conversationSaveCall struct {
	conversationID int64
	userID         int64
	turns          []domain.Turn
}

type fakeConversationService struct {
	mu       sync.Mutex
	saveErr  error
	saveCall []conversationSaveCall
}

func (s *fakeConversationService) CreateConversation(context.Context, int64, int64, string) (*domain.Conversation, error) {
	return nil, nil
}

func (s *fakeConversationService) ListConversations(context.Context, int64, int64) ([]domain.Conversation, error) {
	return nil, nil
}

func (s *fakeConversationService) GetConversation(context.Context, int64, int64) (*domain.Conversation, error) {
	return nil, nil
}

func (s *fakeConversationService) DeleteConversation(context.Context, int64, int64) error {
	return nil
}

func (s *fakeConversationService) SaveTurns(_ context.Context, conversationID, userID int64, turns []domain.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.saveErr != nil {
		return s.saveErr
	}
	s.saveCall = append(s.saveCall, conversationSaveCall{
		conversationID: conversationID,
		userID:         userID,
		turns:          cloneTurns(turns),
	})
	return nil
}

func (s *fakeConversationService) GetTurns(context.Context, int64, int64) ([]domain.Turn, error) {
	return nil, nil
}

func (s *fakeConversationService) failSaves(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveErr = err
}

func (s *fakeConversationService) saveCalls() []conversationSaveCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	calls := make([]conversationSaveCall, 0, len(s.saveCall))
	for _, call := range s.saveCall {
		call.turns = cloneTurns(call.turns)
		calls = append(calls, call)
	}
	return calls
}

func TestAgentRunService_StartRunContinuesAfterRequestContextCancel(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 2)
	chat := fakeRunnerChatService{streamAgentFn: func(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
		if req.UserID != 7 || req.WorkspaceID != 9 || req.Model != "gpt-5.4" || req.CurrentRunID <= 0 {
			t.Fatalf("unexpected request: %+v", req)
		}
		select {
		case <-ctx.Done():
			t.Fatal("run context should not be cancelled before the browser request context")
		default:
		}
		return events, nil
	}}
	service := NewAgentRunService(context.Background(), repo, chat, nil)
	defer shutdownRunner(t, service)

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	run, err := service.StartRun(requestCtx, StartAgentRunRequest{
		UserID:      7,
		WorkspaceID: 9,
		Model:       "gpt-5.4",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "build it")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	cancelRequest()
	events <- domain.ClientEvent{TextDelta: "still running"}
	events <- domain.ClientEvent{Done: true}
	close(events)

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	stored, err := repo.ListEvents(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("expected 2 stored events, got %d", len(stored))
	}
	if stored[0].Event.TextDelta != "still running" {
		t.Fatalf("unexpected first event: %+v", stored[0].Event)
	}
}

func TestAgentRunService_PersistsLinkedConversationFromRunLifecycle(t *testing.T) {
	repo := newFakeAgentRunRepository()
	conversations := &fakeConversationService{}
	events := make(chan domain.ClientEvent, 8)
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			return events, nil
		},
	}, conversations)
	defer shutdownRunner(t, service)

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         7,
		WorkspaceID:    9,
		ConversationID: 33,
		Model:          "gpt-5.4",
		Turns:          []domain.Turn{domain.NewTextTurn(domain.RoleUser, "build it")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	initialSaves := conversations.saveCalls()
	if len(initialSaves) != 1 {
		t.Fatalf("expected initial conversation save, got %d", len(initialSaves))
	}
	assertSavedConversationTurn(t, initialSaves[0], 33, 7, 1)
	if got := initialSaves[0].turns[0].Text(); got != "build it" {
		t.Fatalf("expected initial user turn, got %q", got)
	}

	events <- domain.ClientEvent{ReasoningDelta: "thinking", ReasoningState: []byte(`{"state":"one"}`)}
	events <- domain.ClientEvent{TextDelta: "I'll "}
	events <- domain.ClientEvent{TextDelta: "do it"}
	events <- domain.ClientEvent{ToolCalls: []domain.ToolCall{{
		ID:     "call-1",
		ItemID: "item-1",
		Type:   "function",
		Function: domain.ToolCallFunction{
			Name:      "read_file",
			Arguments: `{"path":"README.md"}`,
		},
	}}}
	events <- domain.ClientEvent{ToolResult: &domain.ToolResultPart{
		ToolCallID: "call-1",
		Name:       "read_file",
		Content:    "README contents",
	}}
	events <- domain.ClientEvent{TextDelta: "done"}
	events <- domain.ClientEvent{Done: true}
	close(events)

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	saves := conversations.saveCalls()
	if len(saves) != 2 {
		t.Fatalf("expected initial and completed conversation saves, got %d", len(saves))
	}
	finalSave := saves[1]
	assertSavedConversationTurn(t, finalSave, 33, 7, 4)
	if got := finalSave.turns[0].Text(); got != "build it" {
		t.Fatalf("expected user prompt to be preserved, got %q", got)
	}
	assertAssistantTurn(t, finalSave.turns[1], "thinking", `{"state":"one"}`, "I'll do it", "call-1")
	assertToolResultTurn(t, finalSave.turns[2], "call-1", "read_file", "README contents")
	assertAssistantTurn(t, finalSave.turns[3], "", "", "done", "")
}

func TestAgentRunService_PersistsConversationWhenRunFails(t *testing.T) {
	repo := newFakeAgentRunRepository()
	conversations := &fakeConversationService{}
	events := make(chan domain.ClientEvent, 8)
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			return events, nil
		},
	}, conversations)
	defer shutdownRunner(t, service)

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         7,
		WorkspaceID:    9,
		ConversationID: 33,
		Model:          "kimi-k3",
		Turns:          []domain.Turn{domain.NewTextTurn(domain.RoleUser, "build it")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events <- domain.ClientEvent{TextDelta: "starting work"}
	events <- domain.ClientEvent{ToolCalls: []domain.ToolCall{{
		ID:       "call-1",
		Type:     "function",
		Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"README.md"}`},
	}}}
	events <- domain.ClientEvent{ToolResult: &domain.ToolResultPart{
		ToolCallID: "call-1",
		Name:       "read_file",
		Content:    "README contents",
	}}
	events <- domain.ClientEvent{ErrorMessage: "moonshot API error (status 429): engine_overloaded_error"}
	events <- domain.ClientEvent{Done: true}
	close(events)

	waitForRunStatus(t, repo, run.ID, domain.AgentRunFailed)
	saves := conversations.saveCalls()
	if len(saves) != 2 {
		t.Fatalf("expected the failed run to persist its transcript, got %d saves", len(saves))
	}
	finalSave := saves[1]
	assertSavedConversationTurn(t, finalSave, 33, 7, 3)
	if got := finalSave.turns[0].Text(); got != "build it" {
		t.Fatalf("expected the user prompt to survive the failure, got %q", got)
	}
	assertAssistantTurn(t, finalSave.turns[1], "", "", "starting work", "call-1")
	assertToolResultTurn(t, finalSave.turns[2], "call-1", "read_file", "README contents")
}

func TestAgentRunService_DropsUnansweredToolCallsFromFailedTranscript(t *testing.T) {
	repo := newFakeAgentRunRepository()
	conversations := &fakeConversationService{}
	events := make(chan domain.ClientEvent, 8)
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			return events, nil
		},
	}, conversations)
	defer shutdownRunner(t, service)

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         7,
		WorkspaceID:    9,
		ConversationID: 33,
		Model:          "kimi-k3",
		Turns:          []domain.Turn{domain.NewTextTurn(domain.RoleUser, "build it")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// The run dies after requesting a tool but before the result comes back.
	events <- domain.ClientEvent{TextDelta: "reading files"}
	events <- domain.ClientEvent{ToolCalls: []domain.ToolCall{{
		ID:       "call-1",
		Type:     "function",
		Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"README.md"}`},
	}}}
	events <- domain.ClientEvent{ErrorMessage: "moonshot API error (status 429): engine_overloaded_error"}
	events <- domain.ClientEvent{Done: true}
	close(events)

	waitForRunStatus(t, repo, run.ID, domain.AgentRunFailed)
	saves := conversations.saveCalls()
	if len(saves) != 2 {
		t.Fatalf("expected the failed run to persist its transcript, got %d saves", len(saves))
	}
	finalSave := saves[1]
	assertSavedConversationTurn(t, finalSave, 33, 7, 2)
	assertAssistantTurn(t, finalSave.turns[1], "", "", "reading files", "")
	if calls := finalSave.turns[1].ToolCalls(); len(calls) != 0 {
		t.Fatalf("expected the unanswered tool call to be dropped, got %+v", calls)
	}
}

func TestAgentRunService_PersistsConversationWhenRunIsCancelled(t *testing.T) {
	repo := newFakeAgentRunRepository()
	conversations := &fakeConversationService{}
	events := make(chan domain.ClientEvent, 8)
	streaming := make(chan struct{})
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			close(streaming)
			return events, nil
		},
	}, conversations)
	defer shutdownRunner(t, service)

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         7,
		WorkspaceID:    9,
		ConversationID: 33,
		Model:          "kimi-k3",
		Turns:          []domain.Turn{domain.NewTextTurn(domain.RoleUser, "build it")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	<-streaming

	events <- domain.ClientEvent{TextDelta: "partial progress"}
	waitForRunEventCount(t, repo, run.ID, 1)

	if err := service.CancelRun(context.Background(), 7, run.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCancelled)
	saves := conversations.saveCalls()
	if len(saves) != 2 {
		t.Fatalf("expected the cancelled run to persist its transcript, got %d saves", len(saves))
	}
	finalSave := saves[1]
	assertSavedConversationTurn(t, finalSave, 33, 7, 2)
	if got := finalSave.turns[0].Text(); got != "build it" {
		t.Fatalf("expected the user prompt to survive cancellation, got %q", got)
	}
	assertAssistantTurn(t, finalSave.turns[1], "", "", "partial progress", "")
}

func TestAgentRunService_CancelStillMarksRunCancelledWhenPersistenceFails(t *testing.T) {
	repo := newFakeAgentRunRepository()
	conversations := &fakeConversationService{}
	streaming := make(chan struct{})
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			close(streaming)
			return make(chan domain.ClientEvent), nil
		},
	}, conversations)
	defer shutdownRunner(t, service)

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         7,
		WorkspaceID:    9,
		ConversationID: 33,
		Model:          "kimi-k3",
		Turns:          []domain.Turn{domain.NewTextTurn(domain.RoleUser, "build it")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	<-streaming
	conversations.failSaves(errors.New("database is locked"))

	if err := service.CancelRun(context.Background(), 7, run.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCancelled)
}

func TestAgentRunService_DoesNotPersistChildRunConversation(t *testing.T) {
	repo := newFakeAgentRunRepository()
	conversations := &fakeConversationService{}
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			ch := make(chan domain.ClientEvent, 1)
			ch <- domain.ClientEvent{Done: true}
			close(ch)
			return ch, nil
		},
	}, conversations)
	defer shutdownRunner(t, service)

	parent, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:      7,
		WorkspaceID: 9,
		Model:       "gpt-5.4",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "parent")},
	})
	if err != nil {
		t.Fatalf("start parent: %v", err)
	}
	waitForRunStatus(t, repo, parent.ID, domain.AgentRunCompleted)

	child, err := service.StartChildRun(context.Background(), parent.ID, StartAgentRunRequest{
		UserID:         7,
		WorkspaceID:    9,
		ConversationID: 33,
		Model:          "gpt-5.4",
		Turns:          []domain.Turn{domain.NewTextTurn(domain.RoleUser, "child")},
	})
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	waitForRunStatus(t, repo, child.ID, domain.AgentRunCompleted)

	if saves := conversations.saveCalls(); len(saves) != 0 {
		t.Fatalf("expected child run not to save primary conversation, got %d saves", len(saves))
	}
}

func TestAgentRunService_SubscribeEventsReplaysPersistedEvents(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 2)
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			return events, nil
		},
	}, nil)
	defer shutdownRunner(t, service)

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:      1,
		WorkspaceID: 2,
		Model:       "gpt-5.4",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	events <- domain.ClientEvent{TextDelta: "first"}
	events <- domain.ClientEvent{Done: true}
	close(events)
	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := service.SubscribeEvents(ctx, 1, run.ID, 1)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	event := receiveRunEvent(t, stream)
	if event.Sequence != 2 || !event.Event.Done {
		t.Fatalf("expected replayed done event at sequence 2, got %+v", event)
	}
}

func TestAgentRunService_CancelRunCancelsActiveRun(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent)
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			return events, nil
		},
	}, nil)
	defer shutdownRunner(t, service)

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:      4,
		WorkspaceID: 5,
		Model:       "gpt-5.4",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "stop")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if err := service.CancelRun(context.Background(), 4, run.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCancelled)
	stored, err := repo.ListEvents(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(stored) != 2 || stored[0].Event.ErrorMessage == "" || !stored[1].Event.Done {
		t.Fatalf("expected cancellation error and done events, got %+v", stored)
	}
}

func TestAgentRunService_StartChildRunPersistsParentRun(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			ch := make(chan domain.ClientEvent, 1)
			ch <- domain.ClientEvent{Done: true}
			close(ch)
			return ch, nil
		},
	}, nil)
	defer shutdownRunner(t, service)

	parent, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:      8,
		WorkspaceID: 3,
		Model:       "gpt-5.4",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "parent")},
	})
	if err != nil {
		t.Fatalf("start parent: %v", err)
	}

	child, err := service.StartChildRun(context.Background(), parent.ID, StartAgentRunRequest{
		UserID:      8,
		WorkspaceID: 3,
		Model:       "gpt-5.4",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "child")},
	})
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	if child.ParentRunID != parent.ID {
		t.Fatalf("expected parent run id %d, got %d", parent.ID, child.ParentRunID)
	}
}

func TestAgentRunService_StartChildRunLimitsActiveChildren(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			t.Fatal("child run should not start when active child limit is reached")
			return nil, nil
		},
	}, nil)
	defer shutdownRunner(t, service)

	parent := &domain.AgentRun{
		UserID:      8,
		WorkspaceID: 3,
		Model:       "gpt-5.4",
		Status:      domain.AgentRunRunning,
		InputTurns:  []domain.Turn{domain.NewTextTurn(domain.RoleUser, "parent")},
	}
	if err := repo.CreateRun(context.Background(), parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	for i := 0; i < maxActiveChildRunsPerParent; i++ {
		child := &domain.AgentRun{
			ParentRunID: parent.ID,
			UserID:      8,
			WorkspaceID: 3,
			Model:       "gpt-5.4",
			Status:      domain.AgentRunRunning,
			InputTurns:  []domain.Turn{domain.NewTextTurn(domain.RoleUser, "child")},
		}
		if err := repo.CreateRun(context.Background(), child); err != nil {
			t.Fatalf("create active child %d: %v", i, err)
		}
	}

	_, err := service.StartChildRun(context.Background(), parent.ID, StartAgentRunRequest{
		UserID:      8,
		WorkspaceID: 3,
		Model:       "gpt-5.4",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "another child")},
	})
	if err == nil {
		t.Fatal("expected active child limit error")
	}
	if !strings.Contains(err.Error(), "active child runs") {
		t.Fatalf("expected active child limit error, got %v", err)
	}
}

func TestAgentRunService_GetRunEnforcesOwnership(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			ch := make(chan domain.ClientEvent)
			close(ch)
			return ch, nil
		},
	}, nil)
	defer shutdownRunner(t, service)

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:      2,
		WorkspaceID: 6,
		Model:       "gpt-5.4",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "private")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := service.GetRun(context.Background(), 99, run.ID); !errors.Is(err, domain.ErrAgentRunNotFound) {
		t.Fatalf("expected ErrAgentRunNotFound, got %v", err)
	}
}

func waitForRunStatus(t *testing.T, repo *fakeAgentRunRepository, runID int64, status domain.AgentRunStatus) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, err := repo.GetRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run != nil && run.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, _ := repo.GetRun(context.Background(), runID)
	t.Fatalf("run %d did not reach status %s; got %+v", runID, status, run)
}

func receiveRunEvent(t *testing.T, stream <-chan domain.AgentRunEvent) domain.AgentRunEvent {
	t.Helper()
	select {
	case event := <-stream:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for run event")
		return domain.AgentRunEvent{}
	}
}

func waitForRunEventCount(t *testing.T, repo *fakeAgentRunRepository, runID int64, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, err := repo.ListEvents(context.Background(), runID, 0)
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		if len(events) >= count {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events on run %d", count, runID)
}

func assertSavedConversationTurn(t *testing.T, call conversationSaveCall, conversationID, userID int64, turnCount int) {
	t.Helper()
	if call.conversationID != conversationID || call.userID != userID {
		t.Fatalf("expected save for conversation %d user %d, got conversation %d user %d", conversationID, userID, call.conversationID, call.userID)
	}
	if len(call.turns) != turnCount {
		t.Fatalf("expected %d saved turns, got %d: %+v", turnCount, len(call.turns), call.turns)
	}
}

func assertAssistantTurn(t *testing.T, turn domain.Turn, thinkingText, thinkingState, text, toolCallID string) {
	t.Helper()
	if turn.Role != domain.RoleAssistant {
		t.Fatalf("expected assistant turn, got %s", turn.Role)
	}
	if got := turn.ThinkingText(); got != thinkingText {
		t.Fatalf("expected thinking %q, got %q", thinkingText, got)
	}
	if thinkingState != "" {
		if got := string(turn.ThinkingState()); got != thinkingState {
			t.Fatalf("expected thinking state %s, got %s", thinkingState, got)
		}
	}
	if got := turn.Text(); got != text {
		t.Fatalf("expected assistant text %q, got %q", text, got)
	}
	toolCalls := turn.ToolCalls()
	if toolCallID == "" {
		if len(toolCalls) != 0 {
			t.Fatalf("expected no tool calls, got %+v", toolCalls)
		}
		return
	}
	if len(toolCalls) != 1 || toolCalls[0].ID != toolCallID {
		t.Fatalf("expected tool call %q, got %+v", toolCallID, toolCalls)
	}
}

func assertToolResultTurn(t *testing.T, turn domain.Turn, toolCallID, name, content string) {
	t.Helper()
	if turn.Role != domain.RoleUser {
		t.Fatalf("expected user tool-result turn, got %s", turn.Role)
	}
	result := turn.ToolResult()
	if result == nil {
		t.Fatalf("expected tool result, got %+v", turn)
	}
	if result.ToolCallID != toolCallID || result.Name != name || result.Content != content {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

func shutdownRunner(t *testing.T, service *agentRunService) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown runner: %v", err)
	}
}

func cloneRun(run *domain.AgentRun) *domain.AgentRun {
	if run == nil {
		return nil
	}
	clone := *run
	clone.InputTurns = cloneTurns(run.InputTurns)
	clone.Thinking = cloneThinking(run.Thinking)
	if run.StartedAt != nil {
		startedAt := *run.StartedAt
		clone.StartedAt = &startedAt
	}
	if run.CompletedAt != nil {
		completedAt := *run.CompletedAt
		clone.CompletedAt = &completedAt
	}
	return &clone
}

func TestAgentRunService_NotifiesListenersWhenRunsFinish(t *testing.T) {
	repo := newFakeAgentRunRepository()
	streams := make(chan chan domain.ClientEvent, 2)
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			return <-streams, nil
		},
	}, nil)
	defer shutdownRunner(t, service)

	finished := make(chan domain.AgentRun, 4)
	service.OnRunFinished(func(run domain.AgentRun) { finished <- run })

	start := func() (*domain.AgentRun, chan domain.ClientEvent) {
		t.Helper()
		events := make(chan domain.ClientEvent, 4)
		streams <- events
		run, err := service.StartRun(context.Background(), StartAgentRunRequest{
			UserID: 1, WorkspaceID: 2, Model: "m",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
		})
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		return run, events
	}
	next := func() domain.AgentRun {
		t.Helper()
		select {
		case run := <-finished:
			return run
		case <-time.After(2 * time.Second):
			t.Fatal("listener was not called")
			return domain.AgentRun{}
		}
	}

	completed, events := start()
	events <- domain.ClientEvent{TextDelta: "done"}
	events <- domain.ClientEvent{Done: true}
	if got := next(); got.ID != completed.ID || got.Status != domain.AgentRunCompleted {
		t.Fatalf("expected completed run %d, got %d %s", completed.ID, got.ID, got.Status)
	}

	cancelled, _ := start()
	if err := service.CancelRun(context.Background(), 1, cancelled.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	if got := next(); got.ID != cancelled.ID || got.Status != domain.AgentRunCancelled {
		t.Fatalf("expected cancelled run %d, got %d %s", cancelled.ID, got.ID, got.Status)
	}

	select {
	case run := <-finished:
		t.Fatalf("unexpected extra notification for run %d", run.ID)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAgentRunService_StartupInterruptsLeftoverRunsAndKeepsTranscript(t *testing.T) {
	repo := newFakeAgentRunRepository()
	ctx := context.Background()
	left := &domain.AgentRun{UserID: 3, WorkspaceID: 4, ConversationID: 21, Model: "m", Status: domain.AgentRunRunning, InputTurns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "build it")}}
	repo.CreateRun(ctx, left)
	repo.AppendEvent(ctx, left.ID, domain.ClientEvent{TextDelta: "halfway there"})
	done := &domain.AgentRun{UserID: 3, WorkspaceID: 4, Model: "m", Status: domain.AgentRunCompleted}
	repo.CreateRun(ctx, done)

	conversations := &fakeConversationService{}
	service := NewAgentRunService(ctx, repo, fakeRunnerChatService{}, conversations)
	defer shutdownRunner(t, service)

	got, _ := repo.GetRun(ctx, left.ID)
	if !got.Interrupted() {
		t.Fatalf("leftover run should be interrupted, got %s %q", got.Status, got.Error)
	}
	if other, _ := repo.GetRun(ctx, done.ID); other.Status != domain.AgentRunCompleted {
		t.Fatalf("finished runs must be left alone, got %s", other.Status)
	}
	events, _ := repo.ListEvents(ctx, left.ID, 0)
	if n := len(events); n != 3 || events[1].Event.ErrorMessage != domain.AgentRunInterruptedError || !events[2].Event.Done {
		t.Fatalf("expected the event log to end with the interruption, got %+v", events)
	}
	saves := conversations.saveCalls()
	if len(saves) != 1 || saves[0].conversationID != 21 || len(saves[0].turns) != 2 || saves[0].turns[1].Text() != "halfway there" {
		t.Fatalf("expected the transcript rebuilt from events, got %+v", saves)
	}
}

func TestAgentRunService_ShutdownInterruptsActiveRuns(t *testing.T) {
	repo := newFakeAgentRunRepository()
	conversations := &fakeConversationService{}
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(ctx context.Context, _ AgentChatRequest) (<-chan domain.ClientEvent, error) {
			events := make(chan domain.ClientEvent)
			go func() {
				<-ctx.Done()
				close(events)
			}()
			return events, nil
		},
	}, conversations)
	finished := make(chan domain.AgentRun, 1)
	service.OnRunFinished(func(run domain.AgentRun) { finished <- run })

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID: 1, WorkspaceID: 2, ConversationID: 5, Model: "m",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	shutdownRunner(t, service)

	got, _ := repo.GetRun(context.Background(), run.ID)
	if !got.Interrupted() {
		t.Fatalf("a run stopped by shutdown should be interrupted, not %s %q", got.Status, got.Error)
	}
	select {
	case run := <-finished:
		if !run.Interrupted() {
			t.Fatalf("listener got %s %q", run.Status, run.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("listener was not told about the interruption")
	}
	if len(conversations.saveCalls()) < 2 {
		t.Fatal("an interrupted run should persist its transcript")
	}
}

// The orchestrator closes its stream when a run's context ends; a cancelled
// run must never be recorded as completed when the closed stream wins the
// race with ctx.Done.
func TestAgentRunService_CancelledRunIsNeverRecordedAsCompleted(t *testing.T) {
	for i := 0; i < 50; i++ {
		repo := newFakeAgentRunRepository()
		service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
			streamAgentFn: func(ctx context.Context, _ AgentChatRequest) (<-chan domain.ClientEvent, error) {
				events := make(chan domain.ClientEvent)
				go func() {
					<-ctx.Done()
					close(events)
				}()
				return events, nil
			},
		}, nil)
		run, err := service.StartRun(context.Background(), StartAgentRunRequest{
			UserID: 1, WorkspaceID: 2, Model: "m",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
		})
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		waitForRunStatus(t, repo, run.ID, domain.AgentRunRunning)
		if err := service.CancelRun(context.Background(), 1, run.ID); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			got, _ := repo.GetRun(context.Background(), run.ID)
			if domain.AgentRunStatusTerminal(got.Status) {
				if got.Status != domain.AgentRunCancelled {
					t.Fatalf("iteration %d: cancelled run recorded as %s", i, got.Status)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("run never finished")
			}
			time.Sleep(time.Millisecond)
		}
		shutdownRunner(t, service)
	}
}

func TestAgentRunService_ListAttentionReturnsPendingPrompts(t *testing.T) {
	repo := newFakeAgentRunRepository()
	ctx := context.Background()
	service := NewAgentRunService(ctx, repo, fakeRunnerChatService{}, nil)
	defer shutdownRunner(t, service)

	add := func(userID int64, status domain.AgentRunStatus, events ...domain.ClientEvent) int64 {
		run := &domain.AgentRun{UserID: userID, WorkspaceID: 9, Model: "m", Status: status}
		repo.CreateRun(ctx, run)
		for _, e := range events {
			repo.AppendEvent(ctx, run.ID, e)
		}
		return run.ID
	}
	approval := add(1, domain.AgentRunWaitingApproval,
		domain.ClientEvent{Approval: &domain.ApprovalRequest{ID: "a1", Command: "sudo apt install jq"}},
		domain.ClientEvent{ApprovalResult: &domain.ApprovalResult{ID: "a1", Status: "approved"}},
		domain.ClientEvent{Approval: &domain.ApprovalRequest{ID: "a2", Command: "sudo reboot"}},
	)
	question := add(1, domain.AgentRunWaitingUser, domain.ClientEvent{Question: &domain.UserQuestionRequest{ID: "q1", Title: "Which DB?"}})
	add(1, domain.AgentRunWaitingUser,
		domain.ClientEvent{Question: &domain.UserQuestionRequest{ID: "q2"}},
		domain.ClientEvent{QuestionResult: &domain.UserQuestionResult{ID: "q2", Status: "answered"}},
	)
	add(1, domain.AgentRunRunning, domain.ClientEvent{Approval: &domain.ApprovalRequest{ID: "a3"}})
	add(2, domain.AgentRunWaitingApproval, domain.ClientEvent{Approval: &domain.ApprovalRequest{ID: "a4"}})

	items, err := service.ListAttention(ctx, 1)
	if err != nil {
		t.Fatalf("list attention: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 waiting runs, got %+v", items)
	}
	if items[0].Run.ID != approval || items[0].Approval == nil || items[0].Approval.ID != "a2" {
		t.Fatalf("expected the unresolved approval, got %+v", items[0])
	}
	if items[1].Run.ID != question || items[1].Question == nil || items[1].Question.Title != "Which DB?" {
		t.Fatalf("expected the open question, got %+v", items[1])
	}
}
