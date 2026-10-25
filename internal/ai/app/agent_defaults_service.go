package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// AgentDefaultsRepository persists the model defaults set for built-in agents.
type AgentDefaultsRepository interface {
	ListAgentDefaults(ctx context.Context) ([]domain.AgentDefaults, error)
	SetAgentDefaults(ctx context.Context, defaults domain.AgentDefaults) error
	DeleteAgentDefaults(ctx context.Context, agentID string) error
}

// ModelCatalog looks up models and whether a run can use them.
type ModelCatalog interface {
	FindModel(modelID string) (domain.Model, error)
	ResolveChatTarget(ctx context.Context, modelID string) (ResolvedTarget, error)
}

// AgentDefaultsService manages the model and reasoning effort each built-in
// agent runs with when whoever starts it picks none.
type AgentDefaultsService interface {
	// ListAgentDefaults returns the defaults set for built-in agents, keyed
	// by agent ID. Agents without any are left out, and so are defaults
	// naming a model devpad no longer offers; an effort the model no longer
	// supports is dropped, leaving the model's default effort.
	ListAgentDefaults(ctx context.Context) (map[string]domain.AgentDefaults, error)
	// DefaultsFor returns the defaults for an agent, or nil when it has none
	// a run could use: it is not built in, has no defaults, or their model's
	// provider is not configured. An empty agentID is the default agent.
	DefaultsFor(ctx context.Context, agentID string) (*domain.AgentDefaults, error)
	// SetAgentDefaults sets a built-in agent's default model and effort. An
	// empty model clears them, so the agent uses devpad's own default.
	SetAgentDefaults(ctx context.Context, defaults domain.AgentDefaults) (*domain.AgentDefaults, error)
}

type agentDefaultsService struct {
	repo    AgentDefaultsRepository
	catalog ModelCatalog
}

// NewAgentDefaultsService creates the app service for built-in agent defaults.
func NewAgentDefaultsService(repo AgentDefaultsRepository, catalog ModelCatalog) AgentDefaultsService {
	return &agentDefaultsService{repo: repo, catalog: catalog}
}

func (s *agentDefaultsService) ListAgentDefaults(ctx context.Context) (map[string]domain.AgentDefaults, error) {
	stored, err := s.repo.ListAgentDefaults(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing agent defaults: %w", err)
	}
	defaults := make(map[string]domain.AgentDefaults, len(stored))
	for _, d := range stored {
		if !domain.IsBuiltInAgentID(d.AgentID) {
			continue
		}
		model, err := s.catalog.FindModel(d.Model)
		if err != nil {
			continue
		}
		if domain.ValidateThinkingEffort(model, d.Effort) != nil {
			d.Effort = ""
		}
		defaults[d.AgentID] = d
	}
	return defaults, nil
}

func (s *agentDefaultsService) DefaultsFor(ctx context.Context, agentID string) (*domain.AgentDefaults, error) {
	if agentID == "" {
		agentID = domain.DefaultAgentID
	}
	all, err := s.ListAgentDefaults(ctx)
	if err != nil {
		return nil, err
	}
	d, ok := all[agentID]
	if !ok {
		return nil, nil
	}
	if _, err := s.catalog.ResolveChatTarget(ctx, d.Model); err != nil {
		if errors.Is(err, domain.ErrModelNotFound) || errors.Is(err, domain.ErrProviderNotEnabled) || errors.Is(err, domain.ErrNoAPIKey) {
			return nil, nil
		}
		return nil, fmt.Errorf("checking the default model's provider: %w", err)
	}
	return &d, nil
}

func (s *agentDefaultsService) SetAgentDefaults(ctx context.Context, defaults domain.AgentDefaults) (*domain.AgentDefaults, error) {
	if !domain.IsBuiltInAgentID(defaults.AgentID) {
		return nil, domain.ErrAgentNotFound
	}
	defaults.Model = strings.TrimSpace(defaults.Model)
	defaults.Effort = strings.TrimSpace(defaults.Effort)
	if defaults.Model == "" {
		if err := s.repo.DeleteAgentDefaults(ctx, defaults.AgentID); err != nil {
			return nil, fmt.Errorf("clearing agent defaults: %w", err)
		}
		return &domain.AgentDefaults{AgentID: defaults.AgentID}, nil
	}
	// The model's provider need not be configured yet.
	model, err := s.catalog.FindModel(defaults.Model)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrModelNotFound, defaults.Model)
	}
	if err := domain.ValidateThinkingEffort(model, defaults.Effort); err != nil {
		return nil, err
	}
	if err := s.repo.SetAgentDefaults(ctx, defaults); err != nil {
		return nil, fmt.Errorf("saving agent defaults: %w", err)
	}
	return &defaults, nil
}
