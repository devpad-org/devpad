package app

import (
	"context"
	"errors"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// testUser is the user the tests' defaults belong to; any other user has
// none and no keys.
const testUser int64 = 1

// memoryAgentDefaultsRepo keeps testUser's defaults in defaults and every
// other user's in others.
type memoryAgentDefaultsRepo struct {
	defaults map[string]domain.AgentDefaults
	others   map[int64]map[string]domain.AgentDefaults
}

func (r *memoryAgentDefaultsRepo) forUser(userID int64) map[string]domain.AgentDefaults {
	if userID == testUser {
		return r.defaults
	}
	if r.others[userID] == nil {
		r.others[userID] = map[string]domain.AgentDefaults{}
	}
	return r.others[userID]
}

func (r *memoryAgentDefaultsRepo) ListAgentDefaults(_ context.Context, userID int64) ([]domain.AgentDefaults, error) {
	defaults := r.forUser(userID)
	out := make([]domain.AgentDefaults, 0, len(defaults))
	for _, d := range defaults {
		out = append(out, d)
	}
	return out, nil
}

func (r *memoryAgentDefaultsRepo) SetAgentDefaults(_ context.Context, userID int64, d domain.AgentDefaults) error {
	r.forUser(userID)[d.AgentID] = d
	return nil
}

func (r *memoryAgentDefaultsRepo) DeleteAgentDefaults(_ context.Context, userID int64, agentID string) error {
	delete(r.forUser(userID), agentID)
	return nil
}

// stubModelCatalog offers its models; only the configured ones can run, and
// only for testUser.
type stubModelCatalog struct {
	models     map[string]domain.Model
	configured map[string]bool
}

func (c stubModelCatalog) FindModel(id string) (domain.Model, error) {
	if model, ok := c.models[id]; ok {
		return model, nil
	}
	return domain.Model{}, domain.ErrModelNotFound
}

func (c stubModelCatalog) ResolveChatTarget(_ context.Context, userID int64, id string) (ResolvedTarget, error) {
	model, err := c.FindModel(id)
	if err != nil {
		return ResolvedTarget{}, err
	}
	if !c.configured[id] || userID != testUser {
		return ResolvedTarget{}, domain.ErrNoAPIKey
	}
	return ResolvedTarget{Model: model}, nil
}

func newTestAgentDefaultsService() (*memoryAgentDefaultsRepo, AgentDefaultsService) {
	repo := &memoryAgentDefaultsRepo{defaults: map[string]domain.AgentDefaults{}, others: map[int64]map[string]domain.AgentDefaults{}}
	catalog := stubModelCatalog{
		models: map[string]domain.Model{
			"thinker": {ID: "thinker", Thinking: domain.ThinkingCapability{Supported: true, SupportedEfforts: []string{"low", "high"}}},
			"plain":   {ID: "plain"},
		},
		configured: map[string]bool{"thinker": true},
	}
	return repo, NewAgentDefaultsService(repo, catalog)
}

func TestAgentDefaultsServiceSetsAndClearsDefaults(t *testing.T) {
	repo, service := newTestAgentDefaultsService()
	ctx := context.Background()

	got, err := service.SetAgentDefaults(ctx, testUser, domain.AgentDefaults{AgentID: domain.ReviewAgentID, Model: " thinker ", Effort: "high"})
	if err != nil {
		t.Fatalf("set defaults: %v", err)
	}
	if got.Model != "thinker" || got.Effort != "high" {
		t.Fatalf("expected trimmed defaults, got %+v", got)
	}
	listed, err := service.ListAgentDefaults(ctx, testUser)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	if listed[domain.ReviewAgentID] != *got {
		t.Fatalf("expected review defaults listed, got %+v", listed)
	}

	if _, err := service.SetAgentDefaults(ctx, testUser, domain.AgentDefaults{AgentID: domain.ReviewAgentID}); err != nil {
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
			if _, err := service.SetAgentDefaults(context.Background(), testUser, tt.defaults); !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
			if len(repo.defaults) != 0 {
				t.Fatalf("expected nothing saved, got %+v", repo.defaults)
			}
		})
	}
}

// A default naming a model devpad no longer offers, or an agent no longer
// built in, is left out. One whose effort the model dropped keeps its model
// with the model's default effort.
func TestAgentDefaultsServiceSkipsStaleDefaults(t *testing.T) {
	repo, service := newTestAgentDefaultsService()
	repo.defaults[domain.CodingAgentID] = domain.AgentDefaults{AgentID: domain.CodingAgentID, Model: "retired"}
	repo.defaults[domain.ReviewAgentID] = domain.AgentDefaults{AgentID: domain.ReviewAgentID, Model: "thinker", Effort: "xhigh"}
	repo.defaults["gone"] = domain.AgentDefaults{AgentID: "gone", Model: "plain"}

	listed, err := service.ListAgentDefaults(context.Background(), testUser)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	want := domain.AgentDefaults{AgentID: domain.ReviewAgentID, Model: "thinker"}
	if len(listed) != 1 || listed[domain.ReviewAgentID] != want {
		t.Fatalf("expected only the review defaults without their effort, got %+v", listed)
	}
}

// Runs only get a default they can use, so one whose provider is not
// configured is none at all.
func TestAgentDefaultsServiceDefaultsForSkipsUnconfiguredModels(t *testing.T) {
	repo, service := newTestAgentDefaultsService()
	ctx := context.Background()
	repo.defaults[domain.CodingAgentID] = domain.AgentDefaults{AgentID: domain.CodingAgentID, Model: "thinker", Effort: "low"}
	repo.defaults[domain.ReviewAgentID] = domain.AgentDefaults{AgentID: domain.ReviewAgentID, Model: "plain"}

	got, err := service.DefaultsFor(ctx, testUser, "", "")
	if err != nil {
		t.Fatalf("defaults for the default agent: %v", err)
	}
	if got == nil || got.Model != "thinker" || got.Effort != "low" {
		t.Fatalf("expected the coding agent's defaults for an empty agent ID, got %+v", got)
	}
	for _, agentID := range []string{domain.ReviewAgentID, "7"} {
		if got, err := service.DefaultsFor(ctx, testUser, agentID, ""); err != nil || got != nil {
			t.Fatalf("%s: expected no usable defaults, got %+v, %v", agentID, got, err)
		}
	}
}

// An effort picked without a model goes with the default model only if that
// model supports it.
func TestAgentDefaultsServiceDefaultsForKeepsASupportedEffort(t *testing.T) {
	repo, service := newTestAgentDefaultsService()
	ctx := context.Background()
	repo.defaults[domain.CodingAgentID] = domain.AgentDefaults{AgentID: domain.CodingAgentID, Model: "thinker", Effort: "low"}

	for effort, want := range map[string]string{"high": "high", "xhigh": "low", "": "low"} {
		got, err := service.DefaultsFor(ctx, testUser, domain.CodingAgentID, effort)
		if err != nil {
			t.Fatalf("%q: %v", effort, err)
		}
		if got == nil || got.Model != "thinker" || got.Effort != want {
			t.Errorf("%q: expected thinker at %q, got %+v", effort, want, got)
		}
	}
}

// Each user has their own defaults, and another user's default is none
// for a user without a key for its model.
func TestAgentDefaultsServiceKeepsEachUsersDefaults(t *testing.T) {
	repo, service := newTestAgentDefaultsService()
	ctx := context.Background()
	const other int64 = 2
	if _, err := service.SetAgentDefaults(ctx, other, domain.AgentDefaults{AgentID: domain.CodingAgentID, Model: "plain"}); err != nil {
		t.Fatalf("set the other user's defaults: %v", err)
	}
	if len(repo.defaults) != 0 {
		t.Fatalf("expected the test user's defaults untouched, got %+v", repo.defaults)
	}
	listed, err := service.ListAgentDefaults(ctx, other)
	if err != nil || listed[domain.CodingAgentID].Model != "plain" {
		t.Fatalf("expected the other user's own default listed, got %+v %v", listed, err)
	}
	repo.defaults[domain.CodingAgentID] = domain.AgentDefaults{AgentID: domain.CodingAgentID, Model: "thinker"}
	if got, err := service.DefaultsFor(ctx, other, domain.CodingAgentID, ""); err != nil || got != nil {
		t.Fatalf("expected no usable default for a user without a key, got %+v %v", got, err)
	}
}
