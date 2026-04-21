package ai

import (
	"context"
	"testing"
)

// mockRepository implements Repository for testing.
type mockRepository struct {
	configs map[string]*ProviderConfig
}

func newMockRepository() *mockRepository {
	return &mockRepository{configs: make(map[string]*ProviderConfig)}
}

func (r *mockRepository) GetConfig(_ context.Context, id string) (*ProviderConfig, error) {
	cfg, ok := r.configs[id]
	if !ok {
		return nil, nil
	}
	return cfg, nil
}

func (r *mockRepository) ListConfigs(_ context.Context) ([]ProviderConfig, error) {
	var out []ProviderConfig
	for _, c := range r.configs {
		out = append(out, *c)
	}
	return out, nil
}

func (r *mockRepository) UpsertConfig(_ context.Context, cfg *ProviderConfig) error {
	r.configs[cfg.ID] = cfg
	return nil
}

// mockProvider implements Provider for testing.
type mockProvider struct {
	id     string
	models []Model
}

func (p *mockProvider) ID() string      { return p.id }
func (p *mockProvider) Models() []Model { return p.models }
func (p *mockProvider) ChatCompletionStream(_ context.Context, _ string, _ ChatRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 2)
	ch <- StreamEvent{Content: "hello"}
	ch <- StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

func boolPtr(v bool) *bool { return &v }

func TestListModels_EmptyConfig(t *testing.T) {
	repo := newMockRepository()
	provider := &mockProvider{
		id:     "test",
		models: []Model{{ID: "m1", Name: "Model 1", ProviderID: "test"}},
	}
	svc := NewService(repo, provider)

	models, err := svc.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Configured {
		t.Error("expected model to be unconfigured when no provider config exists")
	}
}

func TestListModels_ConfiguredProvider(t *testing.T) {
	repo := newMockRepository()
	repo.configs["test"] = &ProviderConfig{ID: "test", APIKey: "key123", Enabled: true}
	provider := &mockProvider{
		id:     "test",
		models: []Model{{ID: "m1", Name: "Model 1", ProviderID: "test"}},
	}
	svc := NewService(repo, provider)

	models, err := svc.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !models[0].Configured {
		t.Error("expected model to be configured when provider has key and is enabled")
	}
}

func TestChatStream_ProviderNotEnabled(t *testing.T) {
	repo := newMockRepository()
	repo.configs["test"] = &ProviderConfig{ID: "test", APIKey: "key123", Enabled: false}
	provider := &mockProvider{
		id:     "test",
		models: []Model{{ID: "m1", Name: "Model 1", ProviderID: "test"}},
	}
	svc := NewService(repo, provider)

	_, err := svc.ChatStream(context.Background(), ChatRequest{Model: "m1", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != ErrProviderNotEnabled {
		t.Fatalf("expected ErrProviderNotEnabled, got %v", err)
	}
}

func TestChatStream_NoAPIKey(t *testing.T) {
	repo := newMockRepository()
	repo.configs["test"] = &ProviderConfig{ID: "test", APIKey: "", Enabled: true}
	provider := &mockProvider{
		id:     "test",
		models: []Model{{ID: "m1", Name: "Model 1", ProviderID: "test"}},
	}
	svc := NewService(repo, provider)

	_, err := svc.ChatStream(context.Background(), ChatRequest{Model: "m1", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != ErrNoAPIKey {
		t.Fatalf("expected ErrNoAPIKey, got %v", err)
	}
}

func TestChatStream_ModelNotFound(t *testing.T) {
	repo := newMockRepository()
	provider := &mockProvider{
		id:     "test",
		models: []Model{{ID: "m1", Name: "Model 1", ProviderID: "test"}},
	}
	svc := NewService(repo, provider)

	_, err := svc.ChatStream(context.Background(), ChatRequest{Model: "nonexistent", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != ErrModelNotFound {
		t.Fatalf("expected ErrModelNotFound, got %v", err)
	}
}

func TestChatStream_Success(t *testing.T) {
	repo := newMockRepository()
	repo.configs["test"] = &ProviderConfig{ID: "test", APIKey: "key123", Enabled: true}
	provider := &mockProvider{
		id:     "test",
		models: []Model{{ID: "m1", Name: "Model 1", ProviderID: "test"}},
	}
	svc := NewService(repo, provider)

	stream, err := svc.ChatStream(context.Background(), ChatRequest{Model: "m1", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for e := range stream {
		events = append(events, e)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Content != "hello" {
		t.Errorf("expected first event content 'hello', got %q", events[0].Content)
	}
	if !events[1].Done {
		t.Error("expected second event to be done")
	}
}

func TestChatStream_ThinkingNotSupported(t *testing.T) {
	repo := newMockRepository()
	repo.configs["test"] = &ProviderConfig{ID: "test", APIKey: "key123", Enabled: true}
	provider := &mockProvider{
		id:     "test",
		models: []Model{{ID: "m1", Name: "Model 1", ProviderID: "test"}},
	}
	svc := NewService(repo, provider)

	_, err := svc.ChatStream(context.Background(), ChatRequest{
		Model:    "m1",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Thinking: &ThinkingConfig{Enabled: boolPtr(true)},
	})
	if err != ErrThinkingNotSupported {
		t.Fatalf("expected ErrThinkingNotSupported, got %v", err)
	}
}

func TestChatStream_ThinkingCannotBeDisabled(t *testing.T) {
	repo := newMockRepository()
	repo.configs["test"] = &ProviderConfig{ID: "test", APIKey: "key123", Enabled: true}
	provider := &mockProvider{
		id: "test",
		models: []Model{{
			ID:         "m1",
			Name:       "Model 1",
			ProviderID: "test",
			Thinking: ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       false,
			},
		}},
	}
	svc := NewService(repo, provider)

	_, err := svc.ChatStream(context.Background(), ChatRequest{
		Model:    "m1",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Thinking: &ThinkingConfig{Enabled: boolPtr(false)},
	})
	if err != ErrThinkingCannotBeDisabled {
		t.Fatalf("expected ErrThinkingCannotBeDisabled, got %v", err)
	}
}

func TestUpdateProvider_NotFound(t *testing.T) {
	repo := newMockRepository()
	svc := NewService(repo)

	err := svc.UpdateProvider(context.Background(), "nonexistent", "key", true)
	if err != ErrProviderNotFound {
		t.Fatalf("expected ErrProviderNotFound, got %v", err)
	}
}

func TestUpdateProvider_PreservesExistingKey(t *testing.T) {
	repo := newMockRepository()
	repo.configs["test"] = &ProviderConfig{ID: "test", APIKey: "existing-key", Enabled: true}
	provider := &mockProvider{
		id:     "test",
		models: []Model{{ID: "m1", Name: "Model 1", ProviderID: "test"}},
	}
	svc := NewService(repo, provider)

	// Update with empty key should preserve existing
	err := svc.UpdateProvider(context.Background(), "test", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.configs["test"].APIKey != "existing-key" {
		t.Errorf("expected API key to be preserved, got %q", repo.configs["test"].APIKey)
	}
}

func TestListProviders(t *testing.T) {
	repo := newMockRepository()
	repo.configs["test"] = &ProviderConfig{ID: "test", APIKey: "key", Enabled: true}
	provider := &mockProvider{
		id:     "test",
		models: []Model{{ID: "m1", Name: "Model 1", ProviderID: "test"}},
	}
	svc := NewService(repo, provider)

	providers, err := svc.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if !providers[0].Enabled {
		t.Error("expected provider to be enabled")
	}
	if !providers[0].HasAPIKey {
		t.Error("expected provider to have API key")
	}
}

func TestMistralProvider_Models(t *testing.T) {
	p := NewMistralProvider()
	if p.ID() != "mistral" {
		t.Errorf("expected ID 'mistral', got %q", p.ID())
	}

	models := p.Models()
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	ids := map[string]bool{}
	for _, m := range models {
		ids[m.ID] = true
	}
	if !ids["devstral-medium-latest"] {
		t.Error("expected devstral-medium-latest model")
	}
	if !ids["mistral-large-latest"] {
		t.Error("expected mistral-large-latest model")
	}
}

func TestMiniMaxProvider_Models(t *testing.T) {
	p := NewMiniMaxProvider()
	if p.ID() != "minimax" {
		t.Errorf("expected ID 'minimax', got %q", p.ID())
	}

	models := p.Models()
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "MiniMax-M2.7" {
		t.Errorf("expected MiniMax-M2.7, got %q", models[0].ID)
	}
}

func TestOpenAIProvider_Models(t *testing.T) {
	p := NewOpenAIProvider()
	if p.ID() != "openai" {
		t.Errorf("expected ID 'openai', got %q", p.ID())
	}

	models := p.Models()
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "gpt-5.4" {
		t.Errorf("expected gpt-5.4, got %q", models[0].ID)
	}
	if models[0].ProviderID != "openai" {
		t.Errorf("expected provider openai, got %q", models[0].ProviderID)
	}
}

func TestMoonshotProvider_Models(t *testing.T) {
	p := NewMoonshotProvider()
	if p.ID() != "moonshot" {
		t.Errorf("expected ID 'moonshot', got %q", p.ID())
	}

	models := p.Models()
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "kimi-k2.6" {
		t.Errorf("expected kimi-k2.6, got %q", models[0].ID)
	}
	if models[0].ProviderID != "moonshot" {
		t.Errorf("expected provider moonshot, got %q", models[0].ProviderID)
	}
	if !models[0].Thinking.Supported {
		t.Error("expected Moonshot model to support thinking")
	}
	if !models[0].Thinking.EnabledByDefault {
		t.Error("expected Moonshot thinking to be enabled by default")
	}
	if !models[0].Thinking.CanDisable {
		t.Error("expected Moonshot thinking to be disableable")
	}
}
