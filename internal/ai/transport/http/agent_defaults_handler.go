package httptransport

import (
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
// and effort set for it (admin only).
func (h *Handler) HandleListAgentDefaults(w http.ResponseWriter, r *http.Request) {
	defaults, err := h.defaults.ListAgentDefaults(r.Context())
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

// HandleSetAgentDefaults sets or, with an empty model, clears a built-in
// agent's default model and effort (admin only).
func (h *Handler) HandleSetAgentDefaults(w http.ResponseWriter, r *http.Request) {
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
	defaults, err := h.defaults.SetAgentDefaults(r.Context(), domain.AgentDefaults{AgentID: agent.ID, Model: req.Model, Effort: req.Effort})
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
