package skills

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Handler serves a workspace's skills to the IDE.
type Handler struct {
	provider *Provider
}

// NewHandler returns the skills HTTP handler.
func NewHandler(p *Provider) *Handler {
	return &Handler{provider: p}
}

// HandleList returns the skills a workspace offers.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	workspaceID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	found, err := h.provider.List(r.Context(), user.ID, workspaceID)
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	case errors.Is(err, workspace.ErrForbidden):
		writeError(w, http.StatusForbidden, "access denied")
		return
	case errors.Is(err, workspace.ErrNotRunning):
		writeError(w, http.StatusConflict, "workspace is not running")
		return
	case errors.Is(err, project.ErrNotReady):
		writeError(w, http.StatusConflict, "the project's repo is not ready")
		return
	case err != nil:
		log.Printf("skills for workspace %d: %v", workspaceID, err)
		writeError(w, http.StatusBadGateway, "could not read the workspace's skills")
		return
	}
	if found == nil {
		found = []Skill{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": found})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
