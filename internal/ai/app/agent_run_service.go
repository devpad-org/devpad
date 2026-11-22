package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	runnerShutdownMessage       = domain.AgentRunInterruptedError
	maxActiveChildRunsPerParent = 3
)

// AgentRunRepository persists background agent runs and their ordered event log.
type AgentRunRepository interface {
	CreateRun(ctx context.Context, run *domain.AgentRun) error
	GetRun(ctx context.Context, id int64) (*domain.AgentRun, error)
	ListRuns(ctx context.Context, userID, workspaceID int64) ([]domain.AgentRun, error)
	ListEvents(ctx context.Context, runID, afterSequence int64) ([]domain.AgentRunEvent, error)
	AppendEvent(ctx context.Context, runID int64, event domain.ClientEvent) (domain.AgentRunEvent, error)
	UpdateStatus(ctx context.Context, runID int64, status domain.AgentRunStatus, errorMessage string) error
	AddUsage(ctx context.Context, runID int64, usage domain.Usage) error
	MarkActiveRunsFailed(ctx context.Context, message string) error
	ListRunsByStatus(ctx context.Context, userID int64, statuses ...domain.AgentRunStatus) ([]domain.AgentRun, error)
}

// StartAgentRunRequest continues a saved conversation with a new user message.
type StartAgentRunRequest struct {
	UserID         int64
	WorkspaceID    int64
	ConversationID int64
	AgentID        string
	Model          string
	// Seed is the history a conversation with no turns yet starts from,
	// before Message, such as the earlier rounds a task carries into a new
	// conversation. A conversation that already has turns refuses it.
	Seed []domain.Turn
	// Message is the user's turn the run answers: text, images, or both.
	Message  domain.Turn
	Thinking *domain.ThinkingConfig
	// Budget stops the run once spent. Nil means no limit.
	Budget *domain.Budget
}

// StartChildAgentRunRequest starts a sub-agent run from its own prompt. The
// conversation ID only links the run to its parent's conversation for display;
// child runs never write to it. A child shares its parent's budget.
type StartChildAgentRunRequest struct {
	UserID         int64
	WorkspaceID    int64
	ConversationID int64
	AgentID        string
	Model          string
	Prompt         string
	Thinking       *domain.ThinkingConfig
}

// AgentRunService manages browser-independent AI agent execution.
type AgentRunService interface {
	StartRun(ctx context.Context, req StartAgentRunRequest) (*domain.AgentRun, error)
	StartChildRun(ctx context.Context, parentRunID int64, req StartChildAgentRunRequest) (*domain.AgentRun, error)
	GetRun(ctx context.Context, userID, runID int64) (*domain.AgentRun, error)
	ListRuns(ctx context.Context, userID, workspaceID int64) ([]domain.AgentRun, error)
	ListEvents(ctx context.Context, userID, runID, afterSequence int64) ([]domain.AgentRunEvent, error)
	SubscribeEvents(ctx context.Context, userID, runID, afterSequence int64) (<-chan domain.AgentRunEvent, error)
	CancelRun(ctx context.Context, userID, runID int64) error
	// ListAttention returns the user's runs that are waiting for an approval
	// or an answer, across all workspaces.
	ListAttention(ctx context.Context, userID int64) ([]AttentionItem, error)
	Shutdown(ctx context.Context) error
}

// AttentionItem is a run waiting on the user and what it is waiting for.
type AttentionItem struct {
	Run      domain.AgentRun
	Approval *domain.ApprovalRequest
	Question *domain.UserQuestionRequest
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
	images        ImageLoader
	models        ModelFinder
	broker        agentEventBroker

	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         sync.WaitGroup

	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
	// budgets holds the budget of each active run that has one, for the
	// child runs it starts.
	budgets map[int64]*domain.Budget

	// startMu serializes claiming conversations so two requests cannot both
	// find a conversation idle and start runs on it. It covers only storage
	// calls; opening the model stream happens outside it.
	startMu sync.Mutex

	listenersMu sync.Mutex
	listeners   []func(domain.AgentRun)
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
		budgets:       make(map[int64]*domain.Budget),
	}
	service.reconcileInterruptedRuns()
	return service
}

// reconcileInterruptedRuns ends runs a previous server process left active.
// Each keeps the steps it completed, which were saved as they completed, so
// it can be continued; only a step the process stopped in the middle of is
// lost.
func (s *agentRunService) reconcileInterruptedRuns() {
	ctx := context.Background()
	runs, err := s.repo.ListRunsByStatus(ctx, 0, domain.AgentRunQueued, domain.AgentRunRunning, domain.AgentRunWaitingApproval, domain.AgentRunWaitingUser)
	if err != nil {
		log.Printf("failed to list interrupted agent runs: %v", err)
	}
	for _, run := range runs {
		if err := s.interruptRun(run.ID); err != nil {
			log.Printf("failed to reconcile interrupted agent run %d: %v", run.ID, err)
		}
	}
	// Anything the loop could not end is still marked failed.
	if err := s.repo.MarkActiveRunsFailed(ctx, runnerShutdownMessage); err != nil {
		log.Printf("failed to reconcile active agent runs: %v", err)
	}
}

// WithAgentService configures the agent lookup dependency.
func (s *agentRunService) WithAgentService(agents AgentService) *agentRunService {
	s.agents = agents
	return s
}

// WithImageLoader configures the loader that checks and loads the images a
// run's input turns refer to.
func (s *agentRunService) WithImageLoader(images ImageLoader) *agentRunService {
	s.images = images
	return s
}

// WithModels configures the catalog that tells which provider a model is
// from. A run starts sub-agents only with one: a sub-agent runs on a model of
// its parent's provider.
func (s *agentRunService) WithModels(models ModelFinder) *agentRunService {
	s.models = models
	return s
}

// OnRunFinished registers fn to be called with the run's final state each time
// a run reaches a terminal status. fn runs on the run's goroutine and must
// return quickly.
func (s *agentRunService) OnRunFinished(fn func(domain.AgentRun)) {
	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()
	s.listeners = append(s.listeners, fn)
}

func (s *agentRunService) notifyFinished(runID int64) {
	s.listenersMu.Lock()
	listeners := slices.Clone(s.listeners)
	s.listenersMu.Unlock()
	if len(listeners) == 0 {
		return
	}
	run, err := s.repo.GetRun(context.Background(), runID)
	if err != nil || run == nil {
		log.Printf("failed to load finished agent run %d for listeners: %v", runID, err)
		return
	}
	for _, fn := range listeners {
		fn(*run)
	}
}

// StartRun continues a conversation with a new user message. The server is the
// only writer of a conversation: it loads the saved history, appends the
// message once the run has started, and saves each step of the run as it
// completes, so the history never depends on a browser staying connected.
func (s *agentRunService) StartRun(ctx context.Context, req StartAgentRunRequest) (*domain.AgentRun, error) {
	req.Message.Role = domain.RoleUser
	// What the user typed is kept only where the model is sent something else.
	if displayText := strings.TrimSpace(req.Message.DisplayText); displayText == req.Message.Text() {
		req.Message.DisplayText = ""
	} else {
		req.Message.DisplayText = displayText
	}
	if err := validateStartAgentRunRequest(req); err != nil {
		return nil, err
	}
	if s.conversations == nil {
		return nil, fmt.Errorf("conversation storage is not configured")
	}
	agent, err := s.resolveAgent(ctx, req.UserID, req.WorkspaceID, req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("resolving agent: %w", err)
	}
	// A malformed image reference fails the run before anything is saved.
	turns := append(cloneTurns(req.Seed), cloneTurns([]domain.Turn{req.Message})...)
	if s.images != nil {
		if turns, err = s.images.Hydrate(ctx, req.UserID, turns); err != nil {
			return nil, err
		}
	}
	run, recorder, err := s.claimConversation(ctx, req, agent, turns)
	if err != nil {
		return nil, err
	}
	return s.launchRun(ctx, run, agent, recorder, req.Budget)
}

// claimConversation stores a queued run for the conversation if it has no
// active run. The stored run is the claim: until it ends, no other run can
// start on the conversation, so nothing else writes to its history while this
// one starts. newTurns are the turns the run adds before it starts: the seed,
// if any, and the user's message.
func (s *agentRunService) claimConversation(ctx context.Context, req StartAgentRunRequest, agent *domain.Agent, newTurns []domain.Turn) (*domain.AgentRun, *conversationRecorder, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	conv, err := s.conversations.GetConversation(ctx, req.ConversationID, req.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading agent run conversation: %w", err)
	}
	if conv == nil || conv.WorkspaceID != req.WorkspaceID {
		return nil, nil, domain.ErrConversationNotFound
	}
	if err := s.ensureConversationIdle(ctx, req.UserID, req.ConversationID); err != nil {
		return nil, nil, err
	}
	history, repaired, err := s.loadConversationHistory(ctx, req.UserID, req.WorkspaceID, req.ConversationID)
	if err != nil {
		return nil, nil, err
	}
	if len(req.Seed) > 0 && len(history) > 0 {
		return nil, nil, fmt.Errorf("a conversation with turns cannot be seeded")
	}
	// A transcript going on with another model, as when its model was
	// retired, leaves behind what only the old model can read.
	modelChanged := conv.Model != req.Model
	if modelChanged {
		history = domain.WithoutReasoningState(history)
	}

	run := &domain.AgentRun{
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		ConversationID: req.ConversationID,
		AgentID:        agent.ID,
		Model:          req.Model,
		Status:         domain.AgentRunQueued,
		InputTurns:     append(cloneTurns(history), cloneTurns(newTurns)...),
		Thinking:       cloneThinking(req.Thinking),
	}
	if err := s.repo.CreateRun(ctx, run); err != nil {
		return nil, nil, fmt.Errorf("creating agent run: %w", err)
	}
	recorder := &conversationRecorder{
		conversations:  s.conversations,
		conversationID: req.ConversationID,
		userID:         req.UserID,
		newTurns:       newTurns,
	}
	if repaired || modelChanged {
		recorder.history = history
	}
	if modelChanged {
		recorder.model = req.Model
	}
	return run, recorder, nil
}

// StartChildRun starts a sub-agent run for a tool call in a parent run. It
// runs on a model of the parent's provider and shares the parent's budget, if
// the parent has one.
func (s *agentRunService) StartChildRun(ctx context.Context, parentRunID int64, req StartChildAgentRunRequest) (*domain.AgentRun, error) {
	if parentRunID <= 0 {
		return nil, domain.ErrAgentRunNotFound
	}
	if err := validateStartChildAgentRunRequest(req); err != nil {
		return nil, err
	}
	parent, err := s.getOwnedRun(ctx, req.UserID, parentRunID)
	if err != nil {
		return nil, fmt.Errorf("validating parent run: %w", err)
	}
	if parent.WorkspaceID != req.WorkspaceID {
		return nil, fmt.Errorf("parent run workspace mismatch: %w", domain.ErrAgentRunNotFound)
	}
	if err := s.checkChildProvider(parent.Model, req.Model); err != nil {
		return nil, err
	}
	if err := s.ensureChildRunCapacity(ctx, req.UserID, req.WorkspaceID, parentRunID); err != nil {
		return nil, err
	}
	agent, err := s.resolveAgent(ctx, req.UserID, req.WorkspaceID, req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("resolving agent: %w", err)
	}

	run := &domain.AgentRun{
		ParentRunID:    parentRunID,
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		ConversationID: req.ConversationID,
		AgentID:        agent.ID,
		Model:          req.Model,
		Status:         domain.AgentRunQueued,
		InputTurns:     []domain.Turn{domain.NewTextTurn(domain.RoleUser, req.Prompt)},
		Thinking:       cloneThinking(req.Thinking),
	}
	if err := s.repo.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("creating agent run: %w", err)
	}
	return s.launchRun(ctx, run, agent, nil, s.budgetForRun(parentRunID))
}

// checkChildProvider refuses a sub-agent on a model of another provider
// than its parent's.
func (s *agentRunService) checkChildProvider(parentModel, childModel string) error {
	if s.models == nil {
		return errors.New("sub-agents are not available: no model catalog is configured")
	}
	parent, err := s.models.FindModel(parentModel)
	if err != nil {
		return fmt.Errorf("looking up the parent run's model %s: %w", parentModel, err)
	}
	child, err := s.models.FindModel(childModel)
	if err != nil {
		return fmt.Errorf("looking up the sub-agent's model %s: %w", childModel, err)
	}
	if child.ProviderID != parent.ProviderID {
		return fmt.Errorf("%w: %s is a %s model, and this run's model %s is a %s model", domain.ErrSubAgentProvider, child.ID, child.ProviderID, parent.ID, parent.ProviderID)
	}
	return nil
}

// launchRun starts a stored run in the background. A nil recorder means the
// run's output is not saved to a conversation.
func (s *agentRunService) launchRun(ctx context.Context, run *domain.AgentRun, agent *domain.Agent, recorder *conversationRecorder, budget *domain.Budget) (*domain.AgentRun, error) {
	runCtx, cancel := context.WithCancel(s.rootCtx)
	s.registerCancel(run.ID, cancel, budget)

	stream, err := s.chat.StreamAgent(runCtx, AgentChatRequest{
		UserID:         run.UserID,
		WorkspaceID:    run.WorkspaceID,
		CurrentRunID:   run.ID,
		ConversationID: run.ConversationID,
		Model:          run.Model,
		Turns:          cloneTurns(run.InputTurns),
		Thinking:       cloneThinking(run.Thinking),
		AgentPrompt:    agent.Instructions,
		Budget:         budget,
	})
	if err != nil {
		s.abortLaunch(run.ID, cancel, err.Error())
		return nil, err
	}
	// Save the user's message only once the run has started, so a run that
	// cannot start leaves the conversation as it was.
	if err := recorder.start(); err != nil {
		s.abortLaunch(run.ID, cancel, saveFailureMessage(err))
		return nil, fmt.Errorf("saving user message: %w", err)
	}

	s.wg.Add(1)
	go s.consumeRun(runCtx, run.ID, recorder, stream)

	created, err := s.repo.GetRun(ctx, run.ID)
	if err != nil {
		return nil, fmt.Errorf("loading created agent run: %w", err)
	}
	return created, nil
}

// abortLaunch stops a run that could not be started and marks it failed.
func (s *agentRunService) abortLaunch(runID int64, cancel context.CancelFunc, message string) {
	s.unregisterCancel(runID)
	cancel()
	if err := s.failRun(runID, message); err != nil {
		log.Printf("failed to mark agent run %d failed: %v", runID, err)
	}
}

// ensureConversationIdle rejects a second run on a conversation while one is
// still active, since both would append to the same history. Sub-agent runs
// are linked to their parent's conversation but never write to it.
func (s *agentRunService) ensureConversationIdle(ctx context.Context, userID, conversationID int64) error {
	runs, err := s.repo.ListRunsByStatus(ctx, userID, domain.AgentRunQueued, domain.AgentRunRunning, domain.AgentRunWaitingApproval, domain.AgentRunWaitingUser)
	if err != nil {
		return fmt.Errorf("checking active conversation runs: %w", err)
	}
	for _, run := range runs {
		if run.ConversationID == conversationID && run.ParentRunID == 0 {
			return domain.ErrConversationBusy
		}
	}
	return nil
}

// loadConversationHistory returns the saved history, repaired if its tool
// calls and results no longer pair up, and whether it was repaired. A repair
// is saved with the run's first turns, once the run starts.
func (s *agentRunService) loadConversationHistory(ctx context.Context, userID, workspaceID, conversationID int64) ([]domain.Turn, bool, error) {
	turns, err := s.conversations.GetTurns(ctx, conversationID, userID)
	if err != nil {
		return nil, false, fmt.Errorf("loading conversation history: %w", err)
	}
	repaired, changed := repairToolPairing(turns, s.conversationToolCalls(ctx, userID, workspaceID, conversationID))
	if !changed {
		return turns, false, nil
	}
	log.Printf("repairing tool call pairing in conversation %d", conversationID)
	return repaired, true, nil
}

// conversationToolCalls looks up tool calls by ID in the stored events of the
// conversation's runs. The events are only read if a lookup is needed.
func (s *agentRunService) conversationToolCalls(ctx context.Context, userID, workspaceID, conversationID int64) func(id string) (domain.ToolCall, bool) {
	var calls map[string]domain.ToolCall
	return func(id string) (domain.ToolCall, bool) {
		if calls == nil {
			calls = make(map[string]domain.ToolCall)
			runs, err := s.repo.ListRuns(ctx, userID, workspaceID)
			if err != nil {
				log.Printf("listing runs to repair conversation %d: %v", conversationID, err)
			}
			for _, run := range runs {
				if run.ConversationID != conversationID {
					continue
				}
				events, err := s.repo.ListEvents(ctx, run.ID, 0)
				if err != nil {
					log.Printf("listing run %d events to repair conversation %d: %v", run.ID, conversationID, err)
					continue
				}
				for _, event := range events {
					for _, call := range event.Event.ToolCalls {
						calls[call.ID] = cloneTranscriptToolCall(call)
					}
				}
			}
		}
		call, ok := calls[id]
		return call, ok
	}
}

func (s *agentRunService) ensureChildRunCapacity(ctx context.Context, userID, workspaceID, parentRunID int64) error {
	runs, err := s.repo.ListRuns(ctx, userID, workspaceID)
	if err != nil {
		return fmt.Errorf("checking active child agent runs: %w", err)
	}

	active := 0
	for _, run := range runs {
		if run.ParentRunID == parentRunID && !domain.AgentRunStatusTerminal(run.Status) {
			active++
		}
	}
	if active >= maxActiveChildRunsPerParent {
		return fmt.Errorf("parent agent run already has %d active child runs; wait for one to finish before spawning another", active)
	}
	return nil
}

func (s *agentRunService) resolveAgent(ctx context.Context, userID, workspaceID int64, agentID string) (*domain.Agent, error) {
	if s.agents == nil {
		agent := domain.DefaultAgent()
		return &agent, nil
	}
	agent, err := s.agents.GetAgent(ctx, userID, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	return agent, nil
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

	// Subscribe before replaying so no event falls between the two.
	liveEvents, unsubscribe := s.broker.Subscribe(runID)
	out := make(chan domain.AgentRunEvent)
	go func() {
		defer close(out)

		lastSequence := afterSequence
		for {
			sent, ok := s.replayRunEvents(ctx, out, runID, lastSequence)
			if !ok {
				unsubscribe()
				return
			}
			lastSequence = sent

			lagged := false
			for !lagged {
				select {
				case event, open := <-liveEvents:
					if !open {
						// The broker dropped this subscriber for falling behind;
						// resubscribe and catch up from the stored events.
						lagged = true
						continue
					}
					if event.Sequence <= lastSequence {
						continue
					}
					if !sendRunEvent(ctx, out, event) {
						unsubscribe()
						return
					}
					lastSequence = event.Sequence
				case <-ctx.Done():
					unsubscribe()
					return
				}
			}
			unsubscribe()
			liveEvents, unsubscribe = s.broker.Subscribe(runID)
		}
	}()

	return out, nil
}

// replayRunEvents sends the stored events after afterSequence and returns the
// last sequence sent. It reports false if the subscriber should stop.
func (s *agentRunService) replayRunEvents(ctx context.Context, out chan<- domain.AgentRunEvent, runID, afterSequence int64) (int64, bool) {
	events, err := s.repo.ListEvents(ctx, runID, afterSequence)
	if err != nil {
		log.Printf("failed to replay agent run %d events: %v", runID, err)
		return afterSequence, false
	}
	lastSequence := afterSequence
	for _, event := range events {
		if event.Sequence <= lastSequence {
			continue
		}
		if !sendRunEvent(ctx, out, event) {
			return lastSequence, false
		}
		lastSequence = event.Sequence
	}
	return lastSequence, true
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

func (s *agentRunService) consumeRun(ctx context.Context, runID int64, recorder *conversationRecorder, stream <-chan domain.ClientEvent) {
	defer s.wg.Done()
	defer s.unregisterCancel(runID)

	if err := s.repo.UpdateStatus(context.Background(), runID, domain.AgentRunRunning, ""); err != nil {
		// A run left queued keeps its conversation busy until the server
		// restarts, and nothing would read or stop its stream, so end it.
		if cancel, ok := s.cancelForRun(runID); ok {
			cancel()
		}
		message := fmt.Sprintf("marking agent run running: %v", err)
		log.Printf("agent run %d: %s", runID, message)
		if err := s.failRun(runID, message); err != nil {
			log.Printf("failed to mark agent run %d failed: %v", runID, err)
		}
		return
	}

	status := domain.AgentRunRunning
	errorMessage := ""
	for {
		select {
		case event, ok := <-stream:
			if !ok {
				// The orchestrator closes the stream when the run's context is
				// cancelled, so a closed stream is not proof of completion.
				if ctx.Err() != nil {
					s.finishRecording(runID, recorder)
					s.endCancelledRun(runID, ctx.Err())
					return
				}
				if err := recorder.finish(); err != nil {
					errorMessage = s.reportSaveFailure(runID, err)
				}
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
			if event.Question != nil && status != domain.AgentRunWaitingUser {
				if err := s.repo.UpdateStatus(context.Background(), runID, domain.AgentRunWaitingUser, ""); err != nil {
					log.Printf("failed to mark agent run %d waiting for user: %v", runID, err)
				} else {
					status = domain.AgentRunWaitingUser
				}
			}
			if event.ApprovalResult != nil && status == domain.AgentRunWaitingApproval && !event.Done {
				if err := s.repo.UpdateStatus(context.Background(), runID, domain.AgentRunRunning, ""); err != nil {
					log.Printf("failed to mark agent run %d running after approval: %v", runID, err)
				} else {
					status = domain.AgentRunRunning
				}
			}
			if event.QuestionResult != nil && status == domain.AgentRunWaitingUser && !event.Done {
				if err := s.repo.UpdateStatus(context.Background(), runID, domain.AgentRunRunning, ""); err != nil {
					log.Printf("failed to mark agent run %d running after user answer: %v", runID, err)
				} else {
					status = domain.AgentRunRunning
				}
			}
			if event.ErrorMessage != "" {
				errorMessage = event.ErrorMessage
			}
			if event.Usage != nil {
				// Losing a request's usage undercounts the run but must not
				// stop it.
				if err := s.repo.AddUsage(context.Background(), runID, *event.Usage); err != nil {
					log.Printf("failed to record agent run %d usage: %v", runID, err)
				}
			}
			if event.Done {
				// The history is complete before the run is seen to end, so
				// whatever acts on the end reads the whole of it.
				err := recorder.record(event)
				if err == nil {
					err = recorder.finish()
				}
				if err != nil {
					errorMessage = s.reportSaveFailure(runID, err)
				}
			}
			stored, err := s.repo.AppendEvent(context.Background(), runID, event)
			if err != nil {
				log.Printf("failed to persist agent run %d event: %v", runID, err)
				errorMessage = err.Error()
				s.finishRecording(runID, recorder)
				s.finishRun(runID, domain.AgentRunFailed, errorMessage)
				return
			}
			if event.Done {
				// Set the final status before viewers see the run end, so one
				// that acts on it right away finds the run finished.
				s.finishRun(runID, status, errorMessage)
				s.broker.Publish(stored)
				return
			}
			s.broker.Publish(stored)
			if err := recorder.record(event); err != nil {
				// A step that cannot be saved would leave a gap in the
				// history, so stop the run rather than go on without it.
				if cancel, ok := s.cancelForRun(runID); ok {
					cancel()
				}
				message := saveFailureMessage(err)
				log.Printf("agent run %d: %s", runID, message)
				if err := s.failRun(runID, message); err != nil {
					log.Printf("failed to mark agent run %d failed: %v", runID, err)
				}
				return
			}
		case <-ctx.Done():
			s.finishRecording(runID, recorder)
			s.endCancelledRun(runID, ctx.Err())
			return
		}
	}
}

// endCancelledRun records why a run's context ended: a cancel from the
// user, the server shutting down, or a deadline.
func (s *agentRunService) endCancelledRun(runID int64, cause error) {
	switch {
	case s.rootCtx.Err() != nil:
		if err := s.interruptRun(runID); err != nil {
			log.Printf("failed to mark agent run %d interrupted: %v", runID, err)
		}
	case errors.Is(cause, context.Canceled):
		if err := s.cancelRun(runID, "agent run cancelled"); err != nil {
			log.Printf("failed to mark agent run %d cancelled: %v", runID, err)
		}
	default:
		s.finishRun(runID, domain.AgentRunFailed, cause.Error())
	}
}

// interruptRun ends a run cut short by a server shutdown or restart. It
// fails with domain.AgentRunInterruptedError, so its owner can tell it apart
// from a real failure and continue it, and it keeps its transcript.
func (s *agentRunService) interruptRun(runID int64) error {
	return s.endRun(runID, domain.AgentRunFailed, runnerShutdownMessage)
}

func (s *agentRunService) ListAttention(ctx context.Context, userID int64) ([]AttentionItem, error) {
	runs, err := s.repo.ListRunsByStatus(ctx, userID, domain.AgentRunWaitingApproval, domain.AgentRunWaitingUser)
	if err != nil {
		return nil, fmt.Errorf("listing waiting agent runs: %w", err)
	}
	items := make([]AttentionItem, 0, len(runs))
	for _, run := range runs {
		events, err := s.repo.ListEvents(ctx, run.ID, 0)
		if err != nil {
			return nil, fmt.Errorf("listing agent run events: %w", err)
		}
		item := AttentionItem{Run: run}
		item.Approval, item.Question = pendingPrompts(events)
		if item.Approval != nil || item.Question != nil {
			items = append(items, item)
		}
	}
	return items, nil
}

// pendingPrompts finds the approval and question a run is still waiting on.
func pendingPrompts(events []domain.AgentRunEvent) (*domain.ApprovalRequest, *domain.UserQuestionRequest) {
	var approval *domain.ApprovalRequest
	var question *domain.UserQuestionRequest
	for _, stored := range events {
		e := stored.Event
		switch {
		case e.Approval != nil:
			approval = e.Approval
		case e.ApprovalResult != nil && approval != nil && e.ApprovalResult.ID == approval.ID:
			approval = nil
		case e.Question != nil:
			question = e.Question
		case e.QuestionResult != nil && question != nil && e.QuestionResult.ID == question.ID:
			question = nil
		}
	}
	return approval, question
}

func (s *agentRunService) finishRun(runID int64, currentStatus domain.AgentRunStatus, errorMessage string) {
	status := domain.AgentRunCompleted
	if errorMessage != "" || currentStatus == domain.AgentRunFailed {
		status = domain.AgentRunFailed
	}
	if err := s.repo.UpdateStatus(context.Background(), runID, status, errorMessage); err != nil {
		log.Printf("failed to finish agent run %d: %v", runID, err)
		return
	}
	s.notifyFinished(runID)
}

// finishRecording saves what a run that is ending early did since its last
// completed step. The run is already ending, so a failure is only logged.
func (s *agentRunService) finishRecording(runID int64, recorder *conversationRecorder) {
	if err := recorder.finish(); err != nil {
		log.Printf("agent run %d: %s", runID, saveFailureMessage(err))
	}
}

// reportSaveFailure tells the run's viewers that its last step could not be
// saved and returns the message to fail the run with.
func (s *agentRunService) reportSaveFailure(runID int64, err error) string {
	message := saveFailureMessage(err)
	log.Printf("agent run %d: %s", runID, message)
	if err := s.appendAndPublish(runID, domain.ClientEvent{ErrorMessage: message}); err != nil {
		log.Printf("failed to persist agent run %d event: %v", runID, err)
	}
	return message
}

func saveFailureMessage(err error) string {
	return fmt.Sprintf("saving conversation history: %v", err)
}

func (s *agentRunService) failRun(runID int64, message string) error {
	return s.endRun(runID, domain.AgentRunFailed, message)
}

func (s *agentRunService) cancelRun(runID int64, message string) error {
	return s.endRun(runID, domain.AgentRunCancelled, message)
}

// endRun tells the run's viewers why it ended and sets its final status. The
// status is set even if the events cannot be saved, since a run left active
// stays listed as active, keeps its conversation from taking another run and,
// as a child run, counts against its parent's limit until the server
// restarts. Like a run that finishes, the status is set before viewers see the
// done event.
func (s *agentRunService) endRun(runID int64, status domain.AgentRunStatus, message string) error {
	var eventErr error
	var done *domain.AgentRunEvent
	if err := s.appendAndPublish(runID, domain.ClientEvent{ErrorMessage: message}); err != nil {
		eventErr = fmt.Errorf("recording end of run: %w", err)
	} else if stored, err := s.repo.AppendEvent(context.Background(), runID, domain.ClientEvent{Done: true}); err != nil {
		eventErr = fmt.Errorf("recording end of run: %w", err)
	} else {
		done = &stored
	}
	statusErr := s.repo.UpdateStatus(context.Background(), runID, status, message)
	if done != nil {
		s.broker.Publish(*done)
	}
	if statusErr != nil {
		return errors.Join(eventErr, statusErr)
	}
	s.notifyFinished(runID)
	return eventErr
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

func (s *agentRunService) registerCancel(runID int64, cancel context.CancelFunc, budget *domain.Budget) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels[runID] = cancel
	if budget != nil {
		s.budgets[runID] = budget
	}
}

func (s *agentRunService) unregisterCancel(runID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, runID)
	delete(s.budgets, runID)
}

// BudgetForRun returns an active run's budget, or nil when it has none or
// is not running, for what a run's tool calls spend on its behalf.
func (s *agentRunService) BudgetForRun(runID int64) *domain.Budget {
	return s.budgetForRun(runID)
}

// budgetForRun returns an active run's budget, or nil when it has none.
func (s *agentRunService) budgetForRun(runID int64) *domain.Budget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budgets[runID]
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
	case req.ConversationID <= 0:
		return fmt.Errorf("conversation ID is required")
	case req.Model == "":
		return fmt.Errorf("model is required")
	case strings.TrimSpace(req.Message.Text()) == "" && len(req.Message.Images()) == 0:
		return fmt.Errorf("message is required")
	default:
		return nil
	}
}

func validateStartChildAgentRunRequest(req StartChildAgentRunRequest) error {
	switch {
	case req.UserID <= 0:
		return fmt.Errorf("user ID is required")
	case req.WorkspaceID <= 0:
		return fmt.Errorf("workspace ID is required")
	case req.Model == "":
		return fmt.Errorf("model is required")
	case strings.TrimSpace(req.Prompt) == "":
		return fmt.Errorf("prompt is required")
	default:
		return nil
	}
}

// conversationRecorder writes a run's turns to its conversation: the user's
// message once the run starts, then each step as it completes, so a run that
// fails, is cancelled, or outlives its browser still leaves a complete, valid
// history behind. A nil recorder records nothing.
type conversationRecorder struct {
	conversations  ConversationService
	conversationID int64
	userID         int64
	// newTurns are the seed, if any, and the user's message.
	newTurns []domain.Turn
	// history, when set, replaces the saved history when the run starts: it
	// was repaired, or stripped of what another model left.
	history []domain.Turn
	// model, when set, is the model the conversation goes on with.
	model      string
	transcript runTranscript
}

// start saves the turns that begin the run.
func (r *conversationRecorder) start() error {
	if r == nil {
		return nil
	}
	// Not the run's context: a cancelled run still saves what it did.
	ctx := context.Background()
	if r.history != nil {
		turns := append(cloneTurns(r.history), r.newTurns...)
		if err := r.conversations.ReplaceTurns(ctx, r.conversationID, r.userID, turns); err != nil {
			return err
		}
	} else if err := r.conversations.AppendTurns(ctx, r.conversationID, r.userID, r.newTurns); err != nil {
		return err
	}
	// Recorded after the turns are saved, so a failure in between can only
	// mean stripping them again.
	if r.model != "" {
		if err := r.conversations.SetConversationModel(ctx, r.conversationID, r.userID, r.model); err != nil {
			return fmt.Errorf("recording the conversation's model: %w", err)
		}
	}
	return nil
}

func (r *conversationRecorder) record(event domain.ClientEvent) error {
	if r == nil {
		return nil
	}
	return r.append(r.transcript.add(event))
}

// finish saves whatever the run produced after its last completed step.
func (r *conversationRecorder) finish() error {
	if r == nil {
		return nil
	}
	return r.append(r.transcript.flush())
}

func (r *conversationRecorder) append(turns []domain.Turn) error {
	if len(turns) == 0 {
		return nil
	}
	return r.conversations.AppendTurns(context.Background(), r.conversationID, r.userID, turns)
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
	clone.Effort = thinking.Effort
	return clone
}

func cloneTurns(turns []domain.Turn) []domain.Turn {
	if turns == nil {
		return nil
	}
	cloned := make([]domain.Turn, 0, len(turns))
	for _, turn := range turns {
		clonedTurn := domain.Turn{Role: turn.Role, DisplayText: turn.DisplayText}
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
		toolResult.Images = domain.CloneImages(toolResult.Images)
		cloned.ToolResult = &toolResult
	}
	if part.Image != nil {
		image := *part.Image
		cloned.Image = &image
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
	// Sends never block, so holding the lock is cheap, and it keeps a channel
	// from being closed while another publish is sending on it.
	b.mu.Lock()
	defer b.mu.Unlock()

	subscribers := b.subscribers[event.RunID]
	for ch := range subscribers {
		select {
		case ch <- event:
		default:
			// The subscriber fell behind. Disconnect it instead of silently
			// skipping the event; it catches up from the stored events.
			delete(subscribers, ch)
			close(ch)
		}
	}
	if len(subscribers) == 0 {
		delete(b.subscribers, event.RunID)
	}
}

var _ AgentRunService = (*agentRunService)(nil)
