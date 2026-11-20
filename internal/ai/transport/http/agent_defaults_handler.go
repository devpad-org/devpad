package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type agentDefaultsDTO struct {
	AgentID string `json:"agentId"`
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

// HandleListAgentDefaults returns each built-in agent with the default model
// and effort the user set for it.
func (h *Handler) HandleListAgentDefaults(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	defaults, err := h.defaults.ListAgentDefaults(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent defaults")
		return
	}
	agents := make([]agentDefaultsDTO, 0, len(domain.BuiltInAgents()))
	for _, agent := range domain.BuiltInAgents() {
		agents = append(agents, toAgentDefaultsDTO(agent, defaults[agent.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

// HandleSetAgentDefaults sets or, with an empty model, clears the user's
// default model and effort for a built-in agent.
func (h *Handler) HandleSetAgentDefaults(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	agent, ok := domain.BuiltInAgent(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	var req struct {
		Model  string `json:"model"`
		Effort string `json:"effort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	defaults, err := h.defaults.SetAgentDefaults(r.Context(), user.ID, domain.AgentDefaults{AgentID: agent.ID, Model: req.Model, Effort: req.Effort})
	switch {
	case errors.Is(err, domain.ErrAgentNotFound):
		writeError(w, http.StatusNotFound, "agent not found")
		return
	case errors.Is(err, domain.ErrModelNotFound), isThinkingRequestError(err):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to save agent defaults")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": toAgentDefaultsDTO(agent, *defaults)})
}

func toAgentDefaultsDTO(agent domain.Agent, defaults domain.AgentDefaults) agentDefaultsDTO {
	return agentDefaultsDTO{
		AgentID: agent.ID,
		Name:    agent.Name,
		Purpose: agent.Purpose,
		Model:   defaults.Model,
		Effort:  defaults.Effort,
	}
}

// withAgentDefaults fills in the model and effort the user set for a
// built-in agent when the request picks no model. The request's effort is kept if
// that model supports it, and thinking it turned off stays off.
func (h *Handler) withAgentDefaults(ctx context.Context, userID int64, agentID, model string, thinking *domain.ThinkingConfig) (string, *domain.ThinkingConfig, error) {
	if model != "" {
		return model, thinking, nil
	}
	effort := ""
	if thinking != nil {
		effort = thinking.Effort
	}
	d, err := h.defaults.DefaultsFor(ctx, userID, agentID, effort)
	if err != nil || d == nil {
		return model, thinking, err
	}
	switch {
	case thinking != nil && thinking.Enabled != nil && !*thinking.Enabled:
		return d.Model, &domain.ThinkingConfig{Enabled: thinking.Enabled}, nil
	case d.Effort != "":
		return d.Model, &domain.ThinkingConfig{Effort: d.Effort}, nil
	default:
		return d.Model, nil, nil
	}
}
