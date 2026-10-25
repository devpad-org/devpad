package gitbridge

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/auth"
)

// Handler serves the pushes made through the devpad remote to the IDE.
type Handler struct {
	bridge *Bridge
}

// NewHandler returns the bridge's HTTP handler.
func NewHandler(b *Bridge) *Handler {
	return &Handler{bridge: b}
}

// HandleActivity returns a project's pushes after ?after=. Without it, only
// the latest sequence number comes back, so a newly opened IDE does not
// replay old pushes.
func (h *Handler) HandleActivity(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	s := r.URL.Query().Get("after")
	if s == "" {
		_, latest := h.bridge.Activity(user.ID, projectID, 0)
		writeJSON(w, http.StatusOK, map[string]any{"events": []Event{}, "latest": latest})
		return
	}
	after, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid after")
		return
	}
	events, latest := h.bridge.Activity(user.ID, projectID, after)
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "latest": latest})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
