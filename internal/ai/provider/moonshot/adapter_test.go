package moonshot

import (
	"net/http"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/testkit"
)

func TestAdapterContract(t *testing.T) {
	testkit.RunAdapterContract(t, testkit.AdapterContract{
		NewAdapter: func() aiprovider.Adapter {
			return NewAdapter()
		},
		Configure: func(adapter aiprovider.Adapter, serverURL string, client *http.Client) {
			tested := adapter.(*Adapter)
			tested.baseURL = serverURL
			tested.client = client
		},
		ExpectedProviderID:   "moonshot",
		ExpectedProviderName: "Moonshot AI",
		ExpectedProtocol:     aiprovider.ProtocolOpenAIChat,
		ExpectedModelIDs:     []string{"kimi-k3.1-pro", "kimi-k3"},
		Request: aiprovider.StreamRequest{
			Model: "kimi-k3",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		},
	})
}

func modelByID(t *testing.T, id string) domain.Model {
	t.Helper()
	model, ok := domain.ModelByID(NewAdapter().Models(), id)
	if !ok {
		t.Fatalf("model %q not found", id)
	}
	return model
}

// k3SeriesModelIDs are the models that always reason and take their effort
// in a top-level reasoning_effort field.
var k3SeriesModelIDs = []string{"kimi-k3.1-pro", "kimi-k3"}

// K3-series models always reason, at the effort in a top-level
// reasoning_effort field.
func TestBuildChatRequest_K3SendsReasoningEffort(t *testing.T) {
	tests := []struct {
		name       string
		thinking   *domain.ThinkingConfig
		wantEffort string
	}{
		{name: "defaults to max", thinking: nil, wantEffort: "max"},
		{name: "honours requested effort", thinking: &domain.ThinkingConfig{Effort: "low"}, wantEffort: "low"},
		{name: "stays enabled when disabling is requested", thinking: &domain.ThinkingConfig{Enabled: boolPtr(false)}, wantEffort: "max"},
	}

	for _, modelID := range k3SeriesModelIDs {
		model := modelByID(t, modelID)
		for _, tt := range tests {
			t.Run(modelID+"/"+tt.name, func(t *testing.T) {
				req := aiprovider.StreamRequest{
					Model:    modelID,
					Thinking: tt.thinking,
					Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Hello")},
				}

				body := buildChatRequest(req, model)

				if body.ReasoningEffort != tt.wantEffort {
					t.Fatalf("expected reasoning_effort %q, got %q", tt.wantEffort, body.ReasoningEffort)
				}
			})
		}
	}
}

// K3-series models require their reasoning history back on every turn, even
// when a caller asks to turn thinking off.
func TestBuildChatRequest_K3PreservesReasoningWhenThinkingDisableRequested(t *testing.T) {
	for _, modelID := range k3SeriesModelIDs {
		t.Run(modelID, func(t *testing.T) {
			assertPreservesReasoningWhenThinkingDisableRequested(t, modelByID(t, modelID))
		})
	}
}

func assertPreservesReasoningWhenThinkingDisableRequested(t *testing.T, model domain.Model) {
	t.Helper()
	req := aiprovider.StreamRequest{
		Model:    model.ID,
		Thinking: &domain.ThinkingConfig{Enabled: boolPtr(false)},
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "Inspect the repo"),
			{
				Role: domain.RoleAssistant,
				Parts: []domain.Part{
					{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{Text: "Check README first."}},
					{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{
						ID:   "call_1",
						Type: "function",
						Function: domain.ToolCallFunction{
							Name:      "read_file",
							Arguments: `{"path":"README.md"}`,
						},
					}},
				},
			},
		},
	}

	body := buildChatRequest(req, model)

	if len(body.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(body.Messages))
	}
	if body.Messages[1].ReasoningContent == nil {
		t.Fatal("expected assistant message to keep reasoning_content")
	}
	if *body.Messages[1].ReasoningContent != "Check README first." {
		t.Fatalf("expected preserved reasoning_content, got %q", *body.Messages[1].ReasoningContent)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestBuildChatRequest_AsksForStreamUsage(t *testing.T) {
	model, _ := domain.ModelByID(NewAdapter().Models(), "kimi-k3")
	body := buildChatRequest(aiprovider.StreamRequest{
		Model: "kimi-k3",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	}, model)

	if body.StreamOptions == nil || !body.StreamOptions.IncludeUsage {
		t.Fatalf("expected stream_options.include_usage, got %+v", body.StreamOptions)
	}
}

// Prices from https://platform.kimi.ai/docs/pricing/chat (November 2026).
func TestAdapterModelPrices(t *testing.T) {
	tests := map[string]domain.TokenPrices{
		"kimi-k3.1-pro": {Input: 2, Output: 10, CacheRead: 0.20, CacheWrite: 2},
		"kimi-k3":       {Input: 3, Output: 15, CacheRead: 0.30, CacheWrite: 3},
	}
	for id, want := range tests {
		model := modelByID(t, id)
		if model.Pricing == nil || model.Pricing.TokenPrices != want {
			t.Fatalf("model %s prices = %+v, want %+v", id, model.Pricing, want)
		}
	}
}

func TestAdapterModelsArePriced(t *testing.T) {
	for _, model := range NewAdapter().Models() {
		if model.Pricing == nil || model.Pricing.Input <= 0 || model.Pricing.Output <= 0 {
			t.Fatalf("model %s has no prices", model.ID)
		}
	}
}
