package task

import (
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// maxCarriedBytes bounds the text of earlier rounds, after their opening
// prompt, that a new round of a task's agent starts with.
const maxCarriedBytes = 32 * 1024

// carryOver turns the conversation of a task's earlier rounds into the
// start of a new one: the user and assistant text only, without the tool
// calls, tool results and thinking that make up most of a round. The
// work those produced is on the task branch, where the agent can read it
// again. It always keeps the opening prompt, which states the task, and
// when the rest is longer than budget bytes it keeps the latest messages
// that fit, clipping the newest rather than dropping it, and marks what was
// left out. Consecutive messages from one side are joined, so the result
// alternates between user and assistant.
func carryOver(turns []domain.Turn, budget int) []domain.Turn {
	var msgs []domain.Turn
	for _, turn := range turns {
		if turn.Role != domain.RoleUser && turn.Role != domain.RoleAssistant {
			continue
		}
		for _, part := range turn.Parts {
			if part.Kind == domain.PartText {
				if text := strings.TrimSpace(part.Text); text != "" {
					msgs = appendText(msgs, turn.Role, text)
				}
			}
		}
	}
	if len(msgs) <= 1 {
		return msgs
	}

	kept := 0
	used := 0
	for i := len(msgs) - 1; i > 0; i-- {
		text := msgs[i].Text()
		if used+len(text) <= budget {
			used += len(text)
			kept++
			continue
		}
		// The newest message is the most relevant: clip it rather than
		// leave it out.
		if kept == 0 && budget > len(clipMarker) {
			msgs[i] = domain.NewTextTurn(msgs[i].Role, clip(text, budget))
			kept++
		}
		break
	}

	out := []domain.Turn{msgs[0]}
	if omitted := len(msgs) - 1 - kept; omitted > 0 {
		out = appendText(out, domain.RoleUser, fmt.Sprintf("[%d earlier %s left out]", omitted, plural(omitted, "message", "messages")))
	}
	for _, msg := range msgs[len(msgs)-kept:] {
		out = appendText(out, msg.Role, msg.Text())
	}
	return out
}

// appendText adds text as a message from role, joining it to the last
// message when that is from the same side.
func appendText(msgs []domain.Turn, role domain.Role, text string) []domain.Turn {
	if last := len(msgs) - 1; last >= 0 && msgs[last].Role == role {
		msgs[last] = domain.NewTextTurn(role, msgs[last].Text()+"\n\n"+text)
		return msgs
	}
	return append(msgs, domain.NewTextTurn(role, text))
}
