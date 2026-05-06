package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/domain"
)

type fakeAgentRunService struct {
	run    *domain.AgentRun
	events []domain.AgentRunEvent
	stream <-chan domain.AgentRunEvent
}

var _ app.AgentRunService = fakeAgentRunService{}

func (s fakeAgentRunService) StartRun(context.Context, app.StartAgentRunRequest) (*domain.AgentRun, error) {
	return nil, nil
}

func (s fakeAgentRunService) StartChildRun(context.Context, int64, app.StartAgentRunRequest) (*domain.AgentRun, error) {
	return nil, nil
}

func (s fakeAgentRunService) GetRun(context.Context, int64, int64) (*domain.AgentRun, error) {
	if s.run == nil {
		return nil, domain.ErrAgentRunNotFound
	}
	return s.run, nil
}

func (s fakeAgentRunService) ListRuns(context.Context, int64, int64) ([]domain.AgentRun, error) {
	return nil, nil
}

func (s fakeAgentRunService) ListEvents(context.Context, int64, int64, int64) ([]domain.AgentRunEvent, error) {
	return s.events, nil
}

func (s fakeAgentRunService) SubscribeEvents(context.Context, int64, int64, int64) (<-chan domain.AgentRunEvent, error) {
	if s.stream != nil {
		return s.stream, nil
	}
	ch := make(chan domain.AgentRunEvent)
	close(ch)
	return ch, nil
}

func (s fakeAgentRunService) CancelRun(context.Context, int64, int64) error {
	return nil
}

func (s fakeAgentRunService) Shutdown(context.Context) error {
	return nil
}

func TestChildAgentRunStarterWaitRejectsRunOutsideParent(t *testing.T) {
	starter := childAgentRunStarter{runs: fakeAgentRunService{
		run: &domain.AgentRun{
			ID:          202,
			ParentRunID: 999,
			UserID:      7,
			WorkspaceID: 9,
			Status:      domain.AgentRunCompleted,
		},
	}}

	_, err := starter.WaitChildAgentRun(context.Background(), 7, 101, 202)
	if !errors.Is(err, domain.ErrAgentRunNotFound) {
		t.Fatalf("expected child run not found, got %v", err)
	}
}

func TestChildAgentRunStarterWaitReturnsTerminalSummary(t *testing.T) {
	starter := childAgentRunStarter{runs: fakeAgentRunService{
		run: &domain.AgentRun{
			ID:          202,
			ParentRunID: 101,
			UserID:      7,
			WorkspaceID: 9,
			Status:      domain.AgentRunCompleted,
		},
		events: []domain.AgentRunEvent{
			{RunID: 202, Sequence: 1, Event: domain.ClientEvent{TextDelta: "Backend "}},
			{RunID: 202, Sequence: 2, Event: domain.ClientEvent{TextDelta: "summary"}},
			{RunID: 202, Sequence: 3, Event: domain.ClientEvent{Done: true}},
		},
	}}

	result, err := starter.WaitChildAgentRun(context.Background(), 7, 101, 202)
	if err != nil {
		t.Fatalf("expected wait success, got %v", err)
	}
	if result.RunID != 202 || result.Status != string(domain.AgentRunCompleted) || result.Summary != "Backend summary" {
		t.Fatalf("unexpected child result: %+v", result)
	}
}
