package workspace

import (
	"net/http"

	"github.com/devpad-org/devpad/internal/auth"
)

// HandleInfo returns agent version and container stats for a workspace.
func (h *Handler) HandleInfo(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	info, err := h.service.Info(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, info)
}
