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
	appendErr   error
	// beforeStatus, if set, runs before each status update is applied.
	beforeStatus func(domain.AgentRunStatus)
	// statusErr, if set, fails the status updates it returns an error for.
	statusErr func(domain.AgentRunStatus) error
	runs      map[int64]*domain.AgentRun
	events    map[int64][]domain.AgentRunEvent
}

func (r *fakeAgentRunRepository) failAppends(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appendErr = err
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

	if r.appendErr != nil {
		return domain.AgentRunEvent{}, r.appendErr
	}
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
	if r.beforeStatus != nil {
		r.beforeStatus(status)
	}
	if r.statusErr != nil {
		if err := r.statusErr(status); err != nil {
			return err
		}
	}
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

func (r *fakeAgentRunRepository) AddUsage(_ context.Context, runID int64, usage domain.Usage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	run := r.runs[runID]
	if run == nil {
		return domain.ErrAgentRunNotFound
	}
	run.Usage.InputTokens += usage.InputTokens
	run.Usage.OutputTokens += usage.OutputTokens
	run.Usage.CacheReadTokens += usage.CacheReadTokens
	run.Usage.CacheWriteTokens += usage.CacheWriteTokens
	if usage.CostUSD != nil {
		cost := *usage.CostUSD
		if run.Usage.CostUSD != nil {
			cost += *run.Usage.CostUSD
		}
		run.Usage.CostUSD = &cost
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

type fakeConversationService struct {
	mu       sync.Mutex
	saveErr  error
	convs    map[int64]domain.Conversation
	turns    map[int64][]domain.Turn
	appends  [][]domain.Turn
	replaces [][]domain.Turn
}

func newFakeConversationService() *fakeConversationService {
	return &fakeConversationService{
		convs: make(map[int64]domain.Conversation),
		turns: make(map[int64][]domain.Turn),
	}
}

func (s *fakeConversationService) addConversation(id, userID, workspaceID int64, turns ...domain.Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convs[id] = domain.Conversation{ID: id, UserID: userID, WorkspaceID: workspaceID, Model: testModel}
	s.turns[id] = cloneTurns(turns)
}

func (s *fakeConversationService) CreateConversation(context.Context, int64, int64, string) (*domain.Conversation, error) {
	return nil, nil
}

func (s *fakeConversationService) ListConversations(context.Context, int64, int64) ([]domain.Conversation, error) {
	return nil, nil
}

func (s *fakeConversationService) GetConversation(_ context.Context, id, userID int64) (*domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.convs[id]
	if !ok || conv.UserID != userID {
		return nil, nil
	}
	return &conv, nil
}

func (s *fakeConversationService) DeleteConversation(context.Context, int64, int64) error {
	return nil
}

func (s *fakeConversationService) SetConversationModel(_ context.Context, id, userID int64, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writableLocked(id, userID); err != nil {
		return err
	}
	conv := s.convs[id]
	conv.Model = model
	s.convs[id] = conv
	return nil
}

func (s *fakeConversationService) conversationModel(id int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.convs[id].Model
}

func (s *fakeConversationService) AppendTurns(_ context.Context, conversationID, userID int64, turns []domain.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writableLocked(conversationID, userID); err != nil {
		return err
	}
	s.turns[conversationID] = append(s.turns[conversationID], cloneTurns(turns)...)
	s.appends = append(s.appends, cloneTurns(turns))
	return nil
}

func (s *fakeConversationService) ReplaceTurns(_ context.Context, conversationID, userID int64, turns []domain.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writableLocked(conversationID, userID); err != nil {
		return err
	}
	s.turns[conversationID] = cloneTurns(turns)
	s.replaces = append(s.replaces, cloneTurns(turns))
	return nil
}

func (s *fakeConversationService) writableLocked(conversationID, userID int64) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	if conv, ok := s.convs[conversationID]; !ok || conv.UserID != userID {
		return domain.ErrConversationNotFound
	}
	return nil
}

func (s *fakeConversationService) GetTurns(_ context.Context, conversationID, userID int64) ([]domain.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if conv, ok := s.convs[conversationID]; !ok || conv.UserID != userID {
		return nil, nil
	}
	return cloneTurns(s.turns[conversationID]), nil
}

func (s *fakeConversationService) failSaves(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveErr = err
}

func (s *fakeConversationService) history(conversationID int64) []domain.Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneTurns(s.turns[conversationID])
}

func (s *fakeConversationService) appendCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.appends)
}

func (s *fakeConversationService) replaceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.replaces)
}

const (
	testUserID         = 7
	testWorkspaceID    = 9
	testConversationID = 33
	testModel          = "gpt-5.4"
)

// newConversationRunner returns a runner and a store holding one empty
// conversation the test user owns.
func newConversationRunner(t *testing.T, repo *fakeAgentRunRepository, streamAgentFn func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error), turns ...domain.Turn) (*agentRunService, *fakeConversationService) {
	t.Helper()
	conversations := newFakeConversationService()
	conversations.addConversation(testConversationID, testUserID, testWorkspaceID, turns...)
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{streamAgentFn: streamAgentFn}, conversations).WithModels(testModels)
	t.Cleanup(func() { shutdownRunner(t, service) })
	return service, conversations
}

// providerModels maps each model ID a test knows to its provider.
type providerModels map[string]string

func (m providerModels) FindModel(id string) (domain.Model, error) {
	provider, ok := m[id]
	if !ok {
		return domain.Model{}, domain.ErrModelNotFound
	}
	return domain.Model{ID: id, Name: id, ProviderID: provider}, nil
}

var testModels = providerModels{
	testModel:       "openai",
	"gpt-5.4-mini":  "openai",
	"claude-sonnet": "anthropic",
}

func streamFrom(events <-chan domain.ClientEvent) func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
	return func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		return events, nil
	}
}

func startTestRun(t *testing.T, service *agentRunService, message string) *domain.AgentRun {
	t.Helper()
	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          "gpt-5.4",
		Message:        domain.NewTextTurn(domain.RoleUser, message),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	return run
}

func TestAgentRunService_StartRunContinuesAfterRequestContextCancel(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 2)
	service, _ := newConversationRunner(t, repo, func(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
		if req.UserID != testUserID || req.WorkspaceID != testWorkspaceID || req.Model != "gpt-5.4" || req.CurrentRunID <= 0 {
			t.Fatalf("unexpected request: %+v", req)
		}
		select {
		case <-ctx.Done():
			t.Fatal("run context should not be cancelled before the browser request context")
		default:
		}
		return events, nil
	})

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	run, err := service.StartRun(requestCtx, StartAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          "gpt-5.4",
		Message:        domain.NewTextTurn(domain.RoleUser, "build it"),
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

func TestAgentRunService_ContinuesConversationFromSavedHistory(t *testing.T) {
	repo := newFakeAgentRunRepository()
	requests := make(chan AgentChatRequest, 1)
	service, conversations := newConversationRunner(t, repo, func(_ context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
		requests <- req
		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	}, domain.NewTextTurn(domain.RoleUser, "first"), domain.NewTextTurn(domain.RoleAssistant, "hello"))

	run := startTestRun(t, service, "second")

	req := <-requests
	want := []string{"user:first", "assistant:hello", "user:second"}
	if got := turnSummary(req.Turns); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected the model to get the saved history plus the message, got %v", got)
	}
	if got := turnSummary(run.InputTurns); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected the run to record its input, got %v", got)
	}
	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	if got := turnSummary(conversations.history(testConversationID)); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected the message to be appended to the saved history, got %v", got)
	}
}

func TestAgentRunService_SavesEachStepAsItCompletes(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 8)
	service, conversations := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "build it")
	if got := turnSummary(conversations.history(testConversationID)); len(got) != 1 || got[0] != "user:build it" {
		t.Fatalf("expected the user message to be saved when the run starts, got %v", got)
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

	// The completed step is saved while the run is still going.
	history := waitForHistoryLength(t, conversations, 3)
	assertAssistantTurn(t, history[1], "thinking", `{"state":"one"}`, "I'll do it", "call-1")
	assertToolResultTurn(t, history[2], "call-1", "read_file", "README contents")

	events <- domain.ClientEvent{TextDelta: "done"}
	events <- domain.ClientEvent{Done: true}
	close(events)

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	history = conversations.history(testConversationID)
	if len(history) != 4 {
		t.Fatalf("expected 4 saved turns, got %v", turnSummary(history))
	}
	assertAssistantTurn(t, history[3], "", "", "done", "")
}

func TestAgentRunService_SavesParallelToolCallsTogether(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 8)
	service, conversations := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "read both")
	events <- domain.ClientEvent{ToolCalls: []domain.ToolCall{
		{ID: "call-a", Type: "function", Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.go"}`}},
		{ID: "call-b", Type: "function", Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"b.go"}`}},
	}}
	events <- domain.ClientEvent{ToolResult: &domain.ToolResultPart{ToolCallID: "call-a", Name: "read_file", Content: "package a"}}
	events <- domain.ClientEvent{ToolResult: &domain.ToolResultPart{ToolCallID: "call-b", Name: "read_file", Content: "package b"}}
	events <- domain.ClientEvent{Done: true}
	close(events)

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	want := []string{"user:read both", "assistant:call-a,call-b", "result:call-a", "result:call-b"}
	if got := turnSummary(conversations.history(testConversationID)); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("unexpected saved history\n got: %v\nwant: %v", got, want)
	}
}

func TestAgentRunService_PersistsConversationWhenRunFails(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 8)
	service, conversations := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "build it")
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
	history := conversations.history(testConversationID)
	if len(history) != 3 {
		t.Fatalf("expected the failed run to keep its finished step, got %v", turnSummary(history))
	}
	if got := history[0].Text(); got != "build it" {
		t.Fatalf("expected the user prompt to survive the failure, got %q", got)
	}
	assertAssistantTurn(t, history[1], "", "", "starting work", "call-1")
	assertToolResultTurn(t, history[2], "call-1", "read_file", "README contents")
}

func TestAgentRunService_DropsUnansweredToolCallsFromFailedTranscript(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 8)
	service, conversations := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "build it")
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
	history := conversations.history(testConversationID)
	if len(history) != 2 {
		t.Fatalf("expected the user message and the partial reply, got %v", turnSummary(history))
	}
	assertAssistantTurn(t, history[1], "", "", "reading files", "")
}

func TestAgentRunService_PersistsConversationWhenRunIsCancelled(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 8)
	service, conversations := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "build it")
	events <- domain.ClientEvent{TextDelta: "partial progress"}
	waitForRunEventCount(t, repo, run.ID, 1)

	if err := service.CancelRun(context.Background(), testUserID, run.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCancelled)
	history := conversations.history(testConversationID)
	if len(history) != 2 {
		t.Fatalf("expected the cancelled run to keep its partial reply, got %v", turnSummary(history))
	}
	assertAssistantTurn(t, history[1], "", "", "partial progress", "")
}

func TestAgentRunService_CancelStillMarksRunCancelledWhenPersistenceFails(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 1)
	service, conversations := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "build it")
	events <- domain.ClientEvent{TextDelta: "partial progress"}
	waitForRunEventCount(t, repo, run.ID, 1)
	conversations.failSaves(errors.New("database is locked"))

	if err := service.CancelRun(context.Background(), testUserID, run.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCancelled)
}

func TestAgentRunService_FailsRunWhenAStepCannotBeSaved(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 4)
	runCtx := make(chan context.Context, 1)
	service, conversations := newConversationRunner(t, repo, func(ctx context.Context, _ AgentChatRequest) (<-chan domain.ClientEvent, error) {
		runCtx <- ctx
		return events, nil
	})

	run := startTestRun(t, service, "build it")
	ctx := <-runCtx
	conversations.failSaves(errors.New("disk full"))
	events <- domain.ClientEvent{ToolCalls: []domain.ToolCall{{ID: "call-1", Type: "function", Function: domain.ToolCallFunction{Name: "read_file"}}}}
	events <- domain.ClientEvent{ToolResult: &domain.ToolResultPart{ToolCallID: "call-1", Name: "read_file", Content: "ok"}}

	waitForRunStatus(t, repo, run.ID, domain.AgentRunFailed)
	stored, _ := repo.GetRun(context.Background(), run.ID)
	if !strings.Contains(stored.Error, "saving conversation history") {
		t.Fatalf("expected a save failure error, got %q", stored.Error)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("expected the run to be stopped after the save failure")
	}
}

func TestAgentRunService_FailsRunWhoseRunningStatusCannotBeSaved(t *testing.T) {
	repo := newFakeAgentRunRepository()
	var failRunning sync.Once
	repo.statusErr = func(status domain.AgentRunStatus) error {
		var err error
		if status == domain.AgentRunRunning {
			failRunning.Do(func() { err = errors.New("database is locked") })
		}
		return err
	}
	runCtx := make(chan context.Context, 2)
	service, _ := newConversationRunner(t, repo, func(ctx context.Context, _ AgentChatRequest) (<-chan domain.ClientEvent, error) {
		runCtx <- ctx
		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	})

	run := startTestRun(t, service, "first")
	ctx := <-runCtx
	waitForRunStatus(t, repo, run.ID, domain.AgentRunFailed)
	stored, _ := repo.GetRun(context.Background(), run.ID)
	if !strings.Contains(stored.Error, "database is locked") {
		t.Fatalf("expected the status failure as the run's error, got %q", stored.Error)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("expected the run's stream to be stopped")
	}

	// The failed run no longer claims the conversation.
	next := startTestRun(t, service, "second")
	waitForRunStatus(t, repo, next.ID, domain.AgentRunCompleted)
}

func TestAgentRunService_RejectsSecondRunWhileConversationIsActive(t *testing.T) {
	repo := newFakeAgentRunRepository()
	first := make(chan domain.ClientEvent, 1)
	streams := []chan domain.ClientEvent{first}
	var mu sync.Mutex
	service, _ := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(streams) > 0 {
			stream := streams[0]
			streams = streams[1:]
			return stream, nil
		}
		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	})

	run := startTestRun(t, service, "first")
	_, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          "gpt-5.4",
		Message:        domain.NewTextTurn(domain.RoleUser, "second"),
	})
	if !errors.Is(err, domain.ErrConversationBusy) {
		t.Fatalf("expected ErrConversationBusy, got %v", err)
	}

	first <- domain.ClientEvent{Done: true}
	close(first)
	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	startTestRun(t, service, "third")
}

func TestAgentRunService_RunThatCannotStartLeavesConversationUnchanged(t *testing.T) {
	repo := newFakeAgentRunRepository()
	fail := true
	service, conversations := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		if fail {
			return nil, errors.New("loading workspace instructions: workspace is stopped")
		}
		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	}, domain.NewTextTurn(domain.RoleUser, "first"), domain.NewTextTurn(domain.RoleAssistant, "hello"))

	_, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          "gpt-5.4",
		Message:        domain.NewTextTurn(domain.RoleUser, "second"),
	})
	if err == nil {
		t.Fatal("expected the run to fail to start")
	}
	if got := conversations.appendCount(); got != 0 {
		t.Fatalf("expected nothing appended for a run that could not start, got %d appends", got)
	}

	// The failed run no longer claims the conversation, and retrying saves the
	// message once.
	fail = false
	run := startTestRun(t, service, "second")
	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	want := []string{"user:first", "assistant:hello", "user:second"}
	if got := turnSummary(conversations.history(testConversationID)); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected the message to be saved once, got %v", got)
	}
}

func TestAgentRunService_SlowStartDoesNotBlockOtherConversations(t *testing.T) {
	repo := newFakeAgentRunRepository()
	entered := make(chan struct{})
	release := make(chan struct{})
	service, conversations := newConversationRunner(t, repo, func(_ context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
		if req.ConversationID == testConversationID {
			// Stands in for a workspace agent that is slow to answer.
			close(entered)
			<-release
		}
		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	})
	conversations.addConversation(44, testUserID, testWorkspaceID)

	slowStarted := make(chan error, 1)
	go func() {
		_, err := service.StartRun(context.Background(), StartAgentRunRequest{
			UserID:         testUserID,
			WorkspaceID:    testWorkspaceID,
			ConversationID: testConversationID,
			Model:          "gpt-5.4",
			Message:        domain.NewTextTurn(domain.RoleUser, "slow"),
		})
		slowStarted <- err
	}()
	<-entered

	started := make(chan error, 2)
	go func() {
		_, err := service.StartRun(context.Background(), StartAgentRunRequest{
			UserID:         testUserID,
			WorkspaceID:    testWorkspaceID,
			ConversationID: 44,
			Model:          "gpt-5.4",
			Message:        domain.NewTextTurn(domain.RoleUser, "other conversation"),
		})
		started <- err
	}()
	go func() {
		_, err := service.StartRun(context.Background(), StartAgentRunRequest{
			UserID:         testUserID,
			WorkspaceID:    testWorkspaceID,
			ConversationID: testConversationID,
			Model:          "gpt-5.4",
			Message:        domain.NewTextTurn(domain.RoleUser, "same conversation"),
		})
		started <- err
	}()

	var sawBusy bool
	for i := 0; i < 2; i++ {
		select {
		case err := <-started:
			if errors.Is(err, domain.ErrConversationBusy) {
				sawBusy = true
			} else if err != nil {
				t.Fatalf("start run: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a start waited on another conversation's slow start")
		}
	}
	if !sawBusy {
		t.Fatal("expected a second start on the starting conversation to be rejected")
	}

	close(release)
	if err := <-slowStarted; err != nil {
		t.Fatalf("start slow run: %v", err)
	}
}

func TestAgentRunService_RejectsConversationOutsideWorkspace(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service, conversations := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		t.Fatal("run should not start")
		return nil, nil
	})
	conversations.addConversation(44, testUserID, testWorkspaceID+1)

	for _, conversationID := range []int64{44, 99} {
		_, err := service.StartRun(context.Background(), StartAgentRunRequest{
			UserID:         testUserID,
			WorkspaceID:    testWorkspaceID,
			ConversationID: conversationID,
			Model:          "gpt-5.4",
			Message:        domain.NewTextTurn(domain.RoleUser, "hello"),
		})
		if !errors.Is(err, domain.ErrConversationNotFound) {
			t.Fatalf("conversation %d: expected ErrConversationNotFound, got %v", conversationID, err)
		}
	}
}

func TestAgentRunService_RepairsToolCallsMissingFromSavedHistory(t *testing.T) {
	repo := newFakeAgentRunRepository()
	callA := domain.ToolCall{ID: "call-a", Type: "function", Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.go"}`}}
	callB := domain.ToolCall{ID: "call-b", Type: "function", Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"b.go"}`}}

	// An earlier run made both calls, but the saved history lost the second.
	earlier := &domain.AgentRun{UserID: testUserID, WorkspaceID: testWorkspaceID, ConversationID: testConversationID, Model: "gpt-5.4", Status: domain.AgentRunCompleted}
	if err := repo.CreateRun(context.Background(), earlier); err != nil {
		t.Fatalf("create earlier run: %v", err)
	}
	if _, err := repo.AppendEvent(context.Background(), earlier.ID, domain.ClientEvent{ToolCalls: []domain.ToolCall{callA, callB}}); err != nil {
		t.Fatalf("append earlier event: %v", err)
	}

	requests := make(chan AgentChatRequest, 1)
	service, conversations := newConversationRunner(t, repo, func(_ context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
		requests <- req
		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	},
		domain.NewTextTurn(domain.RoleUser, "read both"),
		domain.Turn{Role: domain.RoleAssistant, Parts: []domain.Part{{Kind: domain.PartToolCall, ToolCall: &callA}}},
		domain.NewToolResultTurn("call-a", "read_file", "package a", false),
		domain.NewToolResultTurn("call-b", "read_file", "package b", false),
		domain.NewTextTurn(domain.RoleAssistant, "both read"),
	)

	run := startTestRun(t, service, "again")

	want := "user:read both | assistant:call-a,call-b | result:call-a | result:call-b | assistant:both read | user:again"
	if got := strings.Join(turnSummary((<-requests).Turns), " | "); got != want {
		t.Fatalf("expected the repaired history to be sent\n got: %s\nwant: %s", got, want)
	}
	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	if got := strings.Join(turnSummary(conversations.history(testConversationID)), " | "); got != want {
		t.Fatalf("expected the repaired history to be saved\n got: %s\nwant: %s", got, want)
	}
	if conversations.replaceCount() != 1 {
		t.Fatalf("expected one repair write, got %d", conversations.replaceCount())
	}
}

func TestAgentRunService_DoesNotPersistChildRunConversation(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service, conversations := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		ch := make(chan domain.ClientEvent, 2)
		ch <- domain.ClientEvent{TextDelta: "child output"}
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	})

	parent := &domain.AgentRun{UserID: testUserID, WorkspaceID: testWorkspaceID, ConversationID: testConversationID, Model: "gpt-5.4", Status: domain.AgentRunRunning}
	if err := repo.CreateRun(context.Background(), parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	child, err := service.StartChildRun(context.Background(), parent.ID, StartChildAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          "gpt-5.4",
		Prompt:         "child",
	})
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	waitForRunStatus(t, repo, child.ID, domain.AgentRunCompleted)

	if n := conversations.appendCount(); n != 0 {
		t.Fatalf("expected child run not to write to the conversation, got %d appends", n)
	}
}

func TestAgentRunService_SubscribeEventsReplaysPersistedEvents(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 2)
	service, _ := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "hello")
	events <- domain.ClientEvent{TextDelta: "first"}
	events <- domain.ClientEvent{Done: true}
	close(events)
	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := service.SubscribeEvents(ctx, testUserID, run.ID, 1)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	event := receiveRunEvent(t, stream)
	if event.Sequence != 2 || !event.Event.Done {
		t.Fatalf("expected replayed done event at sequence 2, got %+v", event)
	}
}

func TestAgentRunService_SubscriberThatFallsBehindCatchesUp(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent)
	service, _ := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := service.SubscribeEvents(ctx, testUserID, run.ID, 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Publish far more events than the live buffer holds before reading any.
	const total = 200
	for i := 1; i < total; i++ {
		events <- domain.ClientEvent{TextDelta: "x"}
	}
	events <- domain.ClientEvent{Done: true}
	close(events)
	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)

	for want := int64(1); want <= total; want++ {
		event := receiveRunEvent(t, stream)
		if event.Sequence != want {
			t.Fatalf("expected event %d, got %d", want, event.Sequence)
		}
	}
}

func TestAgentRunService_CancelRunCancelsActiveRun(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent)
	service, _ := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "stop")
	if err := service.CancelRun(context.Background(), testUserID, run.ID); err != nil {
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
	service, _ := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	})

	parent := startTestRun(t, service, "parent")
	child, err := service.StartChildRun(context.Background(), parent.ID, StartChildAgentRunRequest{
		UserID:      testUserID,
		WorkspaceID: testWorkspaceID,
		Model:       "gpt-5.4",
		Prompt:      "child",
	})
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	if child.ParentRunID != parent.ID {
		t.Fatalf("expected parent run id %d, got %d", parent.ID, child.ParentRunID)
	}
	if got := turnSummary(child.InputTurns); len(got) != 1 || got[0] != "user:child" {
		t.Fatalf("expected the child to start from its prompt, got %v", got)
	}
}

// A sub-agent runs on a model of its parent's provider: another provider's
// model is refused before the run is made.
func TestAgentRunService_StartChildRunKeepsTheParentsProvider(t *testing.T) {
	repo := newFakeAgentRunRepository()
	var started []string
	service, _ := newConversationRunner(t, repo, func(_ context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
		started = append(started, req.Model)
		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	})

	parent := &domain.AgentRun{UserID: testUserID, WorkspaceID: testWorkspaceID, Model: testModel, Status: domain.AgentRunRunning}
	if err := repo.CreateRun(context.Background(), parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	start := func(model string) error {
		_, err := service.StartChildRun(context.Background(), parent.ID, StartChildAgentRunRequest{
			UserID:      testUserID,
			WorkspaceID: testWorkspaceID,
			Model:       model,
			Prompt:      "child",
		})
		return err
	}

	if err := start("gpt-5.4-mini"); err != nil {
		t.Fatalf("expected a child on the parent's provider to start, got %v", err)
	}
	if err := start("claude-sonnet"); !errors.Is(err, domain.ErrSubAgentProvider) {
		t.Fatalf("expected another provider's model to be refused, got %v", err)
	}
	if err := start("unknown-model"); !errors.Is(err, domain.ErrModelNotFound) {
		t.Fatalf("expected an unknown model to be refused, got %v", err)
	}
	_, err := service.StartChildRun(context.Background(), parent.ID, StartChildAgentRunRequest{
		UserID:      testUserID,
		WorkspaceID: testWorkspaceID,
		Model:       "gpt-5.4-mini",
		Prompt:      "child",
		Thinking:    &domain.ThinkingConfig{Effort: "high"},
	})
	if !errors.Is(err, domain.ErrThinkingEffortNotSupported) {
		t.Fatalf("expected an effort the model does not support to be refused, got %v", err)
	}

	if len(started) != 1 || started[0] != "gpt-5.4-mini" {
		t.Fatalf("expected only the same-provider child to start, got %v", started)
	}
	runs, err := repo.ListRuns(context.Background(), testUserID, testWorkspaceID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected no run to be made for a refused child, got %d runs", len(runs))
	}
}

func TestAgentRunService_StartChildRunLimitsActiveChildren(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service, _ := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		t.Fatal("child run should not start when active child limit is reached")
		return nil, nil
	})

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

	_, err := service.StartChildRun(context.Background(), parent.ID, StartChildAgentRunRequest{
		UserID:      8,
		WorkspaceID: 3,
		Model:       "gpt-5.4",
		Prompt:      "another child",
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
	service, _ := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		ch := make(chan domain.ClientEvent)
		close(ch)
		return ch, nil
	})

	run := startTestRun(t, service, "private")
	if _, err := service.GetRun(context.Background(), 99, run.ID); !errors.Is(err, domain.ErrAgentRunNotFound) {
		t.Fatalf("expected ErrAgentRunNotFound, got %v", err)
	}
}

func TestAgentRunService_ReasoningStateStaysWithItsModel(t *testing.T) {
	const state = `[{"id":"rs_1","type":"reasoning","encrypted_content":"enc"}]`
	transcript := []domain.Turn{
		{Role: domain.RoleUser, Parts: []domain.Part{{Kind: domain.PartText, Text: "build it"}}, DisplayText: "/build"},
		{Role: domain.RoleAssistant, Parts: []domain.Part{
			{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{Text: "tests first", State: []byte(state)}},
			{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{ID: "call-1", ItemID: "fc_1", Type: "function"}},
		}},
		domain.NewToolResultTurn("call-1", "read_file", "contents", false),
	}

	for _, tt := range []struct {
		name         string
		convModel    string
		wantState    string
		wantItem     string
		wantReplaced int
	}{
		{name: "same model keeps it", convModel: "gpt-6.2-sol", wantState: state, wantItem: "fc_1"},
		{name: "another model drops it", convModel: "gpt-5.5", wantReplaced: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeAgentRunRepository()
			var sent []domain.Turn
			service, conversations := newConversationRunner(t, repo, func(_ context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
				sent = req.Turns
				events := make(chan domain.ClientEvent)
				close(events)
				return events, nil
			}, transcript...)
			if err := conversations.SetConversationModel(context.Background(), testConversationID, testUserID, tt.convModel); err != nil {
				t.Fatalf("set model: %v", err)
			}

			if _, err := service.StartRun(context.Background(), StartAgentRunRequest{
				UserID:         testUserID,
				WorkspaceID:    testWorkspaceID,
				ConversationID: testConversationID,
				Model:          "gpt-6.2-sol",
				Message:        domain.NewTextTurn(domain.RoleUser, "go on"),
			}); err != nil {
				t.Fatalf("start run: %v", err)
			}

			for name, turns := range map[string][]domain.Turn{"sent": sent, "saved": conversations.history(testConversationID)} {
				if got := turnSummary(turns); len(got) != 4 || got[3] != "user:go on" {
					t.Fatalf("%s: expected the history and the message, got %v", name, got)
				}
				if got := turns[0].DisplayText; got != "/build" {
					t.Errorf("%s: display text = %q, want what the user typed", name, got)
				}
				assistant := turns[1]
				if got := string(assistant.ThinkingState()); got != tt.wantState {
					t.Errorf("%s: reasoning state = %q, want %q", name, got, tt.wantState)
				}
				if assistant.ThinkingText() != "tests first" {
					t.Errorf("%s: reasoning text = %q", name, assistant.ThinkingText())
				}
				if call := assistant.ToolCalls()[0]; call.ID != "call-1" || call.ItemID != tt.wantItem {
					t.Errorf("%s: tool call = %+v, want item ID %q", name, call, tt.wantItem)
				}
			}
			if got := conversations.replaceCount(); got != tt.wantReplaced {
				t.Errorf("history rewritten %d times, want %d", got, tt.wantReplaced)
			}
			if got := conversations.conversationModel(testConversationID); got != "gpt-6.2-sol" {
				t.Errorf("conversation model = %q, want the run's", got)
			}
		})
	}
}

func TestAgentRunService_SeedsANewConversation(t *testing.T) {
	repo := newFakeAgentRunRepository()
	requests := make(chan AgentChatRequest, 1)
	service, conversations := newConversationRunner(t, repo, func(_ context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
		requests <- req
		ch := make(chan domain.ClientEvent, 1)
		ch <- domain.ClientEvent{Done: true}
		close(ch)
		return ch, nil
	})

	run, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          testModel,
		Seed:           []domain.Turn{domain.NewTextTurn(domain.RoleUser, "earlier"), domain.NewTextTurn(domain.RoleAssistant, "done that")},
		Message:        domain.NewTextTurn(domain.RoleUser, "next round"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	want := "user:earlier,assistant:done that,user:next round"
	if got := strings.Join(turnSummary((<-requests).Turns), ","); got != want {
		t.Fatalf("expected the model to get the seed and the message, got %s", got)
	}
	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	if got := strings.Join(turnSummary(conversations.history(testConversationID)), ","); got != want {
		t.Fatalf("expected the seed and the message saved, got %s", got)
	}

	// The conversation has turns now, so it can no longer be seeded.
	if _, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          testModel,
		Seed:           []domain.Turn{domain.NewTextTurn(domain.RoleUser, "earlier")},
		Message:        domain.NewTextTurn(domain.RoleUser, "again"),
	}); err == nil {
		t.Fatal("expected seeding a conversation with turns to be refused")
	}
	if got := len(conversations.history(testConversationID)); got != 3 {
		t.Fatalf("expected the refused start to leave the history alone, got %d turns", got)
	}
}

func TestAgentRunService_RefusesAnEmptyMessage(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service, _ := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		t.Fatal("run should not start")
		return nil, nil
	})
	if _, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          testModel,
		Message:        domain.NewTextTurn(domain.RoleUser, "  "),
	}); err == nil {
		t.Fatal("expected a blank message to be refused")
	}
}

func TestAgentRunService_FailedStartEndsRunWhenEventsCannotBeSaved(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service, _ := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		return nil, errors.New("workspace is stopped")
	})

	repo.failAppends(errors.New("disk full"))
	if _, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          testModel,
		Message:        domain.NewTextTurn(domain.RoleUser, "build it"),
	}); err == nil {
		t.Fatal("expected the run to fail to start")
	}
	runs, err := repo.ListRuns(context.Background(), testUserID, testWorkspaceID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != domain.AgentRunFailed {
		t.Fatalf("expected the run to be marked failed without its events, got %+v", runs)
	}
}

func TestAgentRunService_CancelStillMarksRunCancelledWhenEventsCannotBeSaved(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{}, nil)
	defer shutdownRunner(t, service)
	run := &domain.AgentRun{UserID: 7, WorkspaceID: 9, Model: "gpt-5.4", Status: domain.AgentRunQueued}
	if err := repo.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// A run with no live cancel func, such as one left queued, is ended directly.
	repo.failAppends(errors.New("disk full"))
	if err := service.CancelRun(context.Background(), 7, run.ID); err == nil {
		t.Fatal("expected the event failure to be reported")
	}
	stored, err := repo.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if stored.Status != domain.AgentRunCancelled {
		t.Fatalf("expected the run to be cancelled, got %s", stored.Status)
	}
}

func TestAgentRunService_RunIsEndedBeforeViewersSeeItEnd(t *testing.T) {
	tests := []struct {
		name   string
		end    func(t *testing.T, service *agentRunService, runID int64, events chan<- domain.ClientEvent)
		status domain.AgentRunStatus
	}{
		{
			name: "completed",
			end: func(_ *testing.T, _ *agentRunService, _ int64, events chan<- domain.ClientEvent) {
				events <- domain.ClientEvent{Done: true}
			},
			status: domain.AgentRunCompleted,
		},
		{
			name: "cancelled",
			end: func(t *testing.T, service *agentRunService, runID int64, _ chan<- domain.ClientEvent) {
				if err := service.CancelRun(context.Background(), testUserID, runID); err != nil {
					t.Fatalf("cancel run: %v", err)
				}
			},
			status: domain.AgentRunCancelled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeAgentRunRepository()
			// A slow final status write would let a viewer see the run end
			// while it still counts as active, if the status came second.
			repo.beforeStatus = func(status domain.AgentRunStatus) {
				if domain.AgentRunStatusTerminal(status) {
					time.Sleep(50 * time.Millisecond)
				}
			}
			events := make(chan domain.ClientEvent, 2)
			service, conversations := newConversationRunner(t, repo, streamFrom(events))
			run := startTestRun(t, service, "build it")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			subscription, err := service.SubscribeEvents(ctx, testUserID, run.ID, 0)
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			// Receiving an event first means the subscription has replayed what
			// was stored and only sees events as they are published.
			events <- domain.ClientEvent{TextDelta: "working"}
			if event := receiveRunEvent(t, subscription); event.Event.TextDelta != "working" {
				t.Fatalf("expected the working event first, got %+v", event.Event)
			}
			tt.end(t, service, run.ID, events)

			for {
				event := receiveRunEvent(t, subscription)
				if !event.Event.Done {
					continue
				}
				stored, err := repo.GetRun(context.Background(), run.ID)
				if err != nil {
					t.Fatalf("get run: %v", err)
				}
				if stored.Status != tt.status {
					t.Fatalf("expected the run to be %s when viewers see it end, got %s", tt.status, stored.Status)
				}
				// Whatever acts on the end finds the whole history saved.
				if got := turnSummary(conversations.history(testConversationID)); len(got) != 2 || got[1] != "assistant:working" {
					t.Fatalf("expected the reply saved when viewers see the run end, got %v", got)
				}
				return
			}
		})
	}
}

func TestAgentRunService_ChildRunSharesItsParentsBudget(t *testing.T) {
	repo := newFakeAgentRunRepository()
	var mu sync.Mutex
	var budgets []*domain.Budget
	release := make(chan struct{})
	service, _ := newConversationRunner(t, repo, func(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
		mu.Lock()
		budgets = append(budgets, req.Budget)
		mu.Unlock()
		// Each run stays active until the test ends.
		ch := make(chan domain.ClientEvent)
		go func() {
			defer close(ch)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}()
		return ch, nil
	})
	defer close(release)

	budget := domain.NewBudget(5, 0, nil)
	parent, err := service.StartRun(context.Background(), StartAgentRunRequest{
		UserID:         testUserID,
		WorkspaceID:    testWorkspaceID,
		ConversationID: testConversationID,
		Model:          testModel,
		Message:        domain.NewTextTurn(domain.RoleUser, "parent"),
		Budget:         budget,
	})
	if err != nil {
		t.Fatalf("start parent: %v", err)
	}
	if _, err := service.StartChildRun(context.Background(), parent.ID, StartChildAgentRunRequest{
		UserID:      testUserID,
		WorkspaceID: testWorkspaceID,
		Model:       testModel,
		Prompt:      "child",
	}); err != nil {
		t.Fatalf("start child: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(budgets) != 2 || budgets[0] != budget || budgets[1] != budget {
		t.Fatalf("expected parent and child to share the parent's budget, got %v", budgets)
	}
}

func TestAgentRunService_NotifiesListenersWhenRunsFinish(t *testing.T) {
	repo := newFakeAgentRunRepository()
	streams := make(chan chan domain.ClientEvent, 2)
	service, _ := newConversationRunner(t, repo, func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
		return <-streams, nil
	})

	finished := make(chan domain.AgentRun, 4)
	service.OnRunFinished(func(run domain.AgentRun) { finished <- run })

	start := func() (*domain.AgentRun, chan domain.ClientEvent) {
		t.Helper()
		events := make(chan domain.ClientEvent, 4)
		streams <- events
		return startTestRun(t, service, "go"), events
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
	if err := service.CancelRun(context.Background(), testUserID, cancelled.ID); err != nil {
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

func TestAgentRunService_StartupInterruptsLeftoverRuns(t *testing.T) {
	repo := newFakeAgentRunRepository()
	ctx := context.Background()
	left := &domain.AgentRun{UserID: 3, WorkspaceID: 4, ConversationID: 21, Model: "m", Status: domain.AgentRunRunning, InputTurns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "build it")}}
	repo.CreateRun(ctx, left)
	repo.AppendEvent(ctx, left.ID, domain.ClientEvent{TextDelta: "halfway there"})
	done := &domain.AgentRun{UserID: 3, WorkspaceID: 4, Model: "m", Status: domain.AgentRunCompleted}
	repo.CreateRun(ctx, done)

	conversations := newFakeConversationService()
	conversations.addConversation(21, 3, 4, domain.NewTextTurn(domain.RoleUser, "build it"))
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
	// The history holds the steps the run completed, saved as they did.
	if got := conversations.appendCount() + conversations.replaceCount(); got != 0 {
		t.Fatalf("expected the history left as it was saved, got %d writes", got)
	}
}

func TestAgentRunService_ShutdownInterruptsActiveRunsAndKeepsTheirReply(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service, conversations := newConversationRunner(t, repo, func(ctx context.Context, _ AgentChatRequest) (<-chan domain.ClientEvent, error) {
		events := make(chan domain.ClientEvent)
		go func() {
			defer close(events)
			select {
			case events <- domain.ClientEvent{TextDelta: "halfway"}:
			case <-ctx.Done():
				return
			}
			<-ctx.Done()
		}()
		return events, nil
	})
	finished := make(chan domain.AgentRun, 1)
	service.OnRunFinished(func(run domain.AgentRun) { finished <- run })

	run := startTestRun(t, service, "go")
	waitForRunEventCount(t, repo, run.ID, 1)
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
	if got := strings.Join(turnSummary(conversations.history(testConversationID)), ","); got != "user:go,assistant:halfway" {
		t.Fatalf("an interrupted run should keep what it did, got %s", got)
	}
}

// The orchestrator closes its stream when a run's context ends; a cancelled
// run must never be recorded as completed when the closed stream wins the
// race with ctx.Done.
func TestAgentRunService_CancelledRunIsNeverRecordedAsCompleted(t *testing.T) {
	for i := 0; i < 50; i++ {
		repo := newFakeAgentRunRepository()
		service, _ := newConversationRunner(t, repo, func(ctx context.Context, _ AgentChatRequest) (<-chan domain.ClientEvent, error) {
			events := make(chan domain.ClientEvent)
			go func() {
				<-ctx.Done()
				close(events)
			}()
			return events, nil
		})
		run := startTestRun(t, service, "go")
		waitForRunStatus(t, repo, run.ID, domain.AgentRunRunning)
		if err := service.CancelRun(context.Background(), testUserID, run.ID); err != nil {
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

func TestAgentRunService_RecordsUsageEventsOnTheRun(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 4)
	service, _ := newConversationRunner(t, repo, streamFrom(events))

	run := startTestRun(t, service, "build it")

	first, second := 0.01, 0.02
	events <- domain.ClientEvent{Usage: &domain.Usage{InputTokens: 100, OutputTokens: 10, CostUSD: &first}}
	events <- domain.ClientEvent{Usage: &domain.Usage{InputTokens: 50, OutputTokens: 5, CacheReadTokens: 100, CostUSD: &second}}
	events <- domain.ClientEvent{Done: true}
	close(events)

	waitForRunStatus(t, repo, run.ID, domain.AgentRunCompleted)
	stored, err := repo.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	usage := stored.Usage
	if usage.InputTokens != 150 || usage.OutputTokens != 15 || usage.CacheReadTokens != 100 {
		t.Fatalf("unexpected usage totals: %+v", usage)
	}
	if usage.CostUSD == nil || *usage.CostUSD != first+second {
		t.Fatalf("expected cost %v, got %v", first+second, usage.CostUSD)
	}

	storedEvents, err := repo.ListEvents(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(storedEvents) != 3 || storedEvents[0].Event.Usage == nil {
		t.Fatalf("expected usage events kept in the run's log, got %+v", storedEvents)
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

func waitForHistoryLength(t *testing.T, conversations *fakeConversationService, length int) []domain.Turn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if history := conversations.history(testConversationID); len(history) >= length {
			return history
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d saved turns, have %v", length, turnSummary(conversations.history(testConversationID)))
	return nil
}

// turnSummary describes turns compactly: "user:<text>", "assistant:<text or
// tool call IDs>", or "result:<tool call ID>".
func turnSummary(turns []domain.Turn) []string {
	summary := make([]string, 0, len(turns))
	for _, turn := range turns {
		if result := turn.ToolResult(); result != nil {
			summary = append(summary, "result:"+result.ToolCallID)
			continue
		}
		if calls := turn.ToolCalls(); len(calls) > 0 {
			ids := make([]string, 0, len(calls))
			for _, call := range calls {
				ids = append(ids, call.ID)
			}
			summary = append(summary, string(turn.Role)+":"+strings.Join(ids, ","))
			continue
		}
		summary = append(summary, string(turn.Role)+":"+turn.Text())
	}
	return summary
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
