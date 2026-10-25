package task

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// maxHandoffBytes bounds the conversation text a handed-off task carries, so
// it informs the agent's first turn without crowding out its own work.
const maxHandoffBytes = 32 * 1024

// HandoffTranscript renders a conversation's user and assistant text as
// Markdown for a task agent, leaving out tool calls, tool results and
// thinking. When it is longer than budget bytes it keeps the opening
// message, which usually states the goal, and as many of the latest
// messages as fit, marking what was left out.
func HandoffTranscript(turns []domain.Turn, budget int) string {
	var entries []string
	for _, turn := range turns {
		var label string
		switch turn.Role {
		case domain.RoleUser:
			label = "User"
		case domain.RoleAssistant:
			label = "Assistant"
		default:
			continue
		}
		var texts []string
		for _, part := range turn.Parts {
			if part.Kind == domain.PartText {
				if text := strings.TrimSpace(part.Text); text != "" {
					texts = append(texts, text)
				}
			}
		}
		if len(texts) == 0 {
			continue
		}
		entries = append(entries, fmt.Sprintf("**%s:** %s", label, strings.Join(texts, "\n\n")))
	}
	if len(entries) == 0 || budget <= 0 {
		return ""
	}

	const sep = "\n\n"
	whole := strings.Join(entries, sep)
	if len(whole) <= budget {
		return whole
	}

	// One message may be larger than the whole budget, so each kept message
	// is capped at half of it.
	limit := budget / 2
	first := clip(entries[0], limit)
	used := len(first)
	var tail []string
	for i := len(entries) - 1; i > 0; i-- {
		entry := clip(entries[i], limit)
		// Leave room for the separators and the omission marker.
		if used+len(entry)+len(sep)*2+64 > budget {
			break
		}
		tail = append(tail, entry)
		used += len(entry) + len(sep)
	}
	omitted := len(entries) - 1 - len(tail)
	parts := []string{first}
	if omitted > 0 {
		parts = append(parts, fmt.Sprintf("[%d earlier %s left out]", omitted, plural(omitted, "message", "messages")))
	}
	for i := len(tail) - 1; i >= 0; i-- {
		parts = append(parts, tail[i])
	}
	return strings.Join(parts, sep)
}

// clip shortens s to at most n bytes, on a rune boundary, marking the cut.
func clip(s string, n int) string {
	const marker = " […]"
	if len(s) <= n {
		return s
	}
	cut := n - len(marker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + marker
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
