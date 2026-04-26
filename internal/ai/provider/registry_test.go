package provider

import (
	"context"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type stubAdapter struct {
	providerID   string
	providerName string
	models       []domain.Model
}

func (a stubAdapter) ProviderID() string   { return a.providerID }
func (a stubAdapter) ProviderName() string { return a.providerName }
func (a stubAdapter) Protocol() Protocol   { return ProtocolOpenAIChat }
func (a stubAdapter) Models() []domain.Model {
	return a.models
}
func (a stubAdapter) Stream(context.Context, Credentials, StreamRequest) (<-chan domain.ProviderEvent, error) {
	return nil, nil
}

func TestRegistryResolveModel(t *testing.T) {
	registry := NewRegistry(
		stubAdapter{providerID: "mistral", providerName: "Mistral AI", models: []domain.Model{{ID: "mistral-large-latest", ProviderID: "mistral"}}},
		stubAdapter{providerID: "openai", providerName: "OpenAI", models: []domain.Model{{ID: "gpt-5.4", ProviderID: "openai"}}},
	)

	adapter, model, ok := registry.ResolveModel("gpt-5.4")
	if !ok {
		t.Fatal("expected model to resolve")
	}
	if adapter.ProviderID() != "openai" {
		t.Fatalf("expected openai adapter, got %q", adapter.ProviderID())
	}
	if model.ID != "gpt-5.4" {
		t.Fatalf("expected gpt-5.4 model, got %q", model.ID)
	}
}

func TestRegistryByProviderID(t *testing.T) {
	registry := NewRegistry(stubAdapter{providerID: "moonshot", providerName: "Moonshot AI"})

	adapter, ok := registry.ByProviderID("moonshot")
	if !ok {
		t.Fatal("expected provider to resolve")
	}
	if adapter.ProviderName() != "Moonshot AI" {
		t.Fatalf("expected Moonshot AI provider name, got %q", adapter.ProviderName())
	}
}

func TestRegistryAllReturnsCopy(t *testing.T) {
	registry := NewRegistry(stubAdapter{providerID: "minimax", providerName: "MiniMax"})

	all := registry.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 adapter, got %d", len(all))
	}

	all = append(all, stubAdapter{providerID: "extra", providerName: "Extra"})
	if len(registry.All()) != 1 {
		t.Fatal("expected registry adapters to be immutable from callers")
	}
}

func TestRegistrySupportsMultipleAdaptersPerProviderID(t *testing.T) {
	registry := NewRegistry(
		stubAdapter{providerID: "openai", providerName: "OpenAI", models: []domain.Model{{ID: "gpt-5.4", ProviderID: "openai"}}},
		stubAdapter{providerID: "openai", providerName: "OpenAI", models: []domain.Model{{ID: "gpt-5", ProviderID: "openai"}}},
	)

	all := registry.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 adapters, got %d", len(all))
	}

	if _, model, ok := registry.ResolveModel("gpt-5.4"); !ok || model.ID != "gpt-5.4" {
		t.Fatalf("expected gpt-5.4 to resolve, got %+v, ok=%v", model, ok)
	}
	if _, model, ok := registry.ResolveModel("gpt-5"); !ok || model.ID != "gpt-5" {
		t.Fatalf("expected gpt-5 to resolve, got %+v, ok=%v", model, ok)
	}

	adapter, ok := registry.ByProviderID("openai")
	if !ok {
		t.Fatal("expected openai provider to resolve")
	}
	if adapter.ProviderID() != "openai" {
		t.Fatalf("expected openai provider, got %q", adapter.ProviderID())
	}
}
