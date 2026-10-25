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

// HandleActivity returns a project's pushes after ?after=, with the latest
// sequence number and the bridge's epoch. Without after, only the latest
// sequence number comes back, so a newly opened IDE does not replay old
// pushes. An ?epoch= from before a restart returns everything since.
func (h *Handler) HandleActivity(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	q := r.URL.Query()
	if q.Get("after") == "" {
		_, latest, epoch := h.bridge.Activity(user.ID, projectID, 0, "")
		writeJSON(w, http.StatusOK, map[string]any{"events": []Event{}, "latest": latest, "epoch": epoch})
		return
	}
	after, err := strconv.ParseInt(q.Get("after"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid after")
		return
	}
	events, latest, epoch := h.bridge.Activity(user.ID, projectID, after, q.Get("epoch"))
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "latest": latest, "epoch": epoch})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
