package mistral

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
		ExpectedProviderID:   "mistral",
		ExpectedProviderName: "Mistral AI",
		ExpectedProtocol:     aiprovider.ProtocolOpenAIChat,
		ExpectedModelIDs:     []string{"devstral-medium-latest", "mistral-medium-3-5", "mistral-large-latest"},
		Request: aiprovider.StreamRequest{
			Model: "devstral-medium-latest",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		},
	})
}

func TestMistralMediumThinkingMetadata(t *testing.T) {
	models := NewAdapter().Models()
	model, ok := domain.ModelByID(models, "mistral-medium-3-5")
	if !ok {
		t.Fatal("expected mistral-medium-3-5 model")
	}

	if model.Name != "Mistral Medium 3.5" {
		t.Fatalf("expected model name Mistral Medium 3.5, got %q", model.Name)
	}
	if !model.Thinking.Supported {
		t.Fatal("expected Mistral Medium 3.5 to support thinking")
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
}

func TestBuildChatRequest_MistralMediumReasoningEffort(t *testing.T) {
	model, ok := domain.ModelByID(NewAdapter().Models(), "mistral-medium-3-5")
	if !ok {
		t.Fatal("expected mistral-medium-3-5 model")
	}

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
				Model:    "mistral-medium-3-5",
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

func TestBuildChatRequest_OmitsReasoningEffortForNonThinkingMistralModel(t *testing.T) {
	model, ok := domain.ModelByID(NewAdapter().Models(), "mistral-large-latest")
	if !ok {
		t.Fatal("expected mistral-large-latest model")
	}

	body := buildChatRequest(aiprovider.StreamRequest{
		Model: "mistral-large-latest",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	}, model)

	if _, ok := body["reasoning_effort"]; ok {
		t.Fatalf("expected reasoning_effort to be omitted for non-thinking model, got %v", body["reasoning_effort"])
	}
}

func boolPtr(v bool) *bool { return &v }
