package tools

import (
	"strings"
	"testing"
)

func TestAgentSystemPromptEncouragesSubAgentFanoutWithoutRateLimitCaveat(t *testing.T) {
	prompt := AgentSystemPrompt

	for _, want := range []string{
		"Use spawn_sub_agent proactively",
		"up to 3 child agents concurrently",
		"call wait_for_sub_agents before synthesizing",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q", want)
		}
	}
	if strings.Contains(prompt, "rate-limited") {
		t.Fatalf("prompt should not discourage sub-agents with stale rate-limit guidance")
	}
}
