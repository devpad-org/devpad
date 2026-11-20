package shared

import (
	"slices"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// ToolCallDelta is one fragment of a streamed chat completion tool call.
type ToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type pendingToolCall struct {
	call      domain.ToolCall
	arguments strings.Builder
}

// ToolCallAccumulator puts streamed tool calls back together. The index says
// which call a fragment belongs to. Providers differ in which fragments repeat
// the id and name, so a repeat adds nothing and only the arguments accumulate.
type ToolCallAccumulator struct {
	pending map[int]*pendingToolCall
}

// Add folds one fragment into its call. A fragment with both a name and an id
// other than the call's starts a new call at that index, replacing the old one.
func (a *ToolCallAccumulator) Add(delta ToolCallDelta) {
	if a.pending == nil {
		a.pending = make(map[int]*pendingToolCall)
	}
	p := a.pending[delta.Index]
	if p == nil || (delta.Function.Name != "" && delta.ID != "" && p.call.ID != "" && delta.ID != p.call.ID) {
		p = &pendingToolCall{}
		a.pending[delta.Index] = p
	}
	if p.call.ID == "" {
		p.call.ID = delta.ID
	}
	if p.call.Type == "" {
		p.call.Type = delta.Type
	}
	if p.call.Function.Name == "" {
		p.call.Function.Name = delta.Function.Name
	}
	p.arguments.WriteString(delta.Function.Arguments)
}

// Calls returns the calls in index order. A call that never got an id is left
// out, since its result could not be sent back.
func (a *ToolCallAccumulator) Calls() []domain.ToolCall {
	indexes := make([]int, 0, len(a.pending))
	for index := range a.pending {
		indexes = append(indexes, index)
	}
	slices.Sort(indexes)

	var calls []domain.ToolCall
	for _, index := range indexes {
		p := a.pending[index]
		if p.call.ID == "" {
			continue
		}
		call := p.call
		if call.Type == "" {
			call.Type = "function"
		}
		call.Function.Arguments = p.arguments.String()
		calls = append(calls, call)
	}
	return calls
}

// Emit sends the accumulated calls, if there are any.
func (a *ToolCallAccumulator) Emit(ch chan<- domain.ProviderEvent) {
	if calls := a.Calls(); len(calls) > 0 {
		ch <- domain.ProviderEvent{ToolCalls: calls}
	}
}
