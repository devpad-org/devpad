package app

import (
	"encoding/json"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// runTranscript turns a run's event stream back into conversation turns, one
// model step at a time. A step is the assistant turn the model produced plus
// the tool results that answer it; the orchestrator builds its own history the
// same way, so the saved turns match what the model is sent next.
type runTranscript struct {
	round agentRunTranscriptRound
}

type agentRunTranscriptRound struct {
	content       string
	reasoning     string
	thinkingState json.RawMessage
	toolCalls     []domain.ToolCall
	toolResults   []domain.ToolResultPart
}

// add records one event and returns the turns of the step it completed, if
// any. A step is complete once every tool call in it has a result.
func (t *runTranscript) add(event domain.ClientEvent) []domain.Turn {
	round := &t.round
	if event.ReasoningDelta != "" || len(event.ReasoningState) > 0 {
		round.reasoning += event.ReasoningDelta
		if len(event.ReasoningState) > 0 {
			round.thinkingState = domain.CloneRawMessage(event.ReasoningState)
		}
	}
	round.content += event.TextDelta
	for _, toolCall := range event.ToolCalls {
		round.toolCalls = append(round.toolCalls, cloneTranscriptToolCall(toolCall))
	}
	if event.ToolResult != nil {
		round.toolResults = append(round.toolResults, *event.ToolResult)
	}

	if !round.complete() {
		return nil
	}
	turns := round.turns()
	t.round = agentRunTranscriptRound{}
	return turns
}

// flush returns the unfinished step when the run ends. Tool calls that never
// got a result are dropped: providers reject an assistant message whose tool
// calls have no matching results, so keeping them would break every later
// request in the conversation.
func (t *runTranscript) flush() []domain.Turn {
	turns := t.round.withAnsweredToolCallsOnly().turns()
	t.round = agentRunTranscriptRound{}
	return turns
}

func (r agentRunTranscriptRound) complete() bool {
	if len(r.toolCalls) == 0 {
		return false
	}
	answered := r.answeredToolCallIDs()
	for _, toolCall := range r.toolCalls {
		if _, ok := answered[toolCall.ID]; !ok {
			return false
		}
	}
	return true
}

func (r agentRunTranscriptRound) answeredToolCallIDs() map[string]struct{} {
	answered := make(map[string]struct{}, len(r.toolResults))
	for _, result := range r.toolResults {
		answered[result.ToolCallID] = struct{}{}
	}
	return answered
}

func (r agentRunTranscriptRound) withAnsweredToolCallsOnly() agentRunTranscriptRound {
	if len(r.toolCalls) == 0 {
		return r
	}

	answered := r.answeredToolCallIDs()
	kept := make([]domain.ToolCall, 0, len(r.toolCalls))
	for _, toolCall := range r.toolCalls {
		if _, ok := answered[toolCall.ID]; ok {
			kept = append(kept, toolCall)
		}
	}
	r.toolCalls = kept

	return r
}

func (r agentRunTranscriptRound) turns() []domain.Turn {
	var turns []domain.Turn
	if r.hasAssistantTurn() {
		turns = append(turns, r.assistantTurn())
	}
	for _, result := range r.toolResults {
		turns = append(turns, domain.NewToolResultTurnFromPart(result))
	}
	return turns
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
