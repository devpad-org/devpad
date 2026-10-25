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
// thinking. Each message sits in its own fenced block, so text inside one,
// which can echo files or tool output, cannot pass itself off as another
// speaker. When it is longer than budget bytes it keeps the opening
// message, which usually states the goal, and as many of the latest
// messages as fit, clipping the newest rather than dropping it, and marks
// what was left out.
func HandoffTranscript(turns []domain.Turn, budget int) string {
	var entries []handoffEntry
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
		entries = append(entries, handoffEntry{label: label, text: strings.Join(texts, "\n\n")})
	}
	if len(entries) == 0 || budget <= 0 {
		return ""
	}

	const sep = "\n\n"
	rendered := make([]string, len(entries))
	total := 0
	for i, e := range entries {
		rendered[i] = e.render()
		total += len(rendered[i]) + len(sep)
	}
	if total-len(sep) <= budget {
		return strings.Join(rendered, sep)
	}
	if len(entries) == 1 {
		return entries[0].renderWithin(budget)
	}

	// Room for the separators around the omission marker and the marker.
	const reserve = len(sep)*2 + 64
	// The opening message gets a quarter of the budget, or more when the
	// rest of the conversation leaves room for it.
	rest := total - len(rendered[0]) - len(sep)
	firstLimit := max(budget/4, budget-rest-reserve)
	first := entries[0].renderWithin(firstLimit)
	used := len(first)
	var tail []string
	for i := len(entries) - 1; i > 0; i-- {
		avail := budget - used - len(sep) - reserve
		if len(rendered[i]) <= avail {
			tail = append(tail, rendered[i])
			used += len(rendered[i]) + len(sep)
			continue
		}
		// The newest message is the most relevant: clip it rather than
		// leave it out.
		if len(tail) == 0 {
			if clipped := entries[i].renderWithin(avail); clipped != "" {
				tail = append(tail, clipped)
			}
		}
		break
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

type handoffEntry struct {
	label string
	text  string
}

// render labels the message and quotes it in a fence longer than any run
// of backticks inside it, so the text cannot close the fence early.
func (e handoffEntry) render() string {
	fence := strings.Repeat("`", max(3, longestRun(e.text, '`')+1))
	return fmt.Sprintf("**%s:**\n%stext\n%s\n%s", e.label, fence, e.text, fence)
}

// renderWithin renders the message in at most n bytes, clipping its text,
// or returns "" when not even a clipped message fits.
func (e handoffEntry) renderWithin(n int) string {
	full := e.render()
	if len(full) <= n {
		return full
	}
	overhead := len(full) - len(e.text)
	room := n - overhead
	if room <= len(clipMarker) {
		return ""
	}
	return handoffEntry{label: e.label, text: clip(e.text, room)}.render()
}

func longestRun(s string, r byte) int {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == r {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// clip shortens s to at most n bytes, on a rune boundary, marking the cut.
const clipMarker = " […]"

func clip(s string, n int) string {
	const marker = clipMarker
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
