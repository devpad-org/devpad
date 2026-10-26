package app

import (
	"context"
	"fmt"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// BudgetDefaultsRepository persists the limits an admin set on what agents
// may spend.
type BudgetDefaultsRepository interface {
	GetBudgetDefaults(ctx context.Context) (domain.BudgetDefaults, error)
	SetBudgetDefaults(ctx context.Context, b domain.BudgetDefaults) error
}

// BudgetDefaultsService manages the limits on what task, review and
// assistant agents may spend.
type BudgetDefaultsService interface {
	BudgetDefaults(ctx context.Context) (domain.BudgetDefaults, error)
	// SetBudgetDefaults refuses a negative or non-finite limit with
	// domain.ErrInvalidBudget.
	SetBudgetDefaults(ctx context.Context, b domain.BudgetDefaults) (domain.BudgetDefaults, error)
}

type budgetDefaultsService struct {
	repo BudgetDefaultsRepository
}

// NewBudgetDefaultsService creates the app service for agent budget defaults.
func NewBudgetDefaultsService(repo BudgetDefaultsRepository) BudgetDefaultsService {
	return &budgetDefaultsService{repo: repo}
}

func (s *budgetDefaultsService) BudgetDefaults(ctx context.Context) (domain.BudgetDefaults, error) {
	b, err := s.repo.GetBudgetDefaults(ctx)
	if err != nil {
		return domain.BudgetDefaults{}, fmt.Errorf("loading budget defaults: %w", err)
	}
	return b, nil
}

func (s *budgetDefaultsService) SetBudgetDefaults(ctx context.Context, b domain.BudgetDefaults) (domain.BudgetDefaults, error) {
	for _, limit := range []float64{b.TaskUSD, b.ReviewUSD, b.AssistantUSD} {
		if err := domain.ValidateBudget(limit); err != nil {
			return domain.BudgetDefaults{}, err
		}
	}
	if err := s.repo.SetBudgetDefaults(ctx, b); err != nil {
		return domain.BudgetDefaults{}, fmt.Errorf("saving budget defaults: %w", err)
	}
	return b, nil
}
