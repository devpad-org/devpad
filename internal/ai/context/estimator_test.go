package context

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestEstimateText_UsesConservativeFallback(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "empty", text: "", want: 0},
		{name: "single token", text: "abc", want: 1},
		{name: "ceil partial token", text: "abcd", want: 2},
		{name: "unicode chars", text: "界a", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EstimateText("unknown", "unknown", tt.text); got != tt.want {
				t.Fatalf("EstimateText() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEstimator_AccountsForRepresentativeAgentTranscript(t *testing.T) {
	enabled := true
	req := Request{
		ProviderID: "anthropic",
		ModelID:    "claude-sonnet-4-6",
		Thinking:   &domain.ThinkingConfig{Enabled: &enabled, Effort: "medium"},
		Tools: []domain.ToolDefinition{{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "read_file",
				Description: "Read the contents of a file.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			},
		}},
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleSystem, "You are an expert coding agent.\n\nSelected agent instructions:\nKeep changes focused."),
			domain.NewTextTurn(domain.RoleUser, "Review the repository and fix the build."),
			{
				Role: domain.RoleAssistant,
				Parts: []domain.Part{
					{
						Kind: domain.PartThinking,
						Thinking: &domain.ThinkingPart{
							Text:  "I need to inspect the relevant files.",
							State: json.RawMessage(`[{"type":"thinking","signature":"sig_123","thinking":"state"}]`),
						},
					},
					{
						Kind: domain.PartToolCall,
						ToolCall: &domain.ToolCall{
							ID:   "toolu_1",
							Type: "function",
							Function: domain.ToolCallFunction{
								Name:      "read_file",
								Arguments: `{"path":"frontend/src/App.vue"}`,
							},
						},
					},
				},
			},
			domain.NewToolResultTurn("toolu_1", "read_file", "1. <template>\n2.   <main>Devpad</main>\n3. </template>", false),
			domain.NewTextTurn(domain.RoleAssistant, "The build issue is in App.vue."),
		},
	}

	estimate := EstimateRequest(req)

	if estimate.ProviderID != req.ProviderID || estimate.ModelID != req.ModelID {
		t.Fatalf("expected provider/model to be preserved, got %q/%q", estimate.ProviderID, estimate.ModelID)
	}
	if len(estimate.Turns) != len(req.Turns) {
		t.Fatalf("expected %d turn estimates, got %d", len(req.Turns), len(estimate.Turns))
	}
	if len(estimate.Tools) != len(req.Tools) {
		t.Fatalf("expected %d tool estimates, got %d", len(req.Tools), len(estimate.Tools))
	}

	assertPositive(t, "system tokens", estimate.SystemTokens)
	assertPositive(t, "conversation tokens", estimate.ConversationTokens)
	assertPositive(t, "tool schema tokens", estimate.ToolSchemaTokens)
	assertPositive(t, "thinking tokens", estimate.ThinkingTokens)
	assertPositive(t, "tool call argument tokens", estimate.ToolCallArgumentTokens)
	assertPositive(t, "tool result tokens", estimate.ToolResultTokens)
	assertPositive(t, "overhead tokens", estimate.OverheadTokens)

	wantTotal := estimate.SystemTokens +
		estimate.ConversationTokens +
		estimate.ToolSchemaTokens +
		estimate.ThinkingTokens +
		estimate.ToolCallArgumentTokens +
		estimate.ToolResultTokens +
		estimate.OverheadTokens
	if estimate.TotalTokens != wantTotal {
		t.Fatalf("TotalTokens = %d, want component sum %d", estimate.TotalTokens, wantTotal)
	}
}

func TestEstimateChatRequest_UsesResolvedModelProvider(t *testing.T) {
	estimate := EstimateChatRequest(domain.Model{
		ID:         "claude-sonnet-4-6",
		ProviderID: "anthropic",
	}, domain.ChatRequest{
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Hello")},
	})

	if estimate.ProviderID != "anthropic" {
		t.Fatalf("ProviderID = %q, want anthropic", estimate.ProviderID)
	}
	if estimate.ModelID != "claude-sonnet-4-6" {
		t.Fatalf("ModelID = %q, want claude-sonnet-4-6", estimate.ModelID)
	}
	assertPositive(t, "total tokens", estimate.TotalTokens)
}

func TestBudgetFor_ReturnsProviderModelDefaults(t *testing.T) {
	tests := []struct {
		name       string
		providerID string
		modelID    string
		wantBudget int
	}{
		{name: "anthropic opus long context", providerID: "anthropic", modelID: "claude-opus-4-7", wantBudget: 1000000},
		{name: "anthropic sonnet long context", providerID: "anthropic", modelID: "claude-sonnet-4-6", wantBudget: 1000000},
		{name: "anthropic haiku context", providerID: "anthropic", modelID: "claude-haiku-4-5", wantBudget: 200000},
		{name: "openai gpt 5.4 context", providerID: "openai", modelID: "gpt-5.4", wantBudget: 1050000},
		{name: "openai gpt 5.5 context", providerID: "openai", modelID: "gpt-5.5", wantBudget: 1050000},
		{name: "mistral devstral context", providerID: "mistral", modelID: "devstral-medium-latest", wantBudget: 256000},
		{name: "mistral small context", providerID: "mistral", modelID: "mistral-small-latest", wantBudget: 256000},
		{name: "mistral older small context", providerID: "mistral", modelID: "mistral-small-2506", wantBudget: 128000},
		{name: "mistral medium context", providerID: "mistral", modelID: "mistral-medium-3-5", wantBudget: 256000},
		{name: "mistral large context", providerID: "mistral", modelID: "mistral-large-latest", wantBudget: 256000},
		{name: "moonshot kimi context", providerID: "moonshot", modelID: "kimi-k2.6", wantBudget: 256000},
		{name: "minimax m3 context", providerID: "minimax", modelID: "MiniMax-M3", wantBudget: 1000000},
		{name: "minimax m2.7 context", providerID: "minimax", modelID: "MiniMax-M2.7", wantBudget: 204800},
		{name: "unknown conservative fallback", providerID: "unknown", modelID: "custom", wantBudget: 25000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budget := BudgetFor(tt.providerID, tt.modelID)
			if budget.InputTokens != tt.wantBudget {
				t.Fatalf("InputTokens = %d, want %d", budget.InputTokens, tt.wantBudget)
			}
			if budget.WarningThresholdPercentage != 80 {
				t.Fatalf("WarningThresholdPercentage = %d, want 80", budget.WarningThresholdPercentage)
			}
		})
	}
}

func TestPercentageUsed(t *testing.T) {
	if got := PercentageUsed(200, 1000); got != 20 {
		t.Fatalf("PercentageUsed() = %v, want 20", got)
	}
	if got := PercentageUsed(200, 0); got != 0 {
		t.Fatalf("PercentageUsed() with zero budget = %v, want 0", got)
	}
}

func TestEstimator_ToolResultContentDrivesBroadRunGrowth(t *testing.T) {
	baseTurns := []domain.Turn{
		domain.NewTextTurn(domain.RoleSystem, "You are a coding agent."),
		domain.NewTextTurn(domain.RoleUser, "Inspect these files."),
		assistantToolCallTurn("call_1", "read_file", `{"path":"internal/ai/orchestrator/agent_chat.go"}`),
	}
	small := EstimateRequest(Request{
		ProviderID: "openai",
		ModelID:    "gpt-5.5",
		Turns: append(cloneTurns(baseTurns),
			domain.NewToolResultTurn("call_1", "read_file", "package orchestrator\n", false),
		),
	})
	largeResult := strings.Repeat("func example() { return }\n", 400)
	large := EstimateRequest(Request{
		ProviderID: "openai",
		ModelID:    "gpt-5.5",
		Turns: append(cloneTurns(baseTurns),
			domain.NewToolResultTurn("call_1", "read_file", largeResult, false),
		),
	})

	if large.ToolResultTokens <= small.ToolResultTokens {
		t.Fatalf("large tool result tokens = %d, small = %d", large.ToolResultTokens, small.ToolResultTokens)
	}
	if large.TotalTokens <= small.TotalTokens {
		t.Fatalf("large total tokens = %d, small = %d", large.TotalTokens, small.TotalTokens)
	}

	expectedDelta := EstimateText("openai", "gpt-5.5", largeResult) - EstimateText("openai", "gpt-5.5", "package orchestrator\n")
	if gotDelta := large.ToolResultTokens - small.ToolResultTokens; gotDelta != expectedDelta {
		t.Fatalf("tool result token delta = %d, want %d", gotDelta, expectedDelta)
	}
}

func TestEstimator_ThinkingStateBlocksAreCounted(t *testing.T) {
	withoutThinking := EstimateRequest(Request{
		ProviderID: "anthropic",
		ModelID:    "claude-haiku-4-5",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "Continue."),
			domain.NewTextTurn(domain.RoleAssistant, "Done."),
		},
	})
	withThinking := EstimateRequest(Request{
		ProviderID: "anthropic",
		ModelID:    "claude-haiku-4-5",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "Continue."),
			{
				Role: domain.RoleAssistant,
				Parts: []domain.Part{
					{
						Kind: domain.PartThinking,
						Thinking: &domain.ThinkingPart{
							Text:  "Reasoning summary.",
							State: json.RawMessage(`[{"type":"thinking","signature":"sig","thinking":"opaque provider continuation state"}]`),
						},
					},
					{Kind: domain.PartText, Text: "Done."},
				},
			},
		},
	})

	if withThinking.ThinkingTokens == 0 {
		t.Fatal("expected thinking tokens to be counted")
	}
	if withThinking.TotalTokens <= withoutThinking.TotalTokens {
		t.Fatalf("with thinking total = %d, without = %d", withThinking.TotalTokens, withoutThinking.TotalTokens)
	}
}

func assertPositive(t *testing.T, name string, got int) {
	t.Helper()
	if got <= 0 {
		t.Fatalf("expected %s to be positive, got %d", name, got)
	}
}

func assistantToolCallTurn(id, name, args string) domain.Turn {
	return domain.Turn{
		Role: domain.RoleAssistant,
		Parts: []domain.Part{{
			Kind: domain.PartToolCall,
			ToolCall: &domain.ToolCall{
				ID:   id,
				Type: "function",
				Function: domain.ToolCallFunction{
					Name:      name,
					Arguments: args,
				},
			},
		}},
	}
}

func cloneTurns(turns []domain.Turn) []domain.Turn {
	return append([]domain.Turn(nil), turns...)
}
