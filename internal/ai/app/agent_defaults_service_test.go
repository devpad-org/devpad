package app

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type memoryAgentDefaultsRepo struct {
	defaults map[string]domain.AgentDefaults
}

func (r *memoryAgentDefaultsRepo) ListAgentDefaults(context.Context) ([]domain.AgentDefaults, error) {
	out := make([]domain.AgentDefaults, 0, len(r.defaults))
	for _, d := range r.defaults {
		out = append(out, d)
	}
	return out, nil
}

func (r *memoryAgentDefaultsRepo) SetAgentDefaults(_ context.Context, d domain.AgentDefaults) error {
	r.defaults[d.AgentID] = d
	return nil
}

func (r *memoryAgentDefaultsRepo) DeleteAgentDefaults(_ context.Context, agentID string) error {
	delete(r.defaults, agentID)
	return nil
}

type stubModelFinder map[string]domain.Model

func (f stubModelFinder) FindModel(id string) (domain.Model, error) {
	if model, ok := f[id]; ok {
		return model, nil
	}
	return domain.Model{}, domain.ErrModelNotFound
}

func newTestAgentDefaultsService() (*memoryAgentDefaultsRepo, AgentDefaultsService) {
	repo := &memoryAgentDefaultsRepo{defaults: map[string]domain.AgentDefaults{}}
	models := stubModelFinder{
		"thinker": {ID: "thinker", Thinking: domain.ThinkingCapability{Supported: true, SupportedEfforts: []string{"low", "high"}}},
		"plain":   {ID: "plain"},
	}
	return repo, NewAgentDefaultsService(repo, models)
}

func TestAgentDefaultsServiceSetsAndClearsDefaults(t *testing.T) {
	repo, service := newTestAgentDefaultsService()
	ctx := context.Background()

	got, err := service.SetAgentDefaults(ctx, domain.AgentDefaults{AgentID: domain.ReviewAgentID, Model: " thinker ", Effort: "high"})
	if err != nil {
		t.Fatalf("set defaults: %v", err)
	}
	if got.Model != "thinker" || got.Effort != "high" {
		t.Fatalf("expected trimmed defaults, got %+v", got)
	}
	listed, err := service.ListAgentDefaults(ctx)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	if listed[domain.ReviewAgentID] != *got {
		t.Fatalf("expected review defaults listed, got %+v", listed)
	}

	if _, err := service.SetAgentDefaults(ctx, domain.AgentDefaults{AgentID: domain.ReviewAgentID}); err != nil {
		t.Fatalf("clear defaults: %v", err)
	}
	if _, ok := repo.defaults[domain.ReviewAgentID]; ok {
		t.Fatalf("expected defaults cleared, got %+v", repo.defaults)
	}
}

func TestAgentDefaultsServiceRejectsInvalidDefaults(t *testing.T) {
	tests := []struct {
		name     string
		defaults domain.AgentDefaults
		want     error
	}{
		{"custom agent", domain.AgentDefaults{AgentID: "7", Model: "thinker"}, domain.ErrAgentNotFound},
		{"unknown model", domain.AgentDefaults{AgentID: domain.CodingAgentID, Model: "nope"}, domain.ErrModelNotFound},
		{"unsupported effort", domain.AgentDefaults{AgentID: domain.CodingAgentID, Model: "thinker", Effort: "xhigh"}, domain.ErrThinkingEffortInvalid},
		{"effort without thinking", domain.AgentDefaults{AgentID: domain.CodingAgentID, Model: "plain", Effort: "low"}, domain.ErrThinkingEffortNotSupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, service := newTestAgentDefaultsService()
			if _, err := service.SetAgentDefaults(context.Background(), tt.defaults); !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
			if len(repo.defaults) != 0 {
				t.Fatalf("expected nothing saved, got %+v", repo.defaults)
			}
		})
	}
}

// Defaults devpad can no longer honor, like a model a provider dropped, are
// left out so the pickers fall back to devpad's own default.
func TestAgentDefaultsServiceSkipsStaleDefaults(t *testing.T) {
	repo, service := newTestAgentDefaultsService()
	repo.defaults[domain.CodingAgentID] = domain.AgentDefaults{AgentID: domain.CodingAgentID, Model: "retired"}
	repo.defaults[domain.ReviewAgentID] = domain.AgentDefaults{AgentID: domain.ReviewAgentID, Model: "plain"}
	repo.defaults["gone"] = domain.AgentDefaults{AgentID: "gone", Model: "plain"}

	listed, err := service.ListAgentDefaults(context.Background())
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	if len(listed) != 1 || listed[domain.ReviewAgentID].Model != "plain" {
		t.Fatalf("expected only the review defaults, got %+v", listed)
	}
}
