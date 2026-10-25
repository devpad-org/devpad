package app

import (
	"context"
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

// ModelFinder looks up a model in the provider catalog.
type ModelFinder interface {
	FindModel(modelID string) (domain.Model, error)
}

// AgentDefaultsService manages the model and reasoning effort each built-in
// agent's model pickers start on.
type AgentDefaultsService interface {
	// ListAgentDefaults returns the defaults set for built-in agents, keyed
	// by agent ID. Agents without any are left out, and so are defaults
	// naming a model or effort devpad no longer offers.
	ListAgentDefaults(ctx context.Context) (map[string]domain.AgentDefaults, error)
	// SetAgentDefaults sets a built-in agent's default model and effort. An
	// empty model clears them, so the agent uses devpad's own default.
	SetAgentDefaults(ctx context.Context, defaults domain.AgentDefaults) (*domain.AgentDefaults, error)
}

type agentDefaultsService struct {
	repo   AgentDefaultsRepository
	models ModelFinder
}

// NewAgentDefaultsService creates the app service for built-in agent defaults.
func NewAgentDefaultsService(repo AgentDefaultsRepository, models ModelFinder) AgentDefaultsService {
	return &agentDefaultsService{repo: repo, models: models}
}

func (s *agentDefaultsService) ListAgentDefaults(ctx context.Context) (map[string]domain.AgentDefaults, error) {
	stored, err := s.repo.ListAgentDefaults(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing agent defaults: %w", err)
	}
	defaults := make(map[string]domain.AgentDefaults, len(stored))
	for _, d := range stored {
		if !domain.IsBuiltInAgentID(d.AgentID) || s.validate(d) != nil {
			continue
		}
		defaults[d.AgentID] = d
	}
	return defaults, nil
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
	if err := s.validate(defaults); err != nil {
		return nil, err
	}
	if err := s.repo.SetAgentDefaults(ctx, defaults); err != nil {
		return nil, fmt.Errorf("saving agent defaults: %w", err)
	}
	return &defaults, nil
}

// validate checks that the catalog offers the model and that the model
// supports the effort. The model's provider need not be configured yet.
func (s *agentDefaultsService) validate(defaults domain.AgentDefaults) error {
	model, err := s.models.FindModel(defaults.Model)
	if err != nil {
		return fmt.Errorf("%w: %s", domain.ErrModelNotFound, defaults.Model)
	}
	if defaults.Effort == "" {
		return nil
	}
	if !model.Thinking.Supported || !model.Thinking.SupportsEffortSelection() {
		return domain.ErrThinkingEffortNotSupported
	}
	if !model.Thinking.SupportsEffort(defaults.Effort) {
		return fmt.Errorf("%w: %s", domain.ErrThinkingEffortInvalid, defaults.Effort)
	}
	return nil
}
