package skills

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Handler serves a workspace's skills to the IDE.
type Handler struct {
	provider *Provider
	scopes   aitools.ScopeResolver
}

// NewHandler returns the skills HTTP handler. It lists the skills a run in
// the workspace would get, so it resolves the workspace as runs do.
func NewHandler(p *Provider, scopes aitools.ScopeResolver) *Handler {
	return &Handler{provider: p, scopes: scopes}
}

// HandleList returns the skills a workspace offers.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	workspaceID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	scope, err := h.scopes.Resolve(r.Context(), user.ID, workspaceID)
	var found []Skill
	if err == nil {
		found, err = h.provider.List(r.Context(), scope)
	}
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
