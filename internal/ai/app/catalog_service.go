package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

// ProviderKeyRepository persists each user's provider keys, which it only
// ever sees encrypted.
type ProviderKeyRepository interface {
	// GetKey returns "" when the user has no key for the provider.
	GetKey(ctx context.Context, userID int64, providerID string) (string, error)
	// ListKeys returns the user's keys by provider ID.
	ListKeys(ctx context.Context, userID int64) (map[string]string, error)
	SetKey(ctx context.Context, userID int64, providerID, key string) error
	DeleteKey(ctx context.Context, userID int64, providerID string) error
}

// KeyCipher seals provider keys at rest.
type KeyCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// ProviderInfo is a provider with whether the user has a key for it.
type ProviderInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	HasAPIKey bool   `json:"hasApiKey"`
	// KeyUnreadable reports a saved key that can no longer be decrypted,
	// such as after the server's encryption key changed. The user must
	// enter it again.
	KeyUnreadable bool `json:"keyUnreadable,omitempty"`
}

// ResolvedTarget is the configured provider target for a model.
type ResolvedTarget struct {
	Adapter aiprovider.Adapter
	Model   domain.Model
	Creds   aiprovider.Credentials
}

// CatalogService exposes the provider catalog and each user's provider keys.
// A model is usable by a user exactly when they have a key for its provider.
type CatalogService interface {
	ListModels(ctx context.Context, userID int64) ([]domain.ModelInfo, error)
	ListProviders(ctx context.Context, userID int64) ([]ProviderInfo, error)
	// SetProviderKey saves the user's key for a provider.
	SetProviderKey(ctx context.Context, userID int64, providerID, apiKey string) error
	// RemoveProviderKey deletes the user's key for a provider, so their runs
	// can no longer use its models.
	RemoveProviderKey(ctx context.Context, userID int64, providerID string) error
	// ResolveChatTarget returns the adapter, model and the user's key for a
	// request to the model. It fails with domain.ErrNoAPIKey or
	// domain.ErrAPIKeyUnreadable when the user cannot use the model.
	ResolveChatTarget(ctx context.Context, userID int64, modelID string) (ResolvedTarget, error)
	// CheapestConfiguredModel returns the priced model with the lowest
	// input plus output price among the providers the user has a key for,
	// or domain.ErrNoAPIKey when there is none.
	CheapestConfiguredModel(ctx context.Context, userID int64) (domain.Model, error)
	FindModel(modelID string) (domain.Model, error)
}

type catalogService struct {
	registry aiprovider.Registry
	repo     ProviderKeyRepository
	cipher   KeyCipher
}

// NewCatalogService creates a new AI catalog service with the given provider
// registry, key repository and the cipher keys are sealed with.
func NewCatalogService(repo ProviderKeyRepository, cipher KeyCipher, registry aiprovider.Registry) CatalogService {
	return &catalogService{registry: registry, repo: repo, cipher: cipher}
}

// configuredProviders returns, by provider ID, whether each provider the
// user saved a key for has a key that can still be read.
func (s *catalogService) configuredProviders(ctx context.Context, userID int64) (map[string]bool, error) {
	keys, err := s.repo.ListKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing provider keys: %w", err)
	}
	readable := make(map[string]bool, len(keys))
	for providerID, sealed := range keys {
		key, err := s.cipher.Decrypt(sealed)
		readable[providerID] = err == nil && key != ""
	}
	return readable, nil
}

func (s *catalogService) ListModels(ctx context.Context, userID int64) ([]domain.ModelInfo, error) {
	configured, err := s.configuredProviders(ctx, userID)
	if err != nil {
		return nil, err
	}

	var models []domain.ModelInfo
	for _, adapter := range s.registry.All() {
		for _, model := range adapter.Models() {
			models = append(models, domain.ModelInfo{
				Model:        model,
				ProviderName: adapter.ProviderName(),
				Configured:   configured[adapter.ProviderID()],
			})
		}
	}

	return models, nil
}

func (s *catalogService) ListProviders(ctx context.Context, userID int64) ([]ProviderInfo, error) {
	configured, err := s.configuredProviders(ctx, userID)
	if err != nil {
		return nil, err
	}

	var infos []ProviderInfo
	seen := make(map[string]struct{}, len(configured))
	for _, adapter := range s.registry.All() {
		if _, ok := seen[adapter.ProviderID()]; ok {
			continue
		}
		seen[adapter.ProviderID()] = struct{}{}

		readable, saved := configured[adapter.ProviderID()]
		infos = append(infos, ProviderInfo{
			ID:            adapter.ProviderID(),
			Name:          adapter.ProviderName(),
			HasAPIKey:     saved && readable,
			KeyUnreadable: saved && !readable,
		})
	}

	return infos, nil
}

func (s *catalogService) SetProviderKey(ctx context.Context, userID int64, providerID, apiKey string) error {
	if _, ok := s.registry.ByProviderID(providerID); !ok {
		return domain.ErrProviderNotFound
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return domain.ErrNoAPIKey
	}
	sealed, err := s.cipher.Encrypt(apiKey)
	if err != nil {
		return fmt.Errorf("encrypting provider key: %w", err)
	}
	return s.repo.SetKey(ctx, userID, providerID, sealed)
}

func (s *catalogService) RemoveProviderKey(ctx context.Context, userID int64, providerID string) error {
	if _, ok := s.registry.ByProviderID(providerID); !ok {
		return domain.ErrProviderNotFound
	}
	return s.repo.DeleteKey(ctx, userID, providerID)
}

func (s *catalogService) ResolveChatTarget(ctx context.Context, userID int64, modelID string) (ResolvedTarget, error) {
	adapter, model, err := s.findProviderForModel(modelID)
	if err != nil {
		return ResolvedTarget{}, err
	}

	sealed, err := s.repo.GetKey(ctx, userID, adapter.ProviderID())
	if err != nil {
		return ResolvedTarget{}, fmt.Errorf("getting provider key: %w", err)
	}
	if sealed == "" {
		return ResolvedTarget{}, fmt.Errorf("%s: %w", adapter.ProviderName(), domain.ErrNoAPIKey)
	}
	apiKey, err := s.cipher.Decrypt(sealed)
	if err != nil || apiKey == "" {
		return ResolvedTarget{}, fmt.Errorf("%s: %w", adapter.ProviderName(), domain.ErrAPIKeyUnreadable)
	}

	return ResolvedTarget{
		Adapter: adapter,
		Model:   model,
		Creds:   aiprovider.Credentials{APIKey: apiKey},
	}, nil
}

func (s *catalogService) CheapestConfiguredModel(ctx context.Context, userID int64) (domain.Model, error) {
	configured, err := s.configuredProviders(ctx, userID)
	if err != nil {
		return domain.Model{}, err
	}
	var (
		cheapest domain.Model
		lowest   float64
		found    bool
	)
	for _, adapter := range s.registry.All() {
		if !configured[adapter.ProviderID()] {
			continue
		}
		for _, model := range adapter.Models() {
			if model.Pricing == nil {
				continue
			}
			price := model.Pricing.Input + model.Pricing.Output
			if !found || price < lowest {
				cheapest, lowest, found = model, price, true
			}
		}
	}
	if !found {
		return domain.Model{}, domain.ErrNoAPIKey
	}
	return cheapest, nil
}

func (s *catalogService) FindModel(modelID string) (domain.Model, error) {
	_, model, err := s.findProviderForModel(modelID)
	return model, err
}

func (s *catalogService) findProviderForModel(modelID string) (aiprovider.Adapter, domain.Model, error) {
	if adapter, model, ok := s.registry.ResolveModel(modelID); ok {
		return adapter, model, nil
	}

	return nil, domain.Model{}, domain.ErrModelNotFound
}
