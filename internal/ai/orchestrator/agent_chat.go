package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/ai/approval"
	"github.com/devpad-org/devpad/internal/ai/domain"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
)

const (
	defaultMaxToolIterations = 100
	defaultApprovalTimeout   = 60 * time.Second
)

type agentChatOrchestrator struct {
	service           ChatService
	toolCatalog       ToolCatalog
	toolExecutor      ToolExecutor
	approvals         approval.Broker
	maxToolIterations int
	approvalTimeout   time.Duration
}

// NewAgentChatOrchestrator creates the phase-2 agent orchestrator.
func NewAgentChatOrchestrator(service ChatService, toolCatalog ToolCatalog, toolExecutor ToolExecutor, approvals approval.Broker) AgentChatOrchestrator {
	return &agentChatOrchestrator{
		service:           service,
		toolCatalog:       toolCatalog,
		toolExecutor:      toolExecutor,
		approvals:         approvals,
		maxToolIterations: defaultMaxToolIterations,
		approvalTimeout:   defaultApprovalTimeout,
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
	go o.run(ctx, req, out)

	return out, nil
}

func (o *agentChatOrchestrator) run(ctx context.Context, req AgentChatRequest, out chan<- domain.ClientEvent) {
	defer close(out)

	systemPrompt := o.toolCatalog.SystemPrompt()
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

	for i := 0; i < o.maxToolIterations; i++ {
		if ctx.Err() != nil {
			return
		}

		stream, err := o.service.ChatStream(ctx, domain.ChatRequest{
			Model:    req.Model,
			Turns:    turns,
			Thinking: req.Thinking,
			Tools:    toolDefinitions,
		})
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

	if aitools.ToolCallNeedsSudoApproval(toolCall.Function.Name, toolCall.Function.Arguments) {
		result, approved, ok := o.awaitApproval(ctx, req.UserID, toolCall.Function.Arguments, out)
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

	result := o.toolExecutor.ExecuteTool(ctx, aitools.ExecutionRequest{
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		CurrentRunID:   req.CurrentRunID,
		ConversationID: req.ConversationID,
		Model:          req.Model,
		Thinking:       req.Thinking,
		ToolName:       toolCall.Function.Name,
		Arguments:      json.RawMessage(toolCall.Function.Arguments),
	})
	if ctx.Err() != nil {
		return false
	}
	result.ToolCallID = toolCall.ID
	if result.Name == "" {
		result.Name = toolCall.Function.Name
	}

	return o.emitToolResult(ctx, result, turns, out)
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

func (o *agentChatOrchestrator) awaitApproval(ctx context.Context, userID int64, args string, out chan<- domain.ClientEvent) (string, bool, bool) {
	request, err := o.approvals.Open(ctx, userID, aitools.CommandFromArgs(args))
	if err != nil {
		return "Command approval failed.", false, true
	}

	if !emitEvent(ctx, out, domain.ClientEvent{Approval: &request}) {
		return "", false, false
	}

	approvalCtx, cancel := context.WithTimeout(ctx, o.approvalTimeout)
	defer cancel()
	approved, err := o.approvals.Await(approvalCtx, request.ID)
	if err == nil {
		status := "denied"
		if approved {
			status = "approved"
		}
		if !emitApprovalResult(ctx, out, request, status) {
			return "", false, false
		}
		if !approved {
			return "Command denied by user. The user rejected executing this sudo command.", false, true
		}
		return "", true, true
	}

	switch {
	case errors.Is(err, context.Canceled):
		if !emitApprovalResult(ctx, out, request, "expired") {
			return "", false, false
		}
		return "Command approval timed out — the request was cancelled.", false, true
	case errors.Is(err, context.DeadlineExceeded):
		if !emitApprovalResult(ctx, out, request, "expired") {
			return "", false, false
		}
		return "Command approval timed out after 60 seconds.", false, true
	default:
		if !emitApprovalResult(ctx, out, request, "failed") {
			return "", false, false
		}
		return "Command approval failed.", false, true
	}
}

func emitApprovalResult(ctx context.Context, out chan<- domain.ClientEvent, request domain.ApprovalRequest, status string) bool {
	return emitEvent(ctx, out, domain.ClientEvent{ApprovalResult: &domain.ApprovalResult{
		ID:      request.ID,
		Command: request.Command,
		Status:  status,
	}})
}
