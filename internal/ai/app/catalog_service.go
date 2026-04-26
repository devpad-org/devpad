package app

import (
	"context"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

// ProviderConfig holds the stored configuration for an AI provider.
type ProviderConfig struct {
	ID        string    `json:"id"`
	APIKey    string    `json:"apiKey,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ProviderConfigRepository handles persistence of AI provider configuration.
type ProviderConfigRepository interface {
	GetConfig(ctx context.Context, providerID string) (*ProviderConfig, error)
	ListConfigs(ctx context.Context) ([]ProviderConfig, error)
	UpsertConfig(ctx context.Context, cfg *ProviderConfig) error
}

// ProviderInfo is a provider with its configuration status.
type ProviderInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	HasAPIKey bool   `json:"hasApiKey"`
}

// ResolvedTarget is the configured provider target for a model.
type ResolvedTarget struct {
	Adapter aiprovider.Adapter
	Model   domain.Model
	Creds   aiprovider.Credentials
}

// CatalogService exposes provider catalog and configuration behaviors.
type CatalogService interface {
	ListModels(ctx context.Context) ([]domain.ModelInfo, error)
	ListProviders(ctx context.Context) ([]ProviderInfo, error)
	UpdateProvider(ctx context.Context, providerID string, apiKey string, enabled bool) error
	ResolveChatTarget(ctx context.Context, modelID string) (ResolvedTarget, error)
	FindModel(modelID string) (domain.Model, error)
}

type catalogService struct {
	registry aiprovider.Registry
	repo     ProviderConfigRepository
}

// NewCatalogService creates a new AI catalog service with the given provider registry and repository.
func NewCatalogService(repo ProviderConfigRepository, registry aiprovider.Registry) CatalogService {
	return &catalogService{registry: registry, repo: repo}
}

func (s *catalogService) ListModels(ctx context.Context) ([]domain.ModelInfo, error) {
	configs, err := s.repo.ListConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing provider configs: %w", err)
	}

	configMap := make(map[string]*ProviderConfig, len(configs))
	for i := range configs {
		configMap[configs[i].ID] = &configs[i]
	}

	var models []domain.ModelInfo
	for _, adapter := range s.registry.All() {
		cfg := configMap[adapter.ProviderID()]
		configured := cfg != nil && cfg.Enabled && cfg.APIKey != ""

		for _, model := range adapter.Models() {
			models = append(models, domain.ModelInfo{
				Model:        model,
				ProviderName: adapter.ProviderName(),
				Configured:   configured,
			})
		}
	}

	return models, nil
}

func (s *catalogService) ListProviders(ctx context.Context) ([]ProviderInfo, error) {
	configs, err := s.repo.ListConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing provider configs: %w", err)
	}

	configMap := make(map[string]*ProviderConfig, len(configs))
	for i := range configs {
		configMap[configs[i].ID] = &configs[i]
	}

	var infos []ProviderInfo
	seen := make(map[string]struct{}, len(configMap))
	for _, adapter := range s.registry.All() {
		if _, ok := seen[adapter.ProviderID()]; ok {
			continue
		}
		seen[adapter.ProviderID()] = struct{}{}

		cfg := configMap[adapter.ProviderID()]
		info := ProviderInfo{
			ID:   adapter.ProviderID(),
			Name: adapter.ProviderName(),
		}
		if cfg != nil {
			info.Enabled = cfg.Enabled
			info.HasAPIKey = cfg.APIKey != ""
		}
		infos = append(infos, info)
	}

	return infos, nil
}

func (s *catalogService) UpdateProvider(ctx context.Context, providerID string, apiKey string, enabled bool) error {
	if _, ok := s.registry.ByProviderID(providerID); !ok {
		return domain.ErrProviderNotFound
	}

	if apiKey == "" {
		existing, err := s.repo.GetConfig(ctx, providerID)
		if err != nil {
			return fmt.Errorf("getting existing config: %w", err)
		}
		if existing != nil {
			apiKey = existing.APIKey
		}
	}

	return s.repo.UpsertConfig(ctx, &ProviderConfig{
		ID:      providerID,
		APIKey:  apiKey,
		Enabled: enabled,
	})
}

func (s *catalogService) ResolveChatTarget(ctx context.Context, modelID string) (ResolvedTarget, error) {
	adapter, model, err := s.findProviderForModel(modelID)
	if err != nil {
		return ResolvedTarget{}, err
	}

	cfg, err := s.repo.GetConfig(ctx, adapter.ProviderID())
	if err != nil {
		return ResolvedTarget{}, fmt.Errorf("getting provider config: %w", err)
	}
	if cfg == nil || !cfg.Enabled {
		return ResolvedTarget{}, domain.ErrProviderNotEnabled
	}
	if cfg.APIKey == "" {
		return ResolvedTarget{}, domain.ErrNoAPIKey
	}

	return ResolvedTarget{
		Adapter: adapter,
		Model:   model,
		Creds:   aiprovider.Credentials{APIKey: cfg.APIKey},
	}, nil
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
