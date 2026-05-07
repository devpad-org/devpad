package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/auth"
)

// HandleListAgents returns the default agent plus user agents available in a workspace.
func (h *Handler) HandleListAgents(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	workspaceID, ok := parsePositiveQueryInt(w, r, "workspaceId", "workspaceId is required")
	if !ok {
		return
	}
	agents, err := h.agents.ListAgents(r.Context(), user.ID, workspaceID)
	if err != nil {
		h.writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

// HandleCreateAgent creates a custom AI agent for the current user.
func (h *Handler) HandleCreateAgent(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req saveAgentRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agent, err := h.agents.CreateAgent(r.Context(), app.SaveAgentRequest{
		UserID:       user.ID,
		WorkspaceID:  req.WorkspaceID,
		Name:         req.Name,
		Purpose:      req.Purpose,
		Instructions: req.Instructions,
		IsGlobal:     req.IsGlobal,
	})
	if err != nil {
		h.writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"agent": agent})
}

// HandleUpdateAgent updates a custom AI agent owned by the current user.
func (h *Handler) HandleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "agent ID is required")
		return
	}
	var req saveAgentRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agent, err := h.agents.UpdateAgent(r.Context(), app.SaveAgentRequest{
		ID:           id,
		UserID:       user.ID,
		WorkspaceID:  req.WorkspaceID,
		Name:         req.Name,
		Purpose:      req.Purpose,
		Instructions: req.Instructions,
		IsGlobal:     req.IsGlobal,
	})
	if err != nil {
		h.writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": agent})
}

// HandleDeleteAgent deletes a custom AI agent owned by the current user.
func (h *Handler) HandleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "agent ID is required")
		return
	}
	if err := h.agents.DeleteAgent(r.Context(), user.ID, id); err != nil {
		h.writeAgentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type saveAgentRequestDTO struct {
	WorkspaceID  int64  `json:"workspaceId"`
	Name         string `json:"name"`
	Purpose      string `json:"purpose"`
	Instructions string `json:"instructions"`
	IsGlobal     bool   `json:"isGlobal"`
}

func (h *Handler) writeAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrAgentNotFound):
		writeError(w, http.StatusNotFound, "agent not found")
	case errors.Is(err, domain.ErrAgentNotEditable):
		writeError(w, http.StatusConflict, "default agent cannot be edited")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}
