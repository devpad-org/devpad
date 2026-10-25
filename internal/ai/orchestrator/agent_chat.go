package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/ai/question"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
)

const defaultMaxToolIterations = 100

type agentChatOrchestrator struct {
	service           ChatService
	toolCatalog       ToolCatalog
	toolExecutor      ToolExecutor
	approvals         approval.Broker
	questions         question.Broker
	maxToolIterations int
}

// NewAgentChatOrchestrator creates the phase-2 agent orchestrator.
func NewAgentChatOrchestrator(service ChatService, toolCatalog ToolCatalog, toolExecutor ToolExecutor, approvals approval.Broker, questions question.Broker) AgentChatOrchestrator {
	return &agentChatOrchestrator{
		service:           service,
		toolCatalog:       toolCatalog,
		toolExecutor:      toolExecutor,
		approvals:         approvals,
		questions:         questions,
		maxToolIterations: defaultMaxToolIterations,
	}
}

func (o *agentChatOrchestrator) Stream(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error) {
	model, err := o.service.FindModel(req.Model)
	if err != nil {
		return nil, err
	}

	if err := domain.ValidateThinkingRequest(model, domain.ChatRequest{
		Model:    req.Model,
		Turns:    req.Turns,
		Thinking: req.Thinking,
	}); err != nil {
		return nil, err
	}

	out := make(chan domain.ClientEvent)
	go o.run(ctx, req, model, out)

	return out, nil
}

func (o *agentChatOrchestrator) run(ctx context.Context, req AgentChatRequest, model domain.Model, out chan<- domain.ClientEvent) {
	defer close(out)

	systemPrompt := o.toolCatalog.SystemPrompt()
	if strings.TrimSpace(req.WorkspaceInstructions) != "" {
		systemPrompt = systemPrompt + "\n\nWorkspace instructions from AGENTS.md:\n" + strings.TrimSpace(req.WorkspaceInstructions)
	}
	if scoped, ok := o.toolCatalog.(WorkspacePromptCatalog); ok {
		if section := strings.TrimSpace(scoped.PromptFor(ctx, req.UserID, req.WorkspaceID)); section != "" {
			systemPrompt = systemPrompt + "\n\n" + section
		}
	}
	if strings.TrimSpace(req.AgentPrompt) != "" {
		systemPrompt = systemPrompt + "\n\nSelected agent instructions:\n" + strings.TrimSpace(req.AgentPrompt)
	}
	turns := make([]domain.Turn, 0, len(req.Turns)+1)
	turns = append(turns, domain.Turn{
		Role: domain.RoleSystem,
		Parts: []domain.Part{{
			Kind: domain.PartText,
			Text: systemPrompt,
		}},
	})
	turns = append(turns, req.Turns...)
	toolDefinitions := o.toolCatalog.Definitions()
	if scoped, ok := o.toolCatalog.(WorkspaceToolCatalog); ok {
		toolDefinitions = scoped.DefinitionsFor(ctx, req.UserID, req.WorkspaceID)
	}

	for i := 0; i < o.maxToolIterations; i++ {
		if ctx.Err() != nil {
			return
		}

		chatReq := domain.ChatRequest{
			Model:    req.Model,
			Turns:    turns,
			Thinking: req.Thinking,
			Tools:    toolDefinitions,
		}
		if !emitEvent(ctx, out, domain.ClientEvent{ContextSize: contextSizeForRequest(model, chatReq)}) {
			return
		}

		stream, err := o.service.ChatStream(ctx, chatReq)
		if err != nil {
			if !emitEvent(ctx, out, domain.ClientEvent{ErrorMessage: err.Error()}) {
				return
			}
			emitEvent(ctx, out, domain.ClientEvent{Done: true})
			return
		}

		var toolCalls []domain.ToolCall
		var reasoningAccum strings.Builder
		var thinkingState json.RawMessage
		var contentAccum strings.Builder

		streamDone := false
		for !streamDone {
			select {
			case event, ok := <-stream:
				if !ok {
					streamDone = true
					continue
				}
				if event.Err != nil {
					if !emitEvent(ctx, out, domain.ClientEvent{ErrorMessage: event.Err.Error()}) {
						return
					}
					emitEvent(ctx, out, domain.ClientEvent{Done: true})
					return
				}
				if event.ReasoningDelta != "" || len(event.ReasoningState) > 0 {
					if !emitEvent(ctx, out, domain.ClientEvent{
						ReasoningDelta: event.ReasoningDelta,
						ReasoningState: domain.CloneRawMessage(event.ReasoningState),
					}) {
						return
					}
				}
				if event.ReasoningDelta != "" {
					reasoningAccum.WriteString(event.ReasoningDelta)
				}
				if len(event.ReasoningState) > 0 {
					thinkingState = domain.CloneRawMessage(event.ReasoningState)
				}
				if event.TextDelta != "" {
					if !emitEvent(ctx, out, domain.ClientEvent{TextDelta: event.TextDelta}) {
						return
					}
					contentAccum.WriteString(event.TextDelta)
				}
				if len(event.ToolCalls) > 0 {
					toolCalls = append([]domain.ToolCall(nil), event.ToolCalls...)
				}
			case <-ctx.Done():
				return
			}
		}

		if len(toolCalls) == 0 {
			break
		}

		assistantTurn := domain.Turn{Role: domain.RoleAssistant}
		if reasoningAccum.Len() > 0 || len(thinkingState) > 0 {
			assistantTurn.Parts = append(assistantTurn.Parts, domain.Part{
				Kind: domain.PartThinking,
				Thinking: &domain.ThinkingPart{
					Text:  reasoningAccum.String(),
					State: domain.CloneRawMessage(thinkingState),
				},
			})
		}
		if contentAccum.Len() > 0 {
			assistantTurn.Parts = append(assistantTurn.Parts, domain.Part{Kind: domain.PartText, Text: contentAccum.String()})
		}
		for _, toolCall := range toolCalls {
			toolCall := toolCall
			assistantTurn.Parts = append(assistantTurn.Parts, domain.Part{Kind: domain.PartToolCall, ToolCall: &toolCall})
		}
		turns = append(turns, assistantTurn)

		if !emitEvent(ctx, out, domain.ClientEvent{ToolCalls: append([]domain.ToolCall(nil), toolCalls...)}) {
			return
		}

		for _, toolCall := range toolCalls {
			if !o.handleToolCall(ctx, req, toolCall, &turns, out) {
				return
			}
		}

		if i == o.maxToolIterations-1 {
			if !emitEvent(ctx, out, domain.ClientEvent{
				TextDelta: fmt.Sprintf("\n\n⚠️ Reached the maximum number of tool call iterations (%d). Stopping here.", o.maxToolIterations),
			}) {
				return
			}
		}
	}

	emitEvent(ctx, out, domain.ClientEvent{Done: true})
}

func (o *agentChatOrchestrator) handleToolCall(ctx context.Context, req AgentChatRequest, toolCall domain.ToolCall, turns *[]domain.Turn, out chan<- domain.ClientEvent) bool {
	if toolCall.Function.Name == "update_plan" {
		var planArgs struct {
			Steps []domain.PlanStep `json:"steps"`
		}
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &planArgs); err == nil && len(planArgs.Steps) > 0 {
			if !emitEvent(ctx, out, domain.ClientEvent{Plan: planArgs.Steps}) {
				return false
			}
		}
	}

	if toolCall.Function.Name == "ask_user" {
		return o.handleUserQuestion(ctx, req, toolCall, turns, out)
	}

	execReq := aitools.ExecutionRequest{
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		CurrentRunID:   req.CurrentRunID,
		ConversationID: req.ConversationID,
		Model:          req.Model,
		Thinking:       req.Thinking,
		ToolName:       toolCall.Function.Name,
		Arguments:      json.RawMessage(toolCall.Function.Arguments),
	}
	if gate, needed := o.approvalFor(ctx, execReq); needed {
		result, approved, ok := o.awaitApproval(ctx, req.UserID, gate, out)
		if !ok {
			return false
		}
		if !approved {
			return o.emitToolResult(ctx, domain.ToolResultPart{
				ToolCallID: toolCall.ID,
				Name:       toolCall.Function.Name,
				Content:    result,
				IsError:    true,
			}, turns, out)
		}
	}

	result := o.toolExecutor.ExecuteTool(ctx, execReq)
	if ctx.Err() != nil {
		return false
	}
	result.ToolCallID = toolCall.ID
	if result.Name == "" {
		result.Name = toolCall.Function.Name
	}

	return o.emitToolResult(ctx, result, turns, out)
}

func (o *agentChatOrchestrator) handleUserQuestion(ctx context.Context, req AgentChatRequest, toolCall domain.ToolCall, turns *[]domain.Turn, out chan<- domain.ClientEvent) bool {
	if o.questions == nil {
		return o.emitToolResult(ctx, domain.ToolResultPart{
			ToolCallID: toolCall.ID,
			Name:       toolCall.Function.Name,
			Content:    "User question failed: question broker is not configured.",
			IsError:    true,
		}, turns, out)
	}

	questionRequest, err := parseUserQuestionArgs(toolCall.Function.Arguments)
	if err != nil {
		return o.emitToolResult(ctx, domain.ToolResultPart{
			ToolCallID: toolCall.ID,
			Name:       toolCall.Function.Name,
			Content:    fmt.Sprintf("User question failed: %v", err),
			IsError:    true,
		}, turns, out)
	}

	opened, err := o.questions.Open(ctx, req.UserID, questionRequest)
	if err != nil {
		return o.emitToolResult(ctx, domain.ToolResultPart{
			ToolCallID: toolCall.ID,
			Name:       toolCall.Function.Name,
			Content:    fmt.Sprintf("User question failed: %v", err),
			IsError:    true,
		}, turns, out)
	}

	if !emitEvent(ctx, out, domain.ClientEvent{Question: &opened}) {
		return false
	}

	// Like approvals, wait until the user answers or the run is cancelled.
	answers, err := o.questions.Await(ctx, opened.ID)
	if err == nil {
		result := domain.UserQuestionResult{ID: opened.ID, Status: "answered", Answers: answers}
		if !emitQuestionResult(ctx, out, result) {
			return false
		}
		content, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return o.emitToolResult(ctx, domain.ToolResultPart{
				ToolCallID: toolCall.ID,
				Name:       toolCall.Function.Name,
				Content:    fmt.Sprintf("User question failed: %v", marshalErr),
				IsError:    true,
			}, turns, out)
		}
		return o.emitToolResult(ctx, domain.ToolResultPart{
			ToolCallID: toolCall.ID,
			Name:       toolCall.Function.Name,
			Content:    string(content),
		}, turns, out)
	}

	status := "failed"
	message := "User question failed."
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = "expired"
		message = "User question was cancelled before the user answered."
	default:
		message = fmt.Sprintf("User question failed: %v", err)
	}
	if !emitQuestionResult(ctx, out, domain.UserQuestionResult{ID: opened.ID, Status: status}) {
		return false
	}
	return o.emitToolResult(ctx, domain.ToolResultPart{
		ToolCallID: toolCall.ID,
		Name:       toolCall.Function.Name,
		Content:    message,
		IsError:    true,
	}, turns, out)
}

func (o *agentChatOrchestrator) emitToolResult(ctx context.Context, result domain.ToolResultPart, turns *[]domain.Turn, out chan<- domain.ClientEvent) bool {
	eventResult := result
	if !emitEvent(ctx, out, domain.ClientEvent{ToolResult: &eventResult}) {
		return false
	}
	turnResult := result

	*turns = append(*turns, domain.Turn{
		Role: domain.RoleUser,
		Parts: []domain.Part{{
			Kind:       domain.PartToolResult,
			ToolResult: &turnResult,
		}},
	})

	return true
}

// approvalGate is a tool call waiting on the user's approval, with the
// result the agent gets if the user denies it.
type approvalGate struct {
	title   string
	command string
	denied  string
}

// approvalFor reports whether a tool call must wait for the user: sudo
// commands always do, and the executor's tools may ask for it too.
func (o *agentChatOrchestrator) approvalFor(ctx context.Context, req aitools.ExecutionRequest) (approvalGate, bool) {
	if aitools.ToolCallNeedsSudoApproval(req.ToolName, string(req.Arguments)) {
		return approvalGate{
			command: aitools.CommandFromArgs(string(req.Arguments)),
			denied:  "Command denied by user. The user rejected executing this sudo command.",
		}, true
	}
	if checker, ok := o.toolExecutor.(ApprovalChecker); ok {
		if approval, needed := checker.ApprovalFor(ctx, req); needed {
			return approvalGate{
				title:   approval.Title,
				command: approval.Summary,
				denied:  "The user denied this action. Do not retry it unless the user asks you to.",
			}, true
		}
	}
	return approvalGate{}, false
}

func (o *agentChatOrchestrator) awaitApproval(ctx context.Context, userID int64, gate approvalGate, out chan<- domain.ClientEvent) (string, bool, bool) {
	request, err := o.approvals.Open(ctx, userID, gate.command)
	if err != nil {
		return "Approval failed.", false, true
	}
	request.Title = gate.title

	if !emitEvent(ctx, out, domain.ClientEvent{Approval: &request}) {
		return "", false, false
	}

	// Wait as long as it takes: a timeout here just pushes the agent toward
	// workarounds. Cancelling the run cancels ctx, which ends the wait.
	approved, err := o.approvals.Await(ctx, request.ID)
	if err == nil {
		status := "denied"
		if approved {
			status = "approved"
		}
		if !emitApprovalResult(ctx, out, request, status) {
			return "", false, false
		}
		if !approved {
			return gate.denied, false, true
		}
		return "", true, true
	}

	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		if !emitApprovalResult(ctx, out, request, "expired") {
			return "", false, false
		}
		return "Approval was cancelled before the user responded.", false, true
	default:
		if !emitApprovalResult(ctx, out, request, "failed") {
			return "", false, false
		}
		return "Approval failed.", false, true
	}
}

func emitApprovalResult(ctx context.Context, out chan<- domain.ClientEvent, request domain.ApprovalRequest, status string) bool {
	return emitEvent(ctx, out, domain.ClientEvent{ApprovalResult: &domain.ApprovalResult{
		ID:      request.ID,
		Title:   request.Title,
		Command: request.Command,
		Status:  status,
	}})
}

func emitQuestionResult(ctx context.Context, out chan<- domain.ClientEvent, result domain.UserQuestionResult) bool {
	resultCopy := result
	return emitEvent(ctx, out, domain.ClientEvent{QuestionResult: &resultCopy})
}
