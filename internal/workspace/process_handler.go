package workspace

import (
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/auth"
)

// HandleListProcesses returns processes running inside a workspace container.
func (h *Handler) HandleListProcesses(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	processes, err := h.service.ListProcesses(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, processes)
}

// HandleKillProcess terminates a process inside a workspace container.
func (h *Handler) HandleKillProcess(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		writeError(w, http.StatusBadRequest, "invalid process id")
		return
	}

	if err := h.service.KillProcess(r.Context(), user.ID, id, pid); err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "signaled"})
}
