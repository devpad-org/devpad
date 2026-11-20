package app

import "github.com/devpad-org/devpad/internal/ai/domain"

// repairToolPairing makes every tool result in a history answer a tool call in
// the assistant turn just before it, and every tool call have a result.
// Providers reject a history that breaks either rule. Conversations saved by
// browsers before the server owned them could lose tool calls, so a result
// whose call is missing gets the call back from findCall, and anything that
// still cannot be paired is dropped. It reports whether the history changed.
func repairToolPairing(turns []domain.Turn, findCall func(id string) (domain.ToolCall, bool)) ([]domain.Turn, bool) {
	repaired := make([]domain.Turn, 0, len(turns))
	changed := false

	for i := 0; i < len(turns); i++ {
		turn := turns[i]
		if turn.Role != domain.RoleAssistant {
			if isToolResultTurn(turn) {
				// No assistant turn precedes these results, so nothing can answer them.
				if kept, ok := withoutToolResults(turn); ok {
					repaired = append(repaired, kept)
				}
				changed = true
				continue
			}
			repaired = append(repaired, turn)
			continue
		}

		var results []domain.Turn
		for i+1 < len(turns) && isToolResultTurn(turns[i+1]) {
			i++
			results = append(results, turns[i])
		}

		assistant, keptResults, stepChanged := repairStep(turn, results, findCall)
		changed = changed || stepChanged
		if len(assistant.Parts) > 0 {
			repaired = append(repaired, assistant)
		}
		repaired = append(repaired, keptResults...)
	}

	return repaired, changed
}

func repairStep(assistant domain.Turn, results []domain.Turn, findCall func(id string) (domain.ToolCall, bool)) (domain.Turn, []domain.Turn, bool) {
	changed := false
	calls := make(map[string]bool)
	for _, call := range assistant.ToolCalls() {
		calls[call.ID] = true
	}

	answered := make(map[string]bool)
	var keptResults []domain.Turn
	var restored []domain.Part
	for _, turn := range results {
		var parts []domain.Part
		for _, part := range turn.Parts {
			if part.Kind != domain.PartToolResult || part.ToolResult == nil {
				parts = append(parts, part)
				continue
			}
			id := part.ToolResult.ToolCallID
			if answered[id] {
				changed = true
				continue
			}
			if !calls[id] {
				call, ok := findCall(id)
				if !ok {
					changed = true
					continue
				}
				restored = append(restored, domain.Part{Kind: domain.PartToolCall, ToolCall: &call})
				calls[id] = true
				changed = true
			}
			answered[id] = true
			parts = append(parts, part)
		}
		if len(parts) > 0 {
			turn.Parts = parts
			keptResults = append(keptResults, turn)
		}
	}

	var parts []domain.Part
	for _, part := range assistant.Parts {
		if part.Kind == domain.PartToolCall && part.ToolCall != nil && !answered[part.ToolCall.ID] {
			changed = true
			continue
		}
		parts = append(parts, part)
	}
	parts = append(parts, restored...)

	return domain.Turn{Role: assistant.Role, Parts: parts}, keptResults, changed
}

func isToolResultTurn(turn domain.Turn) bool {
	if turn.Role != domain.RoleUser {
		return false
	}
	for _, part := range turn.Parts {
		if part.Kind == domain.PartToolResult {
			return true
		}
	}
	return false
}

func withoutToolResults(turn domain.Turn) (domain.Turn, bool) {
	var parts []domain.Part
	for _, part := range turn.Parts {
		if part.Kind != domain.PartToolResult {
			parts = append(parts, part)
		}
	}
	turn.Parts = parts
	return turn, len(parts) > 0
}
