package app

import (
	"context"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

type stubProviderConfigRepo struct {
	configs []ProviderConfig
}

func (r stubProviderConfigRepo) GetConfig(_ context.Context, providerID string) (*ProviderConfig, error) {
	for i := range r.configs {
		if r.configs[i].ID == providerID {
			cfg := r.configs[i]
			return &cfg, nil
		}
	}

	return nil, nil
}

func (r stubProviderConfigRepo) ListConfigs(context.Context) ([]ProviderConfig, error) {
	return append([]ProviderConfig(nil), r.configs...), nil
}

func (r stubProviderConfigRepo) UpsertConfig(context.Context, *ProviderConfig) error {
	return nil
}

type stubCatalogAdapter struct {
	providerID   string
	providerName string
	models       []domain.Model
}

func (a stubCatalogAdapter) ProviderID() string   { return a.providerID }
func (a stubCatalogAdapter) ProviderName() string { return a.providerName }
func (a stubCatalogAdapter) Protocol() aiprovider.Protocol {
	return aiprovider.ProtocolOpenAIChat
}
func (a stubCatalogAdapter) Models() []domain.Model { return a.models }
func (a stubCatalogAdapter) Stream(context.Context, aiprovider.Credentials, aiprovider.StreamRequest) (<-chan domain.ProviderEvent, error) {
	return nil, nil
}

func TestListProvidersDedupesSharedProviderID(t *testing.T) {
	service := NewCatalogService(
		stubProviderConfigRepo{configs: []ProviderConfig{{ID: "openai", Enabled: true, APIKey: "key"}}},
		aiprovider.NewRegistry(
			stubCatalogAdapter{providerID: "openai", providerName: "OpenAI", models: []domain.Model{{ID: "gpt-5.4", ProviderID: "openai"}}},
			stubCatalogAdapter{providerID: "openai", providerName: "OpenAI", models: []domain.Model{{ID: "gpt-5", ProviderID: "openai"}}},
		),
	)

	providers, err := service.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("unexpected error listing providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].ID != "openai" {
		t.Fatalf("expected openai provider, got %q", providers[0].ID)
	}
	if !providers[0].Enabled || !providers[0].HasAPIKey {
		t.Fatalf("expected configured provider info, got %+v", providers[0])
	}
}

func TestListModelsIncludesAllAdaptersForConfiguredProvider(t *testing.T) {
	service := NewCatalogService(
		stubProviderConfigRepo{configs: []ProviderConfig{{ID: "openai", Enabled: true, APIKey: "key"}}},
		aiprovider.NewRegistry(
			stubCatalogAdapter{providerID: "openai", providerName: "OpenAI", models: []domain.Model{{ID: "gpt-5.4", ProviderID: "openai"}}},
			stubCatalogAdapter{providerID: "openai", providerName: "OpenAI", models: []domain.Model{{ID: "gpt-5", ProviderID: "openai"}}},
		),
	)

	models, err := service.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error listing models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if !models[0].Configured || !models[1].Configured {
		t.Fatalf("expected both models to be configured, got %+v", models)
	}
}
