package task

import (
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestCarryOverKeepsOnlyTheText(t *testing.T) {
	turns := []domain.Turn{
		domain.NewTextTurn(domain.RoleUser, "Implement the cart."),
		{Role: domain.RoleAssistant, Parts: []domain.Part{
			{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{Text: "secret reasoning"}},
			{Kind: domain.PartText, Text: "Reading the code first."},
			{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{ID: "1"}},
		}},
		domain.NewToolResultTurn("1", "read_file", "raw file contents", false),
		{Role: domain.RoleAssistant, Parts: []domain.Part{{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{ID: "2"}}}},
		domain.NewToolResultTurn("2", "run_command", "test output", false),
		domain.NewTextTurn(domain.RoleAssistant, "Added the cart."),
		{Role: domain.RoleUser, Parts: []domain.Part{
			{Kind: domain.PartToolResult, ToolResult: &domain.ToolResultPart{ToolCallID: "3", Content: "more output"}},
			{Kind: domain.PartText, Text: "Feedback on your work: rename it."},
		}},
		domain.NewTextTurn(domain.RoleAssistant, "Renamed."),
	}
	got := carryOver(turns, maxCarriedBytes)
	want := []struct {
		role domain.Role
		text string
	}{
		{domain.RoleUser, "Implement the cart."},
		{domain.RoleAssistant, "Reading the code first.\n\nAdded the cart."},
		{domain.RoleUser, "Feedback on your work: rename it."},
		{domain.RoleAssistant, "Renamed."},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d turns, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Role != w.role || len(got[i].Parts) != 1 || got[i].Text() != w.text {
			t.Fatalf("turn %d: got %s %+v, want %s %q", i, got[i].Role, got[i].Parts, w.role, w.text)
		}
	}
}

func TestCarryOverKeepsTheTaskAndLatestMessagesWhenLong(t *testing.T) {
	task := strings.Repeat("the task. ", 1000) + "Done."
	turns := []domain.Turn{domain.NewTextTurn(domain.RoleUser, task)}
	for i := 0; i < 50; i++ {
		turns = append(turns,
			domain.NewTextTurn(domain.RoleAssistant, strings.Repeat("step ", 40)),
			domain.NewTextTurn(domain.RoleUser, strings.Repeat("feedback ", 20)),
		)
	}
	turns = append(turns, domain.NewTextTurn(domain.RoleAssistant, "LATEST summary"))
	got := carryOver(turns, 2000)

	if got[0].Role != domain.RoleUser || !strings.HasPrefix(got[0].Text(), task) {
		t.Fatal("expected the opening prompt kept whole")
	}
	if !strings.Contains(got[0].Text(), "earlier messages left out]") {
		t.Fatalf("expected an omission marker joined to the opening prompt, got %q", got[0].Text()[len(task):])
	}
	if last := got[len(got)-1]; last.Role != domain.RoleAssistant || last.Text() != "LATEST summary" {
		t.Fatalf("expected the latest message last, got %+v", last)
	}
	rest := 0
	for i, turn := range got {
		if i > 0 {
			rest += len(turn.Text())
			if turn.Role == got[i-1].Role {
				t.Fatalf("turns %d and %d are both from %s", i-1, i, turn.Role)
			}
		}
	}
	if rest > 2000 {
		t.Fatalf("carried %d bytes after the opening prompt, over the budget", rest)
	}

	huge := []domain.Turn{domain.NewTextTurn(domain.RoleUser, "task"), domain.NewTextTurn(domain.RoleAssistant, strings.Repeat("é", 5000))}
	got = carryOver(huge, 1000)
	if len(got) != 2 || len(got[1].Text()) > 1000 || !strings.HasSuffix(got[1].Text(), clipMarker) {
		t.Fatalf("expected the newest message clipped to the budget, got %+v", got)
	}
}
