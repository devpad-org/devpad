package mistral

import (
	"errors"
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
		ExpectedProviderID:   "mistral",
		ExpectedProviderName: "Mistral AI",
		ExpectedProtocol:     aiprovider.ProtocolOpenAIChat,
		ExpectedModelIDs:     []string{"mistral-small-latest", "mistral-medium-3-5", "mistral-large-4-0"},
		Request: aiprovider.StreamRequest{
			Model: "mistral-medium-3-5",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		},
	})
}

func TestAdjustableThinkingMetadata(t *testing.T) {
	models := NewAdapter().Models()
	tests := []struct {
		modelID string
		name    string
	}{
		{modelID: "mistral-small-latest", name: "Mistral Small 4"},
		{modelID: "mistral-medium-3-5", name: "Mistral Medium 3.5"},
	}

	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			model, ok := domain.ModelByID(models, tt.modelID)
			if !ok {
				t.Fatalf("expected %s model", tt.modelID)
			}

			if model.Name != tt.name {
				t.Fatalf("expected model name %q, got %q", tt.name, model.Name)
			}
			if !model.Thinking.Supported {
				t.Fatalf("expected %s to support thinking", tt.name)
			}
			if model.Thinking.EnabledByDefault {
				t.Fatal("expected thinking to be opt-in by default")
			}
			if !model.Thinking.CanDisable {
				t.Fatal("expected thinking to be disableable")
			}
			if model.Thinking.DefaultEffort != defaultReasoningEffort {
				t.Fatalf("expected default effort %q, got %q", defaultReasoningEffort, model.Thinking.DefaultEffort)
			}

			expectedEfforts := []string{"high"}
			if len(model.Thinking.SupportedEfforts) != len(expectedEfforts) {
				t.Fatalf("expected %d supported efforts, got %d", len(expectedEfforts), len(model.Thinking.SupportedEfforts))
			}
			for i, expected := range expectedEfforts {
				if model.Thinking.SupportedEfforts[i] != expected {
					t.Fatalf("expected effort %d to be %q, got %q", i, expected, model.Thinking.SupportedEfforts[i])
				}
			}
		})
	}
}

func TestBuildChatRequest_AdjustableReasoningEffort(t *testing.T) {
	modelIDs := []string{"mistral-small-latest", "mistral-medium-3-5"}

	for _, modelID := range modelIDs {
		model, ok := domain.ModelByID(NewAdapter().Models(), modelID)
		if !ok {
			t.Fatalf("expected %s model", modelID)
		}

		t.Run(modelID, func(t *testing.T) {
			assertAdjustableReasoningEffort(t, model)
		})
	}
}

func assertAdjustableReasoningEffort(t *testing.T, model domain.Model) {
	t.Helper()
	tests := []struct {
		name        string
		thinking    *domain.ThinkingConfig
		wantEffort  string
		wantPresent bool
	}{
		{
			name:        "explicit high effort",
			thinking:    &domain.ThinkingConfig{Enabled: boolPtr(true), Effort: defaultReasoningEffort},
			wantEffort:  defaultReasoningEffort,
			wantPresent: true,
		},
		{
			name:        "enabled with default effort",
			thinking:    &domain.ThinkingConfig{Enabled: boolPtr(true)},
			wantEffort:  defaultReasoningEffort,
			wantPresent: true,
		},
		{
			name:        "effort without explicit enabled turns thinking on",
			thinking:    &domain.ThinkingConfig{Effort: defaultReasoningEffort},
			wantEffort:  defaultReasoningEffort,
			wantPresent: true,
		},
		{
			name:        "disabled",
			thinking:    &domain.ThinkingConfig{Enabled: boolPtr(false)},
			wantEffort:  disabledReasoningEffort,
			wantPresent: true,
		},
		{
			name:        "not requested",
			thinking:    nil,
			wantPresent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := buildChatRequest(aiprovider.StreamRequest{
				Model:    model.ID,
				Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
				Thinking: tt.thinking,
			}, model)

			got, ok := body["reasoning_effort"]
			if ok != tt.wantPresent {
				t.Fatalf("expected reasoning_effort presence %v, got %v with value %v", tt.wantPresent, ok, got)
			}
			if tt.wantPresent && got != tt.wantEffort {
				t.Fatalf("expected reasoning_effort %q, got %v", tt.wantEffort, got)
			}
		})
	}
}

func TestLarge4ThinkingMetadata(t *testing.T) {
	model, ok := domain.ModelByID(NewAdapter().Models(), "mistral-large-4-0")
	if !ok {
		t.Fatal("expected mistral-large-4-0 model")
	}

	if model.Name != "Mistral Large 4" {
		t.Fatalf("expected model name %q, got %q", "Mistral Large 4", model.Name)
	}
	if !model.Thinking.Supported || !model.Thinking.EnabledByDefault {
		t.Fatal("expected Large 4 to reason by default")
	}
	if model.Thinking.CanDisable {
		t.Fatal("expected Large 4 reasoning not to be disableable")
	}
	if model.Thinking.DefaultEffort != "high" {
		t.Fatalf("expected default effort high, got %q", model.Thinking.DefaultEffort)
	}

	expectedEfforts := []string{"low", "medium", "high"}
	if len(model.Thinking.SupportedEfforts) != len(expectedEfforts) {
		t.Fatalf("expected efforts %v, got %v", expectedEfforts, model.Thinking.SupportedEfforts)
	}
	for i, expected := range expectedEfforts {
		if model.Thinking.SupportedEfforts[i] != expected {
			t.Fatalf("expected efforts %v, got %v", expectedEfforts, model.Thinking.SupportedEfforts)
		}
	}
}

func TestBuildChatRequest_Large4ReasoningEffort(t *testing.T) {
	model, ok := domain.ModelByID(NewAdapter().Models(), "mistral-large-4-0")
	if !ok {
		t.Fatal("expected mistral-large-4-0 model")
	}

	tests := []struct {
		name       string
		thinking   *domain.ThinkingConfig
		wantEffort string
	}{
		{name: "not requested uses high", thinking: nil, wantEffort: "high"},
		{name: "enabled with default effort", thinking: &domain.ThinkingConfig{Enabled: boolPtr(true)}, wantEffort: "high"},
		{name: "low", thinking: &domain.ThinkingConfig{Effort: "low"}, wantEffort: "low"},
		{name: "medium", thinking: &domain.ThinkingConfig{Enabled: boolPtr(true), Effort: "medium"}, wantEffort: "medium"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := buildChatRequest(aiprovider.StreamRequest{
				Model:    model.ID,
				Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
				Thinking: tt.thinking,
			}, model)

			if got := body["reasoning_effort"]; got != tt.wantEffort {
				t.Fatalf("expected reasoning_effort %q, got %v", tt.wantEffort, got)
			}
		})
	}
}

func TestValidateThinkingRequest_Large4CannotDisable(t *testing.T) {
	model, _ := domain.ModelByID(NewAdapter().Models(), "mistral-large-4-0")
	err := domain.ValidateThinkingRequest(model, domain.ChatRequest{
		Model:    model.ID,
		Thinking: &domain.ThinkingConfig{Enabled: boolPtr(false)},
	})
	if !errors.Is(err, domain.ErrThinkingCannotBeDisabled) {
		t.Fatalf("expected ErrThinkingCannotBeDisabled, got %v", err)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestAdapterModelsArePriced(t *testing.T) {
	for _, model := range NewAdapter().Models() {
		if model.Pricing == nil || model.Pricing.Input <= 0 || model.Pricing.Output <= 0 {
			t.Fatalf("model %s has no prices", model.ID)
		}
	}
}
