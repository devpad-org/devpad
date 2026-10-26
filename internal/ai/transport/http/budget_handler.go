package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// HandleGetBudgetDefaults returns the limits on what agents may spend
// (admin only).
func (h *Handler) HandleGetBudgetDefaults(w http.ResponseWriter, r *http.Request) {
	budgets, err := h.budgets.BudgetDefaults(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent budgets")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"budgets": budgets})
}

// HandleSetBudgetDefaults sets the limits on what agents may spend (admin
// only).
func (h *Handler) HandleSetBudgetDefaults(w http.ResponseWriter, r *http.Request) {
	var req domain.BudgetDefaults
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	budgets, err := h.budgets.SetBudgetDefaults(r.Context(), req)
	switch {
	case errors.Is(err, domain.ErrInvalidBudget):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to save agent budgets")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"budgets": budgets})
}

// assistantBudget is a new budget for one assistant run, or nil when an
// admin set no limit.
func (h *Handler) assistantBudget(ctx context.Context) (*domain.Budget, error) {
	if h.budgets == nil {
		return nil, nil
	}
	defaults, err := h.budgets.BudgetDefaults(ctx)
	if err != nil {
		return nil, err
	}
	if defaults.AssistantUSD <= 0 {
		return nil, nil
	}
	return domain.NewBudget(defaults.AssistantUSD, 0, nil), nil
}
