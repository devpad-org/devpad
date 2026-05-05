package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
)

// Handler holds HTTP handlers for workspace endpoints.
type Handler struct {
	service        HandlerService
	allowedOrigins []string
}

// NewHandler creates a new workspace Handler.
// allowedOrigins is the list of origins permitted for WebSocket upgrades.
// If empty, only same-origin requests (no Origin header) are allowed.
func NewHandler(service HandlerService, allowedOrigins []string) *Handler {
	return &Handler{service: service, allowedOrigins: allowedOrigins}
}

// HandleList returns all workspaces for the authenticated user.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	workspaces, err := h.service.List(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces")
		return
	}

	items := make([]map[string]any, 0, len(workspaces))
	for _, ws := range workspaces {
		items = append(items, workspaceResponse(ws))
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": items})
}

// HandleGet returns a single workspace by ID.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	ws, err := h.service.Get(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"workspace": workspaceResponse(ws)})
}

// HandleCreate creates a new workspace.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	ws, err := h.service.Create(r.Context(), user.ID, req.Name, req.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workspace")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"workspace": workspaceResponse(ws)})
}

// HandleUpdate updates a workspace's name and description.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	ws, err := h.service.Update(r.Context(), user.ID, id, req.Name, req.Description)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"workspace": workspaceResponse(ws)})
}

// HandleDelete deletes a workspace.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	if err := h.service.Delete(r.Context(), user.ID, id); err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleStart starts a stopped workspace container.
func (h *Handler) HandleStart(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	ws, err := h.service.Start(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"workspace": workspaceResponse(ws)})
}

// HandleStop stops a running workspace container.
func (h *Handler) HandleStop(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	ws, err := h.service.Stop(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"workspace": workspaceResponse(ws)})
}

func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

func handleServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if errors.Is(err, ErrForbidden) {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	if errors.Is(err, ErrNotRunning) {
		writeError(w, http.StatusConflict, "workspace is not running")
		return
	}
	var agentErr *agent.AgentError
	if errors.As(err, &agentErr) {
		if agentErr.SSHHostKey != nil {
			writeJSON(w, agentErr.StatusCode, map[string]any{
				"error":      agentErr.Message,
				"sshHostKey": agentErr.SSHHostKey,
			})
			return
		}
		writeError(w, agentErr.StatusCode, agentErr.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func workspaceResponse(ws *Workspace) map[string]any {
	return map[string]any{
		"id":          ws.ID,
		"name":        ws.Name,
		"description": ws.Description,
		"status":      ws.Status,
		"containerId": ws.ContainerID,
		"memoryLimit": ws.MemoryLimit,
		"nanoCpus":    ws.NanoCPUs,
		"createdAt":   ws.CreatedAt,
		"updatedAt":   ws.UpdatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
