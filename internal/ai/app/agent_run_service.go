package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const runnerShutdownMessage = "agent run stopped because the server restarted or shut down"

// AgentRunRepository persists background agent runs and their ordered event log.
type AgentRunRepository interface {
	CreateRun(ctx context.Context, run *domain.AgentRun) error
	GetRun(ctx context.Context, id int64) (*domain.AgentRun, error)
	ListRuns(ctx context.Context, userID, workspaceID int64) ([]domain.AgentRun, error)
	ListEvents(ctx context.Context, runID, afterSequence int64) ([]domain.AgentRunEvent, error)
	AppendEvent(ctx context.Context, runID int64, event domain.ClientEvent) (domain.AgentRunEvent, error)
	UpdateStatus(ctx context.Context, runID int64, status domain.AgentRunStatus, errorMessage string) error
	MarkActiveRunsFailed(ctx context.Context, message string) error
}

// StartAgentRunRequest contains all inputs needed to start a background run.
type StartAgentRunRequest struct {
	UserID         int64
	WorkspaceID    int64
	ConversationID int64
	ParentRunID    int64
	AgentID        string
	Model          string
	Turns          []domain.Turn
	Thinking       *domain.ThinkingConfig
}

// AgentRunService manages browser-independent AI agent execution.
type AgentRunService interface {
	StartRun(ctx context.Context, req StartAgentRunRequest) (*domain.AgentRun, error)
	StartChildRun(ctx context.Context, parentRunID int64, req StartAgentRunRequest) (*domain.AgentRun, error)
	GetRun(ctx context.Context, userID, runID int64) (*domain.AgentRun, error)
	ListRuns(ctx context.Context, userID, workspaceID int64) ([]domain.AgentRun, error)
	ListEvents(ctx context.Context, userID, runID, afterSequence int64) ([]domain.AgentRunEvent, error)
	SubscribeEvents(ctx context.Context, userID, runID, afterSequence int64) (<-chan domain.AgentRunEvent, error)
	CancelRun(ctx context.Context, userID, runID int64) error
	Shutdown(ctx context.Context) error
}

type agentEventBroker interface {
	Subscribe(runID int64) (<-chan domain.AgentRunEvent, func())
	Publish(event domain.AgentRunEvent)
}

type agentRunService struct {
	repo          AgentRunRepository
	chat          ChatService
	conversations ConversationService
	agents        AgentService
	broker        agentEventBroker

	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         sync.WaitGroup

	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}

// NewAgentRunService creates an agent runner that owns background run lifecycles.
func NewAgentRunService(ctx context.Context, repo AgentRunRepository, chat ChatService, conversations ConversationService) *agentRunService {
	if ctx == nil {
		ctx = context.Background()
	}
	rootCtx, cancel := context.WithCancel(ctx)
	service := &agentRunService{
		repo:          repo,
		chat:          chat,
		conversations: conversations,
		broker:        newMemoryAgentEventBroker(),
		rootCtx:       rootCtx,
		rootCancel:    cancel,
		cancels:       make(map[int64]context.CancelFunc),
	}
	if err := service.repo.MarkActiveRunsFailed(context.Background(), runnerShutdownMessage); err != nil {
		log.Printf("failed to reconcile active agent runs: %v", err)
	}
	return service
}

// WithAgentService configures the agent lookup dependency.
func (s *agentRunService) WithAgentService(agents AgentService) *agentRunService {
	s.agents = agents
	return s
}

func (s *agentRunService) StartRun(ctx context.Context, req StartAgentRunRequest) (*domain.AgentRun, error) {
	if err := validateStartAgentRunRequest(req); err != nil {
		return nil, err
	}
	if req.ParentRunID != 0 {
		parent, err := s.getOwnedRun(ctx, req.UserID, req.ParentRunID)
		if err != nil {
			return nil, fmt.Errorf("validating parent run: %w", err)
		}
		if parent.WorkspaceID != req.WorkspaceID {
			return nil, fmt.Errorf("parent run workspace mismatch: %w", domain.ErrAgentRunNotFound)
		}
	}
	agent, err := s.resolveAgent(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resolving agent: %w", err)
	}
	if req.ParentRunID == 0 && req.ConversationID > 0 && s.conversations != nil {
		if err := s.conversations.SaveTurns(ctx, req.ConversationID, req.UserID, cloneTurns(req.Turns)); err != nil {
			return nil, fmt.Errorf("saving initial agent run conversation: %w", err)
		}
	}

	run := &domain.AgentRun{
		ParentRunID:    req.ParentRunID,
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		ConversationID: req.ConversationID,
		AgentID:        agent.ID,
		Model:          req.Model,
		Status:         domain.AgentRunQueued,
		InputTurns:     cloneTurns(req.Turns),
		Thinking:       cloneThinking(req.Thinking),
	}
	if err := s.repo.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("creating agent run: %w", err)
	}

	runCtx, cancel := context.WithCancel(s.rootCtx)
	s.registerCancel(run.ID, cancel)

	stream, err := s.chat.StreamAgent(runCtx, AgentChatRequest{
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		CurrentRunID:   run.ID,
		ConversationID: req.ConversationID,
		Model:          req.Model,
		Turns:          cloneTurns(req.Turns),
		Thinking:       cloneThinking(req.Thinking),
		AgentPrompt:    agent.Instructions,
	})
	if err != nil {
		s.unregisterCancel(run.ID)
		cancel()
		if updateErr := s.failRun(run.ID, err.Error()); updateErr != nil {
			log.Printf("failed to mark agent run %d failed: %v", run.ID, updateErr)
		}
		return nil, err
	}

	s.wg.Add(1)
	go s.consumeRun(runCtx, run.ID, stream)

	created, err := s.repo.GetRun(ctx, run.ID)
	if err != nil {
		return nil, fmt.Errorf("loading created agent run: %w", err)
	}
	return created, nil
}

func (s *agentRunService) resolveAgent(ctx context.Context, req StartAgentRunRequest) (*domain.Agent, error) {
	if s.agents == nil {
		agent := domain.DefaultAgent()
		return &agent, nil
	}
	agent, err := s.agents.GetAgent(ctx, req.UserID, req.WorkspaceID, req.AgentID)
	if err != nil {
		return nil, err
	}
	return agent, nil
}

func (s *agentRunService) StartChildRun(ctx context.Context, parentRunID int64, req StartAgentRunRequest) (*domain.AgentRun, error) {
	if parentRunID <= 0 {
		return nil, domain.ErrAgentRunNotFound
	}
	req.ParentRunID = parentRunID
	return s.StartRun(ctx, req)
}

func (s *agentRunService) GetRun(ctx context.Context, userID, runID int64) (*domain.AgentRun, error) {
	return s.getOwnedRun(ctx, userID, runID)
}

func (s *agentRunService) ListRuns(ctx context.Context, userID, workspaceID int64) ([]domain.AgentRun, error) {
	if workspaceID <= 0 {
		return nil, fmt.Errorf("workspace ID is required")
	}
	runs, err := s.repo.ListRuns(ctx, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing agent runs: %w", err)
	}
	return runs, nil
}

func (s *agentRunService) ListEvents(ctx context.Context, userID, runID, afterSequence int64) ([]domain.AgentRunEvent, error) {
	if _, err := s.getOwnedRun(ctx, userID, runID); err != nil {
		return nil, err
	}
	events, err := s.repo.ListEvents(ctx, runID, afterSequence)
	if err != nil {
		return nil, fmt.Errorf("listing agent run events: %w", err)
	}
	return events, nil
}

func (s *agentRunService) SubscribeEvents(ctx context.Context, userID, runID, afterSequence int64) (<-chan domain.AgentRunEvent, error) {
	if _, err := s.getOwnedRun(ctx, userID, runID); err != nil {
		return nil, err
	}

	liveEvents, unsubscribe := s.broker.Subscribe(runID)
	out := make(chan domain.AgentRunEvent)
	go func() {
		defer close(out)
		defer unsubscribe()

		lastSequence := afterSequence
		replay, err := s.repo.ListEvents(ctx, runID, afterSequence)
		if err != nil {
			log.Printf("failed to replay agent run %d events: %v", runID, err)
			return
		}
		for _, event := range replay {
			if event.Sequence <= lastSequence {
				continue
			}
			if !sendRunEvent(ctx, out, event) {
				return
			}
			lastSequence = event.Sequence
		}

		for {
			select {
			case event, ok := <-liveEvents:
				if !ok {
					return
				}
				if event.Sequence <= lastSequence {
					continue
				}
				if !sendRunEvent(ctx, out, event) {
					return
				}
				lastSequence = event.Sequence
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

func (s *agentRunService) CancelRun(ctx context.Context, userID, runID int64) error {
	run, err := s.getOwnedRun(ctx, userID, runID)
	if err != nil {
		return err
	}
	if domain.AgentRunStatusTerminal(run.Status) {
		return domain.ErrAgentRunNotActive
	}

	cancel, ok := s.cancelForRun(runID)
	if ok {
		cancel()
		return nil
	}

	if err := s.cancelRun(runID, "agent run cancelled"); err != nil {
		return fmt.Errorf("cancelling agent run: %w", err)
	}
	return nil
}

// Shutdown cancels active background runs and waits until they stop or ctx expires.
func (s *agentRunService) Shutdown(ctx context.Context) error {
	s.rootCancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutting down agent runner: %w", ctx.Err())
	}
}

func (s *agentRunService) consumeRun(ctx context.Context, runID int64, stream <-chan domain.ClientEvent) {
	defer s.wg.Done()
	defer s.unregisterCancel(runID)

	if err := s.repo.UpdateStatus(context.Background(), runID, domain.AgentRunRunning, ""); err != nil {
		log.Printf("failed to mark agent run %d running: %v", runID, err)
		return
	}

	status := domain.AgentRunRunning
	errorMessage := ""
	for {
		select {
		case event, ok := <-stream:
			if !ok {
				s.finishRun(runID, status, errorMessage)
				return
			}
			if event.Approval != nil && status != domain.AgentRunWaitingApproval {
				if err := s.repo.UpdateStatus(context.Background(), runID, domain.AgentRunWaitingApproval, ""); err != nil {
					log.Printf("failed to mark agent run %d waiting for approval: %v", runID, err)
				} else {
					status = domain.AgentRunWaitingApproval
				}
			}
			if event.Approval == nil && status == domain.AgentRunWaitingApproval && !event.Done {
				if err := s.repo.UpdateStatus(context.Background(), runID, domain.AgentRunRunning, ""); err != nil {
					log.Printf("failed to mark agent run %d running after approval: %v", runID, err)
				} else {
					status = domain.AgentRunRunning
				}
			}
			if event.ErrorMessage != "" {
				errorMessage = event.ErrorMessage
			}
			if err := s.appendAndPublish(runID, event); err != nil {
				log.Printf("failed to persist agent run %d event: %v", runID, err)
				errorMessage = err.Error()
				s.finishRun(runID, domain.AgentRunFailed, errorMessage)
				return
			}
			if event.Done {
				s.finishRun(runID, status, errorMessage)
				return
			}
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				if err := s.cancelRun(runID, "agent run cancelled"); err != nil {
					log.Printf("failed to mark agent run %d cancelled: %v", runID, err)
				}
				return
			}
			errorMessage = ctx.Err().Error()
			s.finishRun(runID, domain.AgentRunFailed, errorMessage)
			return
		}
	}
}

func (s *agentRunService) finishRun(runID int64, currentStatus domain.AgentRunStatus, errorMessage string) {
	status := domain.AgentRunCompleted
	if errorMessage != "" || currentStatus == domain.AgentRunFailed {
		status = domain.AgentRunFailed
	}
	if status == domain.AgentRunCompleted {
		if err := s.persistCompletedRunConversation(context.Background(), runID); err != nil {
			log.Printf("failed to persist completed agent run %d conversation: %v", runID, err)
			status = domain.AgentRunFailed
			errorMessage = fmt.Sprintf("saving completed conversation transcript: %v", err)
		}
	}
	if err := s.repo.UpdateStatus(context.Background(), runID, status, errorMessage); err != nil {
		log.Printf("failed to finish agent run %d: %v", runID, err)
	}
}

func (s *agentRunService) persistCompletedRunConversation(ctx context.Context, runID int64) error {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("getting agent run: %w", err)
	}
	if run == nil {
		return domain.ErrAgentRunNotFound
	}
	if !s.shouldPersistRunConversation(run) {
		return nil
	}

	events, err := s.repo.ListEvents(ctx, runID, 0)
	if err != nil {
		return fmt.Errorf("listing agent run events: %w", err)
	}
	turns := buildAgentRunConversationTurns(run.InputTurns, events)
	if err := s.conversations.SaveTurns(ctx, run.ConversationID, run.UserID, turns); err != nil {
		return fmt.Errorf("saving conversation turns: %w", err)
	}
	return nil
}

func (s *agentRunService) shouldPersistRunConversation(run *domain.AgentRun) bool {
	return s.conversations != nil && run != nil && run.ConversationID > 0 && run.ParentRunID == 0
}

func (s *agentRunService) failRun(runID int64, message string) error {
	if err := s.appendAndPublish(runID, domain.ClientEvent{ErrorMessage: message}); err != nil {
		return err
	}
	if err := s.appendAndPublish(runID, domain.ClientEvent{Done: true}); err != nil {
		return err
	}
	return s.repo.UpdateStatus(context.Background(), runID, domain.AgentRunFailed, message)
}

func (s *agentRunService) cancelRun(runID int64, message string) error {
	if err := s.appendAndPublish(runID, domain.ClientEvent{ErrorMessage: message}); err != nil {
		return err
	}
	if err := s.appendAndPublish(runID, domain.ClientEvent{Done: true}); err != nil {
		return err
	}
	return s.repo.UpdateStatus(context.Background(), runID, domain.AgentRunCancelled, message)
}

func (s *agentRunService) appendAndPublish(runID int64, event domain.ClientEvent) error {
	stored, err := s.repo.AppendEvent(context.Background(), runID, event)
	if err != nil {
		return err
	}
	s.broker.Publish(stored)
	return nil
}

func (s *agentRunService) getOwnedRun(ctx context.Context, userID, runID int64) (*domain.AgentRun, error) {
	if runID <= 0 {
		return nil, domain.ErrAgentRunNotFound
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("getting agent run: %w", err)
	}
	if run == nil || run.UserID != userID {
		return nil, domain.ErrAgentRunNotFound
	}
	return run, nil
}

func (s *agentRunService) registerCancel(runID int64, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels[runID] = cancel
}

func (s *agentRunService) unregisterCancel(runID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, runID)
}

func (s *agentRunService) cancelForRun(runID int64) (context.CancelFunc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cancel, ok := s.cancels[runID]
	return cancel, ok
}

func validateStartAgentRunRequest(req StartAgentRunRequest) error {
	switch {
	case req.UserID <= 0:
		return fmt.Errorf("user ID is required")
	case req.WorkspaceID <= 0:
		return fmt.Errorf("workspace ID is required")
	case req.Model == "":
		return fmt.Errorf("model is required")
	case len(req.Turns) == 0:
		return fmt.Errorf("turns are required")
	default:
		return nil
	}
}

func sendRunEvent(ctx context.Context, out chan<- domain.AgentRunEvent, event domain.AgentRunEvent) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func cloneThinking(thinking *domain.ThinkingConfig) *domain.ThinkingConfig {
	if thinking == nil {
		return nil
	}
	clone := &domain.ThinkingConfig{}
	if thinking.Enabled != nil {
		enabled := *thinking.Enabled
		clone.Enabled = &enabled
	}
	return clone
}

func cloneTurns(turns []domain.Turn) []domain.Turn {
	if turns == nil {
		return nil
	}
	cloned := make([]domain.Turn, 0, len(turns))
	for _, turn := range turns {
		clonedTurn := domain.Turn{Role: turn.Role}
		for _, part := range turn.Parts {
			clonedTurn.Parts = append(clonedTurn.Parts, clonePart(part))
		}
		cloned = append(cloned, clonedTurn)
	}
	return cloned
}

func clonePart(part domain.Part) domain.Part {
	cloned := domain.Part{
		Kind: part.Kind,
		Text: part.Text,
	}
	if part.Thinking != nil {
		cloned.Thinking = &domain.ThinkingPart{
			Text:  part.Thinking.Text,
			State: domain.CloneRawMessage(part.Thinking.State),
		}
	}
	if part.ToolCall != nil {
		toolCall := *part.ToolCall
		cloned.ToolCall = &toolCall
	}
	if part.ToolResult != nil {
		toolResult := *part.ToolResult
		cloned.ToolResult = &toolResult
	}
	return cloned
}

type memoryAgentEventBroker struct {
	mu          sync.Mutex
	subscribers map[int64]map[chan domain.AgentRunEvent]struct{}
}

func newMemoryAgentEventBroker() *memoryAgentEventBroker {
	return &memoryAgentEventBroker{subscribers: make(map[int64]map[chan domain.AgentRunEvent]struct{})}
}

func (b *memoryAgentEventBroker) Subscribe(runID int64) (<-chan domain.AgentRunEvent, func()) {
	ch := make(chan domain.AgentRunEvent, 64)
	b.mu.Lock()
	if b.subscribers[runID] == nil {
		b.subscribers[runID] = make(map[chan domain.AgentRunEvent]struct{})
	}
	b.subscribers[runID][ch] = struct{}{}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subscribers, ok := b.subscribers[runID]; ok {
			delete(subscribers, ch)
			if len(subscribers) == 0 {
				delete(b.subscribers, runID)
			}
		}
	}
	return ch, unsubscribe
}

func (b *memoryAgentEventBroker) Publish(event domain.AgentRunEvent) {
	b.mu.Lock()
	subscribers := make([]chan domain.AgentRunEvent, 0, len(b.subscribers[event.RunID]))
	for ch := range b.subscribers[event.RunID] {
		subscribers = append(subscribers, ch)
	}
	b.mu.Unlock()

	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

var _ AgentRunService = (*agentRunService)(nil)
