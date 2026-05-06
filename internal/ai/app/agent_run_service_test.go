package app

import (
	"context"
	"errors"
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
	if (status == domain.AgentRunRunning || status == domain.AgentRunWaitingApproval) && run.StartedAt == nil {
		run.StartedAt = &now
	}
	if domain.AgentRunStatusTerminal(status) {
		run.CompletedAt = &now
	}
	return nil
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
	service := NewAgentRunService(context.Background(), repo, chat)
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

func TestAgentRunService_SubscribeEventsReplaysPersistedEvents(t *testing.T) {
	repo := newFakeAgentRunRepository()
	events := make(chan domain.ClientEvent, 2)
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			return events, nil
		},
	})
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
	})
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
	})
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

func TestAgentRunService_GetRunEnforcesOwnership(t *testing.T) {
	repo := newFakeAgentRunRepository()
	service := NewAgentRunService(context.Background(), repo, fakeRunnerChatService{
		streamAgentFn: func(context.Context, AgentChatRequest) (<-chan domain.ClientEvent, error) {
			ch := make(chan domain.ClientEvent)
			close(ch)
			return ch, nil
		},
	})
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
