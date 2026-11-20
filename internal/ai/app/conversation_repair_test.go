package app

import (
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestRepairToolPairing(t *testing.T) {
	call := func(id string) domain.ToolCall {
		return domain.ToolCall{ID: id, Type: "function", Function: domain.ToolCallFunction{Name: "read_file", Arguments: "{}"}}
	}
	assistant := func(text string, ids ...string) domain.Turn {
		turn := domain.Turn{Role: domain.RoleAssistant}
		if text != "" {
			turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartText, Text: text})
		}
		for _, id := range ids {
			c := call(id)
			turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartToolCall, ToolCall: &c})
		}
		return turn
	}
	result := func(id string) domain.Turn {
		return domain.NewToolResultTurn(id, "read_file", "ok", false)
	}
	user := func(text string) domain.Turn { return domain.NewTextTurn(domain.RoleUser, text) }
	known := map[string]bool{"b": true}
	findCall := func(id string) (domain.ToolCall, bool) {
		if !known[id] {
			return domain.ToolCall{}, false
		}
		return call(id), true
	}

	tests := []struct {
		name    string
		turns   []domain.Turn
		want    string
		changed bool
	}{
		{
			name:  "healthy history is unchanged",
			turns: []domain.Turn{user("go"), assistant("", "a", "b"), result("a"), result("b"), assistant("done")},
			want:  "user:go | assistant:a,b | result:a | result:b | assistant:done",
		},
		{
			name:    "missing call is restored from run events",
			turns:   []domain.Turn{user("go"), assistant("", "a"), result("a"), result("b")},
			want:    "user:go | assistant:a,b | result:a | result:b",
			changed: true,
		},
		{
			name:    "result whose call cannot be found is dropped",
			turns:   []domain.Turn{user("go"), assistant("", "a"), result("a"), result("c")},
			want:    "user:go | assistant:a | result:a",
			changed: true,
		},
		{
			name:    "call without a result is dropped",
			turns:   []domain.Turn{user("go"), assistant("reading", "a", "c"), result("a"), user("next")},
			want:    "user:go | assistant:a | result:a | user:next",
			changed: true,
		},
		{
			name:    "assistant turn left empty is dropped",
			turns:   []domain.Turn{user("go"), assistant("", "c"), user("next")},
			want:    "user:go | user:next",
			changed: true,
		},
		{
			name:    "result with no assistant turn before it is dropped",
			turns:   []domain.Turn{user("go"), result("a"), assistant("hi")},
			want:    "user:go | assistant:hi",
			changed: true,
		},
		{
			name:    "duplicate result is dropped",
			turns:   []domain.Turn{user("go"), assistant("", "a"), result("a"), result("a")},
			want:    "user:go | assistant:a | result:a",
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := repairToolPairing(tt.turns, findCall)
			if summary := strings.Join(turnSummary(got), " | "); summary != tt.want {
				t.Fatalf("unexpected history\n got: %s\nwant: %s", summary, tt.want)
			}
			if changed != tt.changed {
				t.Fatalf("expected changed=%v, got %v", tt.changed, changed)
			}
		})
	}
}

func TestRepairToolPairing_KeepsAssistantTextAndThinking(t *testing.T) {
	c := domain.ToolCall{ID: "c", Type: "function", Function: domain.ToolCallFunction{Name: "read_file"}}
	turns := []domain.Turn{
		domain.NewTextTurn(domain.RoleUser, "go"),
		{Role: domain.RoleAssistant, Parts: []domain.Part{
			{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{Text: "plan", State: []byte(`[{"type":"thinking"}]`)}},
			{Kind: domain.PartText, Text: "Looking."},
			{Kind: domain.PartToolCall, ToolCall: &c},
		}},
	}

	got, changed := repairToolPairing(turns, func(string) (domain.ToolCall, bool) { return domain.ToolCall{}, false })

	if !changed || len(got) != 2 {
		t.Fatalf("expected the unanswered call to be removed, got %v", turnSummary(got))
	}
	if got[1].ThinkingText() != "plan" || string(got[1].ThinkingState()) != `[{"type":"thinking"}]` || got[1].Text() != "Looking." {
		t.Fatalf("expected thinking and text to survive the repair, got %+v", got[1])
	}
}
