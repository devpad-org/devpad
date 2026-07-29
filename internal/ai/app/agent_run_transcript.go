package app

import (
	"encoding/json"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type agentRunTranscriptRound struct {
	content       string
	reasoning     string
	thinkingState json.RawMessage
	toolCalls     []domain.ToolCall
	toolResults   []domain.ToolResultPart
}

func buildAgentRunConversationTurns(inputTurns []domain.Turn, events []domain.AgentRunEvent) []domain.Turn {
	turns := cloneTurns(inputTurns)
	rounds := []agentRunTranscriptRound{{}}

	for _, stored := range events {
		event := stored.Event
		current := &rounds[len(rounds)-1]

		if event.ReasoningDelta != "" || len(event.ReasoningState) > 0 {
			if len(current.toolResults) > 0 {
				rounds = append(rounds, agentRunTranscriptRound{})
				current = &rounds[len(rounds)-1]
			}
			current.reasoning += event.ReasoningDelta
			if len(event.ReasoningState) > 0 {
				current.thinkingState = domain.CloneRawMessage(event.ReasoningState)
			}
		}

		if event.TextDelta != "" {
			if len(current.toolResults) > 0 {
				rounds = append(rounds, agentRunTranscriptRound{})
				current = &rounds[len(rounds)-1]
			}
			current.content += event.TextDelta
		}

		if len(event.ToolCalls) > 0 {
			if len(current.toolResults) > 0 {
				rounds = append(rounds, agentRunTranscriptRound{})
				current = &rounds[len(rounds)-1]
			}
			for _, toolCall := range event.ToolCalls {
				current.toolCalls = append(current.toolCalls, cloneTranscriptToolCall(toolCall))
			}
		}

		if event.ToolResult != nil {
			current.toolResults = append(current.toolResults, *event.ToolResult)
		}
	}

	for _, round := range rounds {
		round = round.withAnsweredToolCallsOnly()
		if round.hasAssistantTurn() {
			turns = append(turns, round.assistantTurn())
		}
		for _, result := range round.toolResults {
			turns = append(turns, domain.NewToolResultTurn(result.ToolCallID, result.Name, result.Content, result.IsError))
		}
	}

	return turns
}

// withAnsweredToolCallsOnly drops tool calls that never produced a result, which
// happens when a run ends mid-iteration. Providers reject an assistant message
// whose tool calls have no matching tool results, so keeping them would make
// every later request in the conversation fail.
func (r agentRunTranscriptRound) withAnsweredToolCallsOnly() agentRunTranscriptRound {
	if len(r.toolCalls) == 0 {
		return r
	}

	answered := make(map[string]struct{}, len(r.toolResults))
	for _, result := range r.toolResults {
		answered[result.ToolCallID] = struct{}{}
	}

	kept := make([]domain.ToolCall, 0, len(r.toolCalls))
	for _, toolCall := range r.toolCalls {
		if _, ok := answered[toolCall.ID]; ok {
			kept = append(kept, toolCall)
		}
	}
	r.toolCalls = kept

	return r
}

func (r agentRunTranscriptRound) hasAssistantTurn() bool {
	return r.content != "" || r.reasoning != "" || len(r.thinkingState) > 0 || len(r.toolCalls) > 0
}

func (r agentRunTranscriptRound) assistantTurn() domain.Turn {
	turn := domain.Turn{Role: domain.RoleAssistant}
	if r.reasoning != "" || len(r.thinkingState) > 0 {
		turn.Parts = append(turn.Parts, domain.Part{
			Kind: domain.PartThinking,
			Thinking: &domain.ThinkingPart{
				Text:  r.reasoning,
				State: domain.CloneRawMessage(r.thinkingState),
			},
		})
	}
	if r.content != "" {
		turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartText, Text: r.content})
	}
	for _, toolCall := range r.toolCalls {
		toolCall := toolCall
		turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartToolCall, ToolCall: &toolCall})
	}
	return turn
}

func cloneTranscriptToolCall(toolCall domain.ToolCall) domain.ToolCall {
	return domain.ToolCall{
		ID:     toolCall.ID,
		ItemID: toolCall.ItemID,
		Type:   toolCall.Type,
		Function: domain.ToolCallFunction{
			Name:      toolCall.Function.Name,
			Arguments: toolCall.Function.Arguments,
		},
	}
}
