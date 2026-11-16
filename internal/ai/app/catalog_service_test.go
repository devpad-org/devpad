package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/encrypt"
)

// memoryProviderKeys keeps provider keys by user, then provider.
type memoryProviderKeys map[int64]map[string]string

func (r memoryProviderKeys) GetKey(_ context.Context, userID int64, providerID string) (string, error) {
	return r[userID][providerID], nil
}

func (r memoryProviderKeys) ListKeys(_ context.Context, userID int64) (map[string]string, error) {
	keys := make(map[string]string, len(r[userID]))
	for id, key := range r[userID] {
		keys[id] = key
	}
	return keys, nil
}

func (r memoryProviderKeys) SetKey(_ context.Context, userID int64, providerID, key string) error {
	if r[userID] == nil {
		r[userID] = map[string]string{}
	}
	r[userID][providerID] = key
	return nil
}

func (r memoryProviderKeys) DeleteKey(_ context.Context, userID int64, providerID string) error {
	delete(r[userID], providerID)
	return nil
}

func newTestCipher(t *testing.T, hexByte string) *encrypt.Cipher {
	t.Helper()
	c, err := encrypt.NewCipher(strings.Repeat(hexByte, 64))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	return c
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
	cipher := newTestCipher(t, "a")
	service := NewCatalogService(memoryProviderKeys{}, cipher,
		aiprovider.NewRegistry(
			stubCatalogAdapter{providerID: "openai", providerName: "OpenAI", models: []domain.Model{{ID: "gpt-5.4", ProviderID: "openai"}}},
			stubCatalogAdapter{providerID: "openai", providerName: "OpenAI", models: []domain.Model{{ID: "gpt-5", ProviderID: "openai"}}},
		),
	)
	ctx := context.Background()
	if err := service.SetProviderKey(ctx, 1, "openai", " key "); err != nil {
		t.Fatalf("set key: %v", err)
	}

	providers, err := service.ListProviders(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error listing providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].ID != "openai" || !providers[0].HasAPIKey || providers[0].KeyUnreadable {
		t.Fatalf("expected openai with a key, got %+v", providers[0])
	}

	models, err := service.ListModels(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error listing models: %v", err)
	}
	if len(models) != 2 || !models[0].Configured || !models[1].Configured {
		t.Fatalf("expected both models configured, got %+v", models)
	}
}

func newTwoProviderCatalog(t *testing.T, keys memoryProviderKeys, cipher KeyCipher) CatalogService {
	t.Helper()
	price := func(in, out float64) *domain.Pricing {
		return &domain.Pricing{TokenPrices: domain.TokenPrices{Input: in, Output: out}}
	}
	return NewCatalogService(keys, cipher, aiprovider.NewRegistry(
		stubCatalogAdapter{providerID: "anthropic", providerName: "Anthropic", models: []domain.Model{
			{ID: "big", ProviderID: "anthropic", Pricing: price(5, 25)},
			{ID: "small", ProviderID: "anthropic", Pricing: price(1, 5)},
			{ID: "unpriced", ProviderID: "anthropic"},
		}},
		stubCatalogAdapter{providerID: "mistral", providerName: "Mistral", models: []domain.Model{
			{ID: "tiny", ProviderID: "mistral", Pricing: price(0.1, 0.3)},
		}},
	))
}

// A request is sent with the key of the user it is for, never another
// user's, and the key is stored sealed.
func TestResolveChatTargetUsesOnlyTheUsersOwnKey(t *testing.T) {
	keys := memoryProviderKeys{}
	service := newTwoProviderCatalog(t, keys, newTestCipher(t, "a"))
	ctx := context.Background()
	if err := service.SetProviderKey(ctx, 1, "anthropic", "alice-key"); err != nil {
		t.Fatalf("set alice's key: %v", err)
	}
	if err := service.SetProviderKey(ctx, 2, "anthropic", "bob-key"); err != nil {
		t.Fatalf("set bob's key: %v", err)
	}
	if keys[1]["anthropic"] == "alice-key" || keys[1]["anthropic"] == "" {
		t.Fatalf("expected the key stored encrypted, got %q", keys[1]["anthropic"])
	}

	for userID, want := range map[int64]string{1: "alice-key", 2: "bob-key"} {
		target, err := service.ResolveChatTarget(ctx, userID, "big")
		if err != nil {
			t.Fatalf("user %d: %v", userID, err)
		}
		if target.Creds.APIKey != want {
			t.Fatalf("user %d: expected %q, got %q", userID, want, target.Creds.APIKey)
		}
	}
	if _, err := service.ResolveChatTarget(ctx, 3, "big"); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected a user without a key refused, got %v", err)
	}
	if _, err := service.ResolveChatTarget(ctx, 1, "tiny"); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected a provider without the user's key refused, got %v", err)
	}

	if err := service.RemoveProviderKey(ctx, 1, "anthropic"); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	if _, err := service.ResolveChatTarget(ctx, 1, "big"); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected a removed key refused, got %v", err)
	}
	if err := service.SetProviderKey(ctx, 1, "nope", "k"); !errors.Is(err, domain.ErrProviderNotFound) {
		t.Fatalf("expected an unknown provider refused, got %v", err)
	}
	if err := service.SetProviderKey(ctx, 1, "anthropic", "  "); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected an empty key refused, got %v", err)
	}
}

// A key sealed with an encryption key the server no longer has, such as a
// generated one before a restart, is reported so the user can enter it
// again.
func TestResolveChatTargetReportsAnUnreadableKey(t *testing.T) {
	keys := memoryProviderKeys{}
	if err := newTwoProviderCatalog(t, keys, newTestCipher(t, "a")).SetProviderKey(context.Background(), 1, "anthropic", "k"); err != nil {
		t.Fatalf("set key: %v", err)
	}
	service := newTwoProviderCatalog(t, keys, newTestCipher(t, "b"))

	if _, err := service.ResolveChatTarget(context.Background(), 1, "big"); !errors.Is(err, domain.ErrAPIKeyUnreadable) {
		t.Fatalf("expected an unreadable key, got %v", err)
	}
	providers, err := service.ListProviders(context.Background(), 1)
	if err != nil {
		t.Fatalf("list providers: %v", err)
	}
	if p := providers[0]; p.ID != "anthropic" || p.HasAPIKey || !p.KeyUnreadable {
		t.Fatalf("expected anthropic's key reported unreadable, got %+v", p)
	}
	if _, err := service.CheapestConfiguredModel(context.Background(), 1); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected an unreadable key to configure nothing, got %v", err)
	}
}

func TestCheapestConfiguredModelPicksAmongTheUsersProviders(t *testing.T) {
	service := newTwoProviderCatalog(t, memoryProviderKeys{}, newTestCipher(t, "a"))
	ctx := context.Background()
	if _, err := service.CheapestConfiguredModel(ctx, 1); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected no model without keys, got %v", err)
	}
	if err := service.SetProviderKey(ctx, 1, "anthropic", "k"); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if got, err := service.CheapestConfiguredModel(ctx, 1); err != nil || got.ID != "small" {
		t.Fatalf("expected small, the cheapest priced anthropic model, got %+v %v", got, err)
	}
	if err := service.SetProviderKey(ctx, 1, "mistral", "k"); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if got, err := service.CheapestConfiguredModel(ctx, 1); err != nil || got.ID != "tiny" {
		t.Fatalf("expected tiny once mistral has a key, got %+v %v", got, err)
	}
	if got, err := service.CheapestConfiguredModel(ctx, 2); !errors.Is(err, domain.ErrNoAPIKey) {
		t.Fatalf("expected another user to have no model, got %+v %v", got, err)
	}
}
