package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrProviderNotFound         = errors.New("AI provider not found")
	ErrProviderNotEnabled       = errors.New("AI provider is not enabled")
	ErrModelNotFound            = errors.New("AI model not found")
	ErrNoAPIKey                 = errors.New("no API key configured for provider")
	ErrThinkingNotSupported     = errors.New("model does not support thinking")
	ErrThinkingCannotBeDisabled = errors.New("thinking cannot be disabled for this model")
	ErrConversationNotFound     = errors.New("conversation not found")
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
	FindModel(modelID string) (Model, error)
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

func (s *service) FindModel(modelID string) (Model, error) {
	_, model, err := s.findProviderForModel(modelID)
	return model, err
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

// ConversationService defines business logic for managing chat conversations.
type ConversationService interface {
	// CreateConversation creates a new blank conversation.
	CreateConversation(ctx context.Context, userID, workspaceID int64, model string) (*Conversation, error)

	// ListConversations returns all conversations for a user in a workspace.
	ListConversations(ctx context.Context, userID, workspaceID int64) ([]Conversation, error)

	// GetConversation returns a single conversation, validating user ownership.
	GetConversation(ctx context.Context, id, userID int64) (*Conversation, error)

	// DeleteConversation deletes a conversation and its messages (cascades via FK).
	DeleteConversation(ctx context.Context, id, userID int64) error

	// SaveMessages validates ownership, replaces all messages, updates the
	// conversation's updated_at, and derives a title from the first user message
	// if the title is currently empty.
	SaveMessages(ctx context.Context, conversationID, userID int64, messages []Message) error

	// GetMessages returns messages for a conversation, validating user ownership.
	GetMessages(ctx context.Context, conversationID, userID int64) ([]Message, error)
}

type conversationService struct {
	convRepo ConversationRepository
}

// NewConversationService creates a ConversationService backed by the given repository.
func NewConversationService(convRepo ConversationRepository) ConversationService {
	return &conversationService{convRepo: convRepo}
}

func (s *conversationService) CreateConversation(ctx context.Context, userID, workspaceID int64, model string) (*Conversation, error) {
	conv := &Conversation{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Title:       "",
		Model:       model,
	}
	if err := s.convRepo.CreateConversation(ctx, conv); err != nil {
		return nil, fmt.Errorf("creating conversation: %w", err)
	}
	return conv, nil
}

func (s *conversationService) ListConversations(ctx context.Context, userID, workspaceID int64) ([]Conversation, error) {
	convs, err := s.convRepo.ListConversations(ctx, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing conversations: %w", err)
	}
	return convs, nil
}

func (s *conversationService) GetConversation(ctx context.Context, id, userID int64) (*Conversation, error) {
	conv, err := s.convRepo.GetConversation(ctx, id, userID)
	if err != nil {
		return nil, fmt.Errorf("getting conversation: %w", err)
	}
	return conv, nil
}

func (s *conversationService) DeleteConversation(ctx context.Context, id, userID int64) error {
	if err := s.convRepo.DeleteConversation(ctx, id, userID); err != nil {
		return fmt.Errorf("deleting conversation: %w", err)
	}
	return nil
}

func (s *conversationService) SaveMessages(ctx context.Context, conversationID, userID int64, messages []Message) error {
	// Validate ownership before mutating.
	conv, err := s.convRepo.GetConversation(ctx, conversationID, userID)
	if err != nil {
		return fmt.Errorf("validating conversation ownership: %w", err)
	}
	if conv == nil {
		return ErrConversationNotFound
	}

	// Derive title from first user message if not yet set.
	newTitle := conv.Title
	if newTitle == "" {
		for _, msg := range messages {
			if msg.Role == "user" && msg.Content != "" {
				title := msg.Content
				if len([]rune(title)) > 60 {
					title = string([]rune(title)[:60])
				}
				newTitle = strings.TrimSpace(title)
				break
			}
		}
	}

	if err := s.convRepo.SaveMessages(ctx, conversationID, messages); err != nil {
		return fmt.Errorf("saving messages: %w", err)
	}

	// Update conversation metadata (title and updated_at via UpdateConversation).
	if err := s.convRepo.UpdateConversation(ctx, conversationID, userID, newTitle, conv.Model); err != nil {
		return fmt.Errorf("updating conversation metadata: %w", err)
	}

	return nil
}

func (s *conversationService) GetMessages(ctx context.Context, conversationID, userID int64) ([]Message, error) {
	msgs, err := s.convRepo.GetMessages(ctx, conversationID, userID)
	if err != nil {
		return nil, fmt.Errorf("getting messages: %w", err)
	}
	return msgs, nil
}
