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

func TestAgentChatOrchestrator_EmptyToolArgumentsAreAnEmptyObject(t *testing.T) {
	callCount := 0
	var got json.RawMessage
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				ch := make(chan domain.ProviderEvent, 2)
				callCount++
				if callCount == 1 {
					ch <- domain.ProviderEvent{ToolCalls: []domain.ToolCall{
						{ID: "tc1", Type: "function", Function: domain.ToolCallFunction{Name: "read_merge_request_comments"}},
					}}
				} else {
					ch <- domain.ProviderEvent{TextDelta: "done"}
				}
				ch <- domain.ProviderEvent{Done: true}
				close(ch)
				return ch, nil
			},
		},
		toolCatalog: fakeToolCatalog{},
		toolExecutor: &mockToolExecutor{executeToolFn: func(_ context.Context, req aitools.ExecutionRequest) domain.ToolResultPart {
			got = req.Arguments
			return domain.ToolResultPart{Name: req.ToolName, Content: "ok"}
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
	collectEvents(t, stream)

	if string(got) != "{}" {
		t.Fatalf("expected the tool to get {}, got %q", got)
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

func TestAgentChatOrchestrator_MaxIterationsFailsTheRun(t *testing.T) {
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

	errorFound := false
	doneFound := false
	for _, event := range events {
		if strings.Contains(event.ErrorMessage, "the agent made 2 model requests") {
			errorFound = true
		}
		if event.Done {
			doneFound = true
		}
	}

	if !errorFound {
		t.Fatalf("expected the run to fail on its request limit: %+v", events)
	}
	if !doneFound {
		t.Fatalf("expected done event in events: %+v", events)
	}
}

func TestAgentChatOrchestrator_StopsWhenTheBudgetIsSpent(t *testing.T) {
	callCount := 0
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			findModelFn: func(id string) (domain.Model, error) {
				// $1 per million input tokens, so each request costs $0.40.
				return domain.Model{ID: id, Pricing: &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 1}}}, nil
			},
			chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				callCount++
				ch := make(chan domain.ProviderEvent, 3)
				ch <- domain.ProviderEvent{Usage: &domain.Usage{InputTokens: 400_000}}
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
		approvals:         &mockApprovalBroker{},
		questions:         &mockQuestionBroker{},
		maxToolIterations: defaultMaxToolIterations,
	}

	var recorded float64
	budget := domain.NewBudget(1, 0.1, func(spent float64) { recorded = spent })
	stream, err := orch.Stream(context.Background(), AgentChatRequest{
		UserID:      1,
		WorkspaceID: 1,
		Model:       "test-model",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
		Budget:      budget,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	events := collectEvents(t, stream)

	// $0.10 already spent plus two requests is $0.90, under the limit; the
	// third takes it to $1.30, and no fourth is made.
	if callCount != 3 {
		t.Fatalf("expected 3 requests, got %d", callCount)
	}
	if recorded < 1.29 || recorded > 1.31 {
		t.Fatalf("expected $1.30 recorded as spent in all, got %v", recorded)
	}
	last := events[len(events)-1]
	stop := events[len(events)-2]
	if !last.Done || !strings.Contains(stop.ErrorMessage, "budget of $1.00 is spent ($1.30)") {
		t.Fatalf("expected the run to fail on its budget, got %+v then %+v", stop, last)
	}
	// The last request's tool call ran, so the transcript can be continued.
	if events[len(events)-3].ToolResult == nil {
		t.Fatalf("expected the last tool call's result before the stop, got %+v", events[len(events)-3])
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
	err error
}

func (g gatedToolExecutor) ApprovalFor(_ context.Context, req aitools.ExecutionRequest) (aitools.Approval, bool, error) {
	if g.err != nil {
		return aitools.Approval{}, false, g.err
	}
	if req.ToolName != "start_task" {
		return aitools.Approval{}, false, nil
	}
	return aitools.Approval{
		Title:   "Start a background agent",
		Summary: "Start the coding agent on task #3.",
		Details: []domain.ApprovalDetail{{Label: "Instructions", Value: "do it", Text: true}},
	}, true, nil
}

func TestAgentChatOrchestrator_ExecutorApprovalGatesTheCall(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(fmt.Sprintf("approved=%v", approve), func(t *testing.T) {
			decisionCh := make(chan bool, 1)
			callCount, toolCalls := 0, 0
			executor := &gatedToolExecutor{mockToolExecutor: mockToolExecutor{executeToolFn: func(_ context.Context, req aitools.ExecutionRequest) domain.ToolResultPart {
				toolCalls++
				if !req.Approved {
					return domain.ToolResultPart{Name: req.ToolName, Content: "not approved", IsError: true}
				}
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
			if d := approvalEvent.Approval.Details; len(d) != 1 || d[0].Value != "do it" || !d[0].Text {
				t.Fatalf("expected the approval's details, got %+v", d)
			}
			if toolCalls != 0 {
				t.Fatal("the tool ran before the user decided")
			}
			decisionCh <- approve

			resultEvent := nextEvent(t, stream)
			if resultEvent.ApprovalResult == nil || resultEvent.ApprovalResult.Title != "Start a background agent" || len(resultEvent.ApprovalResult.Details) != 1 {
				t.Fatalf("expected an approval result with its title and details, got %+v", resultEvent)
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

func TestAgentChatOrchestrator_ApprovalCheckErrorFailsClosed(t *testing.T) {
	toolCalls, callCount := 0, 0
	executor := &gatedToolExecutor{
		mockToolExecutor: mockToolExecutor{executeToolFn: func(_ context.Context, req aitools.ExecutionRequest) domain.ToolResultPart {
			toolCalls++
			return domain.ToolResultPart{Name: req.ToolName, Content: "started"}
		}},
		err: errors.New("database is locked"),
	}
	opened := false
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
		approvals: &mockApprovalBroker{openFn: func(context.Context, int64, string) (domain.ApprovalRequest, error) {
			opened = true
			return domain.ApprovalRequest{ID: "a1"}, nil
		}},
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
	toolResult := nextEvent(t, stream).ToolResult
	if toolResult == nil || !toolResult.IsError || !strings.Contains(toolResult.Content, "did not run") {
		t.Fatalf("expected a failed tool call, got %+v", toolResult)
	}
	if toolCalls != 0 || opened {
		t.Fatalf("expected no run and no approval card, got %d calls, opened=%v", toolCalls, opened)
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

func TestAgentChatOrchestrator_EmitsPricedUsageForEachRequest(t *testing.T) {
	tests := []struct {
		name     string
		pricing  *domain.Pricing
		wantCost *float64
	}{
		{
			name:     "priced model",
			pricing:  &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 3, Output: 15, CacheRead: 0.3}},
			wantCost: func() *float64 { cost := (1000*3 + 200*15 + 5000*0.3) / 1e6; return &cost }(),
		},
		{name: "unpriced model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orch := &agentChatOrchestrator{
				service: &mockChatService{
					findModelFn: func(modelID string) (domain.Model, error) {
						return domain.Model{ID: modelID, Pricing: tt.pricing}, nil
					},
					chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
						ch := make(chan domain.ProviderEvent, 3)
						ch <- domain.ProviderEvent{TextDelta: "done"}
						ch <- domain.ProviderEvent{Usage: &domain.Usage{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 5000}}
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
				UserID:      1,
				WorkspaceID: 1,
				Model:       "model-1",
				Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hi")},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var usages []domain.Usage
			for _, event := range collectEvents(t, stream) {
				if event.Usage != nil {
					usages = append(usages, *event.Usage)
				}
			}
			if len(usages) != 1 {
				t.Fatalf("expected one usage event, got %+v", usages)
			}
			usage := usages[0]
			if usage.InputTokens != 1000 || usage.OutputTokens != 200 || usage.CacheReadTokens != 5000 {
				t.Fatalf("unexpected usage tokens: %+v", usage)
			}
			switch {
			case tt.wantCost == nil && usage.CostUSD != nil:
				t.Fatalf("expected no cost for an unpriced model, got %v", *usage.CostUSD)
			case tt.wantCost != nil && (usage.CostUSD == nil || *usage.CostUSD != *tt.wantCost):
				t.Fatalf("cost = %v, want %v", usage.CostUSD, *tt.wantCost)
			}
		})
	}
}

func TestAgentChatOrchestrator_MetersARequestCutShort(t *testing.T) {
	// Buffered as a provider's is, so the test can send before the run
	// reads: it first sends its context size.
	provider := make(chan domain.ProviderEvent, 4)
	orch := &agentChatOrchestrator{
		service: &mockChatService{
			findModelFn: func(id string) (domain.Model, error) {
				return domain.Model{ID: id, Pricing: &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 1}}}, nil
			},
			chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
				return provider, nil
			},
		},
		toolCatalog:       fakeToolCatalog{},
		toolExecutor:      &mockToolExecutor{},
		approvals:         &mockApprovalBroker{},
		questions:         &mockQuestionBroker{},
		maxToolIterations: defaultMaxToolIterations,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spent := make(chan float64, 1)
	recorded := make(chan domain.Usage, 1)
	stream, err := orch.Stream(ctx, AgentChatRequest{
		UserID:      1,
		WorkspaceID: 1,
		Model:       "test-model",
		Turns:       []domain.Turn{domain.NewTextTurn(domain.RoleUser, "go")},
		Budget:      domain.NewBudget(0, 0, func(total float64) { spent <- total }),
		RecordUsage: func(usage domain.Usage) { recorded <- usage },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	provider <- domain.ProviderEvent{TextDelta: "hel"}
	if event := nextEvent(t, stream); event.TextDelta != "hel" {
		t.Fatalf("expected the text, got %+v", event)
	}

	// The user stops the reply; the provider reports what the request was
	// billed for as it ends, after nobody reads the chat any more.
	cancel()
	collectEvents(t, stream)
	provider <- domain.ProviderEvent{Usage: &domain.Usage{InputTokens: 400_000}}
	provider <- domain.ProviderEvent{Done: true}
	close(provider)

	select {
	case usage := <-recorded:
		if usage.InputTokens != 400_000 || usage.CostUSD == nil || *usage.CostUSD != 0.4 {
			t.Fatalf("expected the request recorded and priced, got %+v", usage)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request cut short was never recorded")
	}
	select {
	case total := <-spent:
		if total != 0.4 {
			t.Fatalf("expected $0.40 spent, got %v", total)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request cut short was never spent from the budget")
	}
}
