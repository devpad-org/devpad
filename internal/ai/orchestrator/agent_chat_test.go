package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
)

type mockChatService struct {
	findModelFn  func(modelID string) (domain.Model, error)
	chatStreamFn func(ctx context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error)
}

func (m *mockChatService) FindModel(modelID string) (domain.Model, error) {
	if m.findModelFn != nil {
		return m.findModelFn(modelID)
	}
	return domain.Model{ID: modelID}, nil
}

func (m *mockChatService) ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
	if m.chatStreamFn != nil {
		return m.chatStreamFn(ctx, req)
	}
	ch := make(chan domain.ProviderEvent, 1)
	ch <- domain.ProviderEvent{Done: true}
	close(ch)
	return ch, nil
}

type mockToolExecutor struct {
	executeToolFn func(ctx context.Context, req aitools.ExecutionRequest) domain.ToolResultPart
}

func (m *mockToolExecutor) ExecuteTool(ctx context.Context, req aitools.ExecutionRequest) domain.ToolResultPart {
	if m.executeToolFn != nil {
		return m.executeToolFn(ctx, req)
	}
	return domain.ToolResultPart{Name: req.ToolName, Content: "ok"}
}

type mockApprovalBroker struct {
	openFn    func(ctx context.Context, userID int64, command string) (domain.ApprovalRequest, error)
	awaitFn   func(ctx context.Context, approvalID string) (bool, error)
	resolveFn func(ctx context.Context, userID int64, approvalID string, approved bool) error
}

func (m *mockApprovalBroker) Open(ctx context.Context, userID int64, command string) (domain.ApprovalRequest, error) {
	if m.openFn != nil {
		return m.openFn(ctx, userID, command)
	}
	return domain.ApprovalRequest{ID: "approval-1", Command: command}, nil
}

func (m *mockApprovalBroker) Await(ctx context.Context, approvalID string) (bool, error) {
	if m.awaitFn != nil {
		return m.awaitFn(ctx, approvalID)
	}
	<-ctx.Done()
	return false, ctx.Err()
}

func (m *mockApprovalBroker) Resolve(ctx context.Context, userID int64, approvalID string, approved bool) error {
	if m.resolveFn != nil {
		return m.resolveFn(ctx, userID, approvalID, approved)
	}
	return nil
}

type mockQuestionBroker struct {
	openFn    func(ctx context.Context, userID int64, request domain.UserQuestionRequest) (domain.UserQuestionRequest, error)
	awaitFn   func(ctx context.Context, questionID string) ([]domain.UserQuestionAnswer, error)
	resolveFn func(ctx context.Context, userID int64, questionID string, answers []domain.UserQuestionAnswer) error
}

func (m *mockQuestionBroker) Open(ctx context.Context, userID int64, request domain.UserQuestionRequest) (domain.UserQuestionRequest, error) {
	if m.openFn != nil {
		return m.openFn(ctx, userID, request)
	}
	request.ID = "question-1"
	return request, nil
}

func (m *mockQuestionBroker) Await(ctx context.Context, questionID string) ([]domain.UserQuestionAnswer, error) {
	if m.awaitFn != nil {
		return m.awaitFn(ctx, questionID)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *mockQuestionBroker) Resolve(ctx context.Context, userID int64, questionID string, answers []domain.UserQuestionAnswer) error {
	if m.resolveFn != nil {
		return m.resolveFn(ctx, userID, questionID, answers)
	}
	return nil
}

type fakeToolCatalog struct{}

func (fakeToolCatalog) SystemPrompt() string { return "system" }

func (fakeToolCatalog) Definitions() []domain.ToolDefinition { return nil }

func TestAgentChatOrchestrator_IncludesWorkspaceInstructionsBeforeSelectedAgent(t *testing.T) {
	var captured domain.ChatRequest
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			chatStreamFn: func(_ context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				captured = req
				ch := make(chan domain.ProviderEvent, 1)
				ch <- domain.ProviderEvent{Done: true}
				close(ch)
				return ch, nil
			},
		},
		toolCatalog:       fakeToolCatalog{},
		toolExecutor:      &mockToolExecutor{},
		approvals:         &mockApprovalBroker{},
		questions:         &mockQuestionBroker{},
		maxToolIterations: defaultMaxToolIterations,
	}

	stream, err := orch.Stream(context.Background(), AgentChatRequest{
		UserID:                1,
		WorkspaceID:           1,
		Model:                 "test-model",
		Turns:                 []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
		WorkspaceInstructions: "Use make test.",
		AgentPrompt:           "Be concise.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	collectEvents(t, stream)

	if len(captured.Turns) == 0 || captured.Turns[0].Role != domain.RoleSystem {
		t.Fatalf("expected first provider turn to be system prompt, got %+v", captured.Turns)
	}
	systemPrompt := captured.Turns[0].Text()
	workspaceIdx := strings.Index(systemPrompt, "Workspace instructions from AGENTS.md:\nUse make test.")
	agentIdx := strings.Index(systemPrompt, "Selected agent instructions:\nBe concise.")
	if workspaceIdx == -1 || agentIdx == -1 {
		t.Fatalf("expected workspace and selected agent instructions in system prompt, got %q", systemPrompt)
	}
	if workspaceIdx > agentIdx {
		t.Fatalf("expected workspace instructions before selected agent instructions, got %q", systemPrompt)
	}
}

// scopedToolCatalog offers an extra tool in one workspace.
type scopedToolCatalog struct{ fakeToolCatalog }

func (scopedToolCatalog) DefinitionsFor(_ context.Context, _, workspaceID int64) []domain.ToolDefinition {
	if workspaceID != 7 {
		return nil
	}
	return []domain.ToolDefinition{{Type: "function", Function: domain.ToolFunction{Name: "submit_review"}}}
}

func TestAgentChatOrchestrator_UsesWorkspaceScopedTools(t *testing.T) {
	for _, tt := range []struct {
		workspaceID int64
		want        int
	}{{7, 1}, {8, 0}} {
		var captured domain.ChatRequest
		orch := &agentChatOrchestrator{
			service: &mockChatService{
				chatStreamFn: func(_ context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
					captured = req
					ch := make(chan domain.ProviderEvent, 1)
					ch <- domain.ProviderEvent{Done: true}
					close(ch)
					return ch, nil
				},
			},
			toolCatalog:       scopedToolCatalog{},
			toolExecutor:      &mockToolExecutor{},
			approvals:         &mockApprovalBroker{},
			questions:         &mockQuestionBroker{},
			maxToolIterations: defaultMaxToolIterations,
		}
		stream, err := orch.Stream(context.Background(), AgentChatRequest{
			UserID: 1, WorkspaceID: tt.workspaceID, Model: "test-model",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		collectEvents(t, stream)
		if len(captured.Tools) != tt.want {
			t.Fatalf("workspace %d: expected %d tools, got %+v", tt.workspaceID, tt.want, captured.Tools)
		}
	}
}

func collectEvents(t *testing.T, stream <-chan domain.ClientEvent) []domain.ClientEvent {
	t.Helper()

	var events []domain.ClientEvent
	for {
		select {
		case event, ok := <-stream:
			if !ok {
				return events
			}
			events = append(events, event)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for stream events")
		}
	}
}

func nextEvent(t *testing.T, stream <-chan domain.ClientEvent) domain.ClientEvent {
	t.Helper()

	for {
		select {
		case event, ok := <-stream:
			if !ok {
				t.Fatal("stream closed unexpectedly")
			}
			if event.ContextSize != nil {
				continue
			}
			return event
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for stream event")
			return domain.ClientEvent{}
		}
	}
}

func TestAgentChatOrchestrator_EmitsContextTelemetryBeforeProviderRequest(t *testing.T) {
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			findModelFn: func(modelID string) (domain.Model, error) {
				return domain.Model{ID: modelID, ProviderID: "anthropic"}, nil
			},
			chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				ch := make(chan domain.ProviderEvent, 1)
				ch <- domain.ProviderEvent{TextDelta: "done"}
				close(ch)
				return ch, nil
			},
		},
		toolCatalog:       fakeToolCatalog{},
		toolExecutor:      &mockToolExecutor{},
		approvals:         &mockApprovalBroker{},
		questions:         &mockQuestionBroker{},
		maxToolIterations: defaultMaxToolIterations,
	}

	stream, err := orch.Stream(context.Background(), AgentChatRequest{
		UserID:      1,
		WorkspaceID: 1,
		Model:       "claude-haiku-4-5",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, strings.Repeat("x", 120))},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := collectEvents(t, stream)
	if len(events) == 0 || events[0].ContextSize == nil {
		t.Fatalf("expected first event to contain context telemetry, got %+v", events)
	}
	telemetry := events[0].ContextSize
	if !telemetry.Approximate {
		t.Fatal("expected telemetry to be marked approximate")
	}
	if telemetry.ProviderID != "anthropic" || telemetry.Model != "claude-haiku-4-5" {
		t.Fatalf("unexpected provider/model telemetry: %+v", telemetry)
	}
	if telemetry.NextRequestTokens <= 0 || telemetry.TotalTranscriptTokens != telemetry.NextRequestTokens {
		t.Fatalf("unexpected token estimates: %+v", telemetry)
	}
	if telemetry.ProviderFacingTokens != telemetry.NextRequestTokens {
		t.Fatalf("expected uncompacted provider-facing tokens to match next request estimate: %+v", telemetry)
	}
	if telemetry.InputBudgetTokens != 200000 || telemetry.PercentageUsed <= 0 {
		t.Fatalf("unexpected budget telemetry: %+v", telemetry)
	}
}

func TestAgentChatOrchestrator_MultiToolCallOrdering(t *testing.T) {
	callCount := 0
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				ch := make(chan domain.ProviderEvent, 2)
				callCount++
				if callCount == 1 {
					ch <- domain.ProviderEvent{ToolCalls: []domain.ToolCall{
						{ID: "tc1", Type: "function", Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.txt"}`}},
						{ID: "tc2", Type: "function", Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"b.txt"}`}},
					}}
					ch <- domain.ProviderEvent{Done: true}
				} else {
					ch <- domain.ProviderEvent{TextDelta: "done"}
					ch <- domain.ProviderEvent{Done: true}
				}
				close(ch)
				return ch, nil
			},
		},
		toolCatalog: fakeToolCatalog{},
		toolExecutor: &mockToolExecutor{executeToolFn: func(_ context.Context, req aitools.ExecutionRequest) domain.ToolResultPart {
			return domain.ToolResultPart{Name: req.ToolName, Content: "result-" + req.ToolName}
		}},
		approvals:         &mockApprovalBroker{awaitFn: func(context.Context, string) (bool, error) { return true, nil }},
		questions:         &mockQuestionBroker{},
		maxToolIterations: defaultMaxToolIterations,
	}

	stream, err := orch.Stream(context.Background(), AgentChatRequest{
		UserID:      1,
		WorkspaceID: 1,
		Model:       "test-model",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := collectEvents(t, stream)

	var toolCallEvents []domain.ClientEvent
	var toolResultEvents []domain.ClientEvent
	doneIdx := -1
	for i, event := range events {
		if len(event.ToolCalls) > 0 {
			toolCallEvents = append(toolCallEvents, event)
		}
		if event.ToolResult != nil {
			toolResultEvents = append(toolResultEvents, event)
		}
		if event.Done {
			doneIdx = i
		}
	}

	if len(toolCallEvents) != 1 {
		t.Fatalf("expected 1 ToolCalls event, got %d", len(toolCallEvents))
	}
	if len(toolCallEvents[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls in one ToolCalls event, got %d", len(toolCallEvents[0].ToolCalls))
	}
	if len(toolResultEvents) != 2 {
		t.Fatalf("expected 2 ToolResult events, got %d", len(toolResultEvents))
	}

	toolCallIdx := -1
	firstResultIdx := -1
	for i, event := range events {
		if toolCallIdx == -1 && len(event.ToolCalls) > 0 {
			toolCallIdx = i
		}
		if firstResultIdx == -1 && event.ToolResult != nil {
			firstResultIdx = i
		}
	}
	if toolCallIdx == -1 || firstResultIdx == -1 {
		t.Fatal("missing ToolCalls or ToolResult events")
	}
	if toolCallIdx >= firstResultIdx {
		t.Fatalf("ToolCalls event index %d must be before first ToolResult index %d", toolCallIdx, firstResultIdx)
	}
	if doneIdx != len(events)-1 {
		t.Fatalf("Done event should be last, got index %d of %d events", doneIdx, len(events))
	}
}

func TestAgentChatOrchestrator_ApprovalWaitAndDenial(t *testing.T) {
	decisionCh := make(chan bool, 1)
	callCount := 0
	toolCalls := 0
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				ch := make(chan domain.ProviderEvent, 2)
				callCount++
				if callCount == 1 {
					ch <- domain.ProviderEvent{ToolCalls: []domain.ToolCall{{
						ID:   "tc1",
						Type: "function",
						Function: domain.ToolCallFunction{
							Name:      "run_command",
							Arguments: `{"command":"sudo apt update"}`,
						},
					}}}
					ch <- domain.ProviderEvent{Done: true}
				} else {
					ch <- domain.ProviderEvent{TextDelta: "done"}
					ch <- domain.ProviderEvent{Done: true}
				}
				close(ch)
				return ch, nil
			},
		},
		toolCatalog: fakeToolCatalog{},
		toolExecutor: &mockToolExecutor{executeToolFn: func(_ context.Context, _ aitools.ExecutionRequest) domain.ToolResultPart {
			toolCalls++
			return domain.ToolResultPart{Name: "run_command", Content: "should-not-run"}
		}},
		approvals: &mockApprovalBroker{
			openFn: func(_ context.Context, userID int64, command string) (domain.ApprovalRequest, error) {
				if userID != 1 {
					t.Fatalf("expected user id 1, got %d", userID)
				}
				if command != "sudo apt update" {
					t.Fatalf("expected command sudo apt update, got %q", command)
				}
				return domain.ApprovalRequest{ID: "approval-1", Command: command}, nil
			},
			awaitFn: func(_ context.Context, approvalID string) (bool, error) {
				if approvalID != "approval-1" {
					t.Fatalf("expected approval-1, got %q", approvalID)
				}
				return <-decisionCh, nil
			},
		},
		maxToolIterations: defaultMaxToolIterations,
		questions:         &mockQuestionBroker{},
	}

	stream, err := orch.Stream(context.Background(), AgentChatRequest{
		UserID:      1,
		WorkspaceID: 1,
		Model:       "test-model",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolCallEvent := nextEvent(t, stream)
	if len(toolCallEvent.ToolCalls) != 1 {
		t.Fatalf("expected one ToolCalls event, got %+v", toolCallEvent)
	}

	approvalEvent := nextEvent(t, stream)
	if approvalEvent.Approval == nil || approvalEvent.Approval.Command != "sudo apt update" {
		t.Fatalf("expected approval request for sudo command, got %+v", approvalEvent)
	}

	decisionCh <- false

	approvalResultEvent := nextEvent(t, stream)
	if approvalResultEvent.ApprovalResult == nil || approvalResultEvent.ApprovalResult.Status != "denied" {
		t.Fatalf("expected denied approval result, got %+v", approvalResultEvent)
	}

	toolResultEvent := nextEvent(t, stream)
	if toolResultEvent.ToolResult == nil {
		t.Fatalf("expected ToolResult after denial, got %+v", toolResultEvent)
	}
	if !strings.Contains(toolResultEvent.ToolResult.Content, "Command denied by user") {
		t.Fatalf("expected denial result, got %q", toolResultEvent.ToolResult.Content)
	}
	if toolCalls != 0 {
		t.Fatalf("expected tool executor not to run, got %d calls", toolCalls)
	}

	contentEvent := nextEvent(t, stream)
	if contentEvent.TextDelta != "done" {
		t.Fatalf("expected follow-up assistant content, got %+v", contentEvent)
	}

	doneEvent := nextEvent(t, stream)
	if !doneEvent.Done {
		t.Fatalf("expected done event, got %+v", doneEvent)
	}
}

func TestAgentChatOrchestrator_AskUserWaitsAndReturnsAnswers(t *testing.T) {
	answersCh := make(chan []domain.UserQuestionAnswer, 1)
	callCount := 0
	toolCalls := 0
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				ch := make(chan domain.ProviderEvent, 2)
				callCount++
				if callCount == 1 {
					ch <- domain.ProviderEvent{ToolCalls: []domain.ToolCall{{
						ID:   "tc-question",
						Type: "function",
						Function: domain.ToolCallFunction{
							Name:      "ask_user",
							Arguments: `{"title":"Choose stack","questions":[{"id":"stack","prompt":"Which stack should I use?","type":"single_choice","options":[{"value":"go","label":"Go"},{"value":"node","label":"Node.js"}]}]}`,
						},
					}}}
					ch <- domain.ProviderEvent{Done: true}
				} else {
					ch <- domain.ProviderEvent{TextDelta: "I'll use Go."}
					ch <- domain.ProviderEvent{Done: true}
				}
				close(ch)
				return ch, nil
			},
		},
		toolCatalog: fakeToolCatalog{},
		toolExecutor: &mockToolExecutor{executeToolFn: func(_ context.Context, _ aitools.ExecutionRequest) domain.ToolResultPart {
			toolCalls++
			return domain.ToolResultPart{Name: "ask_user", Content: "should-not-run"}
		}},
		approvals: &mockApprovalBroker{},
		questions: &mockQuestionBroker{
			openFn: func(_ context.Context, userID int64, request domain.UserQuestionRequest) (domain.UserQuestionRequest, error) {
				if userID != 1 {
					t.Fatalf("expected user id 1, got %d", userID)
				}
				if request.Title != "Choose stack" || len(request.Questions) != 1 {
					t.Fatalf("unexpected question request: %+v", request)
				}
				request.ID = "question-1"
				return request, nil
			},
			awaitFn: func(_ context.Context, questionID string) ([]domain.UserQuestionAnswer, error) {
				if questionID != "question-1" {
					t.Fatalf("expected question-1, got %q", questionID)
				}
				return <-answersCh, nil
			},
		},
		maxToolIterations: defaultMaxToolIterations,
	}

	stream, err := orch.Stream(context.Background(), AgentChatRequest{
		UserID:      1,
		WorkspaceID: 1,
		Model:       "test-model",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolCallEvent := nextEvent(t, stream)
	if len(toolCallEvent.ToolCalls) != 1 || toolCallEvent.ToolCalls[0].Function.Name != "ask_user" {
		t.Fatalf("expected ask_user ToolCalls event, got %+v", toolCallEvent)
	}

	questionEvent := nextEvent(t, stream)
	if questionEvent.Question == nil || questionEvent.Question.ID != "question-1" {
		t.Fatalf("expected question request, got %+v", questionEvent)
	}

	answersCh <- []domain.UserQuestionAnswer{{QuestionID: "stack", Values: []string{"go"}}}

	questionResultEvent := nextEvent(t, stream)
	if questionResultEvent.QuestionResult == nil || questionResultEvent.QuestionResult.Status != "answered" {
		t.Fatalf("expected answered question result, got %+v", questionResultEvent)
	}

	toolResultEvent := nextEvent(t, stream)
	if toolResultEvent.ToolResult == nil || toolResultEvent.ToolResult.Name != "ask_user" {
		t.Fatalf("expected ask_user tool result, got %+v", toolResultEvent)
	}
	var result domain.UserQuestionResult
	if err := json.Unmarshal([]byte(toolResultEvent.ToolResult.Content), &result); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if result.ID != "question-1" || len(result.Answers) != 1 || result.Answers[0].Values[0] != "go" {
		t.Fatalf("unexpected tool result content: %+v", result)
	}
	if toolCalls != 0 {
		t.Fatalf("expected workspace executor not to run, got %d calls", toolCalls)
	}

	contentEvent := nextEvent(t, stream)
	if contentEvent.TextDelta != "I'll use Go." {
		t.Fatalf("expected follow-up content, got %+v", contentEvent)
	}
}

func TestAgentChatOrchestrator_CancellationStopsToolExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	toolStarted := make(chan struct{}, 1)
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				ch := make(chan domain.ProviderEvent, 2)
				ch <- domain.ProviderEvent{ToolCalls: []domain.ToolCall{{
					ID:       "tc1",
					Type:     "function",
					Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.txt"}`},
				}}}
				ch <- domain.ProviderEvent{Done: true}
				close(ch)
				return ch, nil
			},
		},
		toolCatalog: fakeToolCatalog{},
		toolExecutor: &mockToolExecutor{executeToolFn: func(ctx context.Context, _ aitools.ExecutionRequest) domain.ToolResultPart {
			toolStarted <- struct{}{}
			<-ctx.Done()
			return domain.ToolResultPart{Name: "read_file", Content: ctx.Err().Error(), IsError: true}
		}},
		approvals:         &mockApprovalBroker{},
		questions:         &mockQuestionBroker{},
		maxToolIterations: defaultMaxToolIterations,
	}

	stream, err := orch.Stream(ctx, AgentChatRequest{
		UserID:      1,
		WorkspaceID: 1,
		Model:       "test-model",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolCallEvent := nextEvent(t, stream)
	if len(toolCallEvent.ToolCalls) != 1 {
		t.Fatalf("expected a ToolCalls event, got %+v", toolCallEvent)
	}

	select {
	case <-toolStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("tool executor did not start")
	}

	cancel()

	events := collectEvents(t, stream)
	for _, event := range events {
		if event.Done {
			t.Fatalf("did not expect a done event after cancellation: %+v", events)
		}
	}
}

func TestAgentChatOrchestrator_MaxIterationsEmitsWarning(t *testing.T) {
	callCount := 0
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				callCount++
				ch := make(chan domain.ProviderEvent, 2)
				ch <- domain.ProviderEvent{ToolCalls: []domain.ToolCall{{
					ID:       fmt.Sprintf("tc-%d", callCount),
					Type:     "function",
					Function: domain.ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.txt"}`},
				}}}
				ch <- domain.ProviderEvent{Done: true}
				close(ch)
				return ch, nil
			},
		},
		toolCatalog:       fakeToolCatalog{},
		toolExecutor:      &mockToolExecutor{},
		approvals:         &mockApprovalBroker{awaitFn: func(context.Context, string) (bool, error) { return true, nil }},
		questions:         &mockQuestionBroker{},
		maxToolIterations: 2,
	}

	stream, err := orch.Stream(context.Background(), AgentChatRequest{
		UserID:      1,
		WorkspaceID: 1,
		Model:       "test-model",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := collectEvents(t, stream)
	if callCount != 2 {
		t.Fatalf("expected 2 chat iterations, got %d", callCount)
	}

	warningFound := false
	doneFound := false
	for _, event := range events {
		if strings.Contains(event.TextDelta, "Reached the maximum number of tool call iterations") {
			warningFound = true
		}
		if event.Done {
			doneFound = true
		}
	}

	if !warningFound {
		t.Fatalf("expected max-iterations warning in events: %+v", events)
	}
	if !doneFound {
		t.Fatalf("expected done event in events: %+v", events)
	}
}

func TestAgentChatOrchestrator_StreamValidatesModelErrors(t *testing.T) {
	modelErr := errors.New("boom")
	orch := &agentChatOrchestrator{
		service: &mockChatService{findModelFn: func(string) (domain.Model, error) {
			return domain.Model{}, modelErr
		}},
		toolCatalog:       fakeToolCatalog{},
		toolExecutor:      &mockToolExecutor{},
		approvals:         &mockApprovalBroker{},
		questions:         &mockQuestionBroker{},
		maxToolIterations: defaultMaxToolIterations,
	}

	_, err := orch.Stream(context.Background(), AgentChatRequest{Model: "test-model"})
	if !errors.Is(err, modelErr) {
		t.Fatalf("expected model error, got %v", err)
	}
}

// gatedToolExecutor asks for approval before start_task, as a tool provider
// that starts agents does.
type gatedToolExecutor struct {
	mockToolExecutor
}

func (gatedToolExecutor) ApprovalFor(_ context.Context, req aitools.ExecutionRequest) (aitools.Approval, bool) {
	if req.ToolName != "start_task" {
		return aitools.Approval{}, false
	}
	return aitools.Approval{Title: "Start a background agent", Summary: "Start the coding agent on task #3."}, true
}

func TestAgentChatOrchestrator_ExecutorApprovalGatesTheCall(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(fmt.Sprintf("approved=%v", approve), func(t *testing.T) {
			decisionCh := make(chan bool, 1)
			callCount, toolCalls := 0, 0
			executor := &gatedToolExecutor{mockToolExecutor{executeToolFn: func(_ context.Context, req aitools.ExecutionRequest) domain.ToolResultPart {
				toolCalls++
				return domain.ToolResultPart{Name: req.ToolName, Content: "started"}
			}}}
			orch := &agentChatOrchestrator{
				service: &mockChatService{
					chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
						ch := make(chan domain.ProviderEvent, 2)
						callCount++
						if callCount == 1 {
							ch <- domain.ProviderEvent{ToolCalls: []domain.ToolCall{{
								ID: "tc1", Type: "function",
								Function: domain.ToolCallFunction{Name: "start_task", Arguments: `{"task_id":3}`},
							}}}
						}
						ch <- domain.ProviderEvent{Done: true}
						close(ch)
						return ch, nil
					},
				},
				toolCatalog:  fakeToolCatalog{},
				toolExecutor: executor,
				approvals: &mockApprovalBroker{
					awaitFn: func(_ context.Context, _ string) (bool, error) { return <-decisionCh, nil },
				},
				maxToolIterations: defaultMaxToolIterations,
				questions:         &mockQuestionBroker{},
			}

			stream, err := orch.Stream(context.Background(), AgentChatRequest{
				UserID: 1, WorkspaceID: 1, Model: "test-model",
				Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			nextEvent(t, stream) // the tool call
			approvalEvent := nextEvent(t, stream)
			if approvalEvent.Approval == nil || approvalEvent.Approval.Title != "Start a background agent" || approvalEvent.Approval.Command != "Start the coding agent on task #3." {
				t.Fatalf("expected an approval request from the executor, got %+v", approvalEvent)
			}
			if toolCalls != 0 {
				t.Fatal("the tool ran before the user decided")
			}
			decisionCh <- approve

			resultEvent := nextEvent(t, stream)
			if resultEvent.ApprovalResult == nil || resultEvent.ApprovalResult.Title != "Start a background agent" {
				t.Fatalf("expected an approval result with its title, got %+v", resultEvent)
			}
			toolResult := nextEvent(t, stream).ToolResult
			if toolResult == nil {
				t.Fatal("expected a tool result")
			}
			if approve {
				if toolCalls != 1 || toolResult.Content != "started" {
					t.Fatalf("expected the approved call to run once, got %d calls, %+v", toolCalls, toolResult)
				}
			} else if toolCalls != 0 || !toolResult.IsError || !strings.Contains(toolResult.Content, "denied") {
				t.Fatalf("expected the denied call not to run, got %d calls, %+v", toolCalls, toolResult)
			}
		})
	}
}

type promptToolCatalog struct{ fakeToolCatalog }

func (promptToolCatalog) PromptFor(_ context.Context, _, workspaceID int64) string {
	return fmt.Sprintf("Project context for workspace %d", workspaceID)
}

func TestAgentChatOrchestrator_AddsWorkspaceContextToTheSystemPrompt(t *testing.T) {
	var captured domain.ChatRequest
	orch := &agentChatOrchestrator{
		service: &mockChatService{chatStreamFn: func(_ context.Context, req domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
			captured = req
			ch := make(chan domain.ProviderEvent, 1)
			ch <- domain.ProviderEvent{Done: true}
			close(ch)
			return ch, nil
		}},
		toolCatalog:       promptToolCatalog{},
		toolExecutor:      &mockToolExecutor{},
		approvals:         &mockApprovalBroker{},
		questions:         &mockQuestionBroker{},
		maxToolIterations: defaultMaxToolIterations,
	}
	stream, err := orch.Stream(context.Background(), AgentChatRequest{
		UserID: 1, WorkspaceID: 9, Model: "m", AgentPrompt: "agent",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}
	system := captured.Turns[0].Text()
	if !strings.Contains(system, "Project context for workspace 9") || strings.Index(system, "Project context") > strings.Index(system, "Selected agent instructions") {
		t.Fatalf("expected the workspace context before the agent instructions:\n%s", system)
	}
}
