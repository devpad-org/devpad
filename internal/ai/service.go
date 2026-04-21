package ai

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrProviderNotFound         = errors.New("AI provider not found")
	ErrProviderNotEnabled       = errors.New("AI provider is not enabled")
	ErrModelNotFound            = errors.New("AI model not found")
	ErrNoAPIKey                 = errors.New("no API key configured for provider")
	ErrThinkingNotSupported     = errors.New("model does not support thinking")
	ErrThinkingCannotBeDisabled = errors.New("thinking cannot be disabled for this model")
)

// ModelInfo is a model with its provider's enabled/configured status.
type ModelInfo struct {
	Model
	ProviderName string `json:"providerName"`
	Configured   bool   `json:"configured"`
}

// ProviderInfo is a provider with its configuration status.
type ProviderInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	HasAPIKey bool   `json:"hasApiKey"`
}

// Service defines the AI business logic.
type Service interface {
	ListModels(ctx context.Context) ([]ModelInfo, error)
	ListProviders(ctx context.Context) ([]ProviderInfo, error)
	UpdateProvider(ctx context.Context, providerID string, apiKey string, enabled bool) error
	ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)
}

type service struct {
	providers map[string]Provider
	repo      Repository
}

// providerDisplayNames maps provider IDs to human-readable names.
var providerDisplayNames = map[string]string{
	"mistral":  "Mistral AI",
	"minimax":  "MiniMax",
	"moonshot": "Moonshot AI",
	"openai":   "OpenAI",
}

// NewService creates a new AI service with the given providers and repository.
func NewService(repo Repository, providers ...Provider) Service {
	pm := make(map[string]Provider, len(providers))
	for _, p := range providers {
		pm[p.ID()] = p
	}
	return &service{providers: pm, repo: repo}
}

func (s *service) ListModels(ctx context.Context) ([]ModelInfo, error) {
	configs, err := s.repo.ListConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing provider configs: %w", err)
	}

	configMap := make(map[string]*ProviderConfig, len(configs))
	for i := range configs {
		configMap[configs[i].ID] = &configs[i]
	}

	var models []ModelInfo
	for _, p := range s.providers {
		cfg := configMap[p.ID()]
		configured := cfg != nil && cfg.Enabled && cfg.APIKey != ""

		for _, m := range p.Models() {
			models = append(models, ModelInfo{
				Model:        m,
				ProviderName: providerDisplayName(p.ID()),
				Configured:   configured,
			})
		}
	}
	return models, nil
}

func (s *service) ListProviders(ctx context.Context) ([]ProviderInfo, error) {
	configs, err := s.repo.ListConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing provider configs: %w", err)
	}

	configMap := make(map[string]*ProviderConfig, len(configs))
	for i := range configs {
		configMap[configs[i].ID] = &configs[i]
	}

	var infos []ProviderInfo
	for _, p := range s.providers {
		cfg := configMap[p.ID()]
		info := ProviderInfo{
			ID:   p.ID(),
			Name: providerDisplayName(p.ID()),
		}
		if cfg != nil {
			info.Enabled = cfg.Enabled
			info.HasAPIKey = cfg.APIKey != ""
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (s *service) UpdateProvider(ctx context.Context, providerID string, apiKey string, enabled bool) error {
	if _, ok := s.providers[providerID]; !ok {
		return ErrProviderNotFound
	}

	// If apiKey is empty, preserve the existing key
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

func (s *service) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error) {
	provider, model, err := s.findProviderForModel(req.Model)
	if err != nil {
		return nil, err
	}
	if err := validateThinkingRequest(model, req); err != nil {
		return nil, err
	}

	cfg, err := s.repo.GetConfig(ctx, provider.ID())
	if err != nil {
		return nil, fmt.Errorf("getting provider config: %w", err)
	}
	if cfg == nil || !cfg.Enabled {
		return nil, ErrProviderNotEnabled
	}
	if cfg.APIKey == "" {
		return nil, ErrNoAPIKey
	}

	return provider.ChatCompletionStream(ctx, cfg.APIKey, req)
}

func (s *service) findProviderForModel(modelID string) (Provider, Model, error) {
	for _, p := range s.providers {
		for _, m := range p.Models() {
			if m.ID == modelID {
				return p, m, nil
			}
		}
	}
	return nil, Model{}, ErrModelNotFound
}

func validateThinkingRequest(model Model, req ChatRequest) error {
	if req.Thinking == nil || req.Thinking.Enabled == nil {
		return nil
	}

	enabled := *req.Thinking.Enabled
	if enabled {
		if !model.Thinking.Supported {
			return ErrThinkingNotSupported
		}
		return nil
	}

	if !model.Thinking.Supported {
		return nil
	}
	if !model.Thinking.CanDisable {
		return ErrThinkingCannotBeDisabled
	}

	return nil
}

func providerDisplayName(id string) string {
	if name, ok := providerDisplayNames[id]; ok {
		return name
	}
	return id
}
