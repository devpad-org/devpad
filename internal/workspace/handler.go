package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/auth"
)

// Handler holds HTTP handlers for workspace endpoints.
type Handler struct {
	service        Service
	allowedOrigins []string
}

// NewHandler creates a new workspace Handler.
// allowedOrigins is the list of origins permitted for WebSocket upgrades.
// If empty, only same-origin requests (no Origin header) are allowed.
func NewHandler(service Service, allowedOrigins []string) *Handler {
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

// HandleListFiles lists files in a workspace directory.
func (h *Handler) HandleListFiles(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/workspace"
	}

	entries, err := h.service.ListFiles(r.Context(), user.ID, id, path)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// HandleReadFile reads a file from the workspace.
func (h *Handler) HandleReadFile(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	data, err := h.service.ReadFile(r.Context(), user.ID, id, path)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

// HandleWriteFile writes content to a file in the workspace.
func (h *Handler) HandleWriteFile(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	if err := h.service.WriteFile(r.Context(), user.ID, id, req.Path, []byte(req.Content)); err != nil {
		handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleDeleteFile deletes a file or directory from the workspace.
func (h *Handler) HandleDeleteFile(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	if err := h.service.DeleteFile(r.Context(), user.ID, id, path); err != nil {
		handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleMkdir creates a directory in the workspace.
func (h *Handler) HandleMkdir(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	if err := h.service.CreateDirectory(r.Context(), user.ID, id, req.Path); err != nil {
		handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleRename renames or moves a file or directory in the workspace.
func (h *Handler) HandleRename(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.OldPath == "" || req.NewPath == "" {
		writeError(w, http.StatusBadRequest, "oldPath and newPath are required")
		return
	}

	if err := h.service.RenameFile(r.Context(), user.ID, id, req.OldPath, req.NewPath); err != nil {
		handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleGitStatus returns the git status of a workspace.
func (h *Handler) HandleGitStatus(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	status, err := h.service.GitStatus(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, status)
}

// HandleGitLog returns the commit log of a workspace.
func (h *Handler) HandleGitLog(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	count := 50
	if c := r.URL.Query().Get("count"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 && n <= 200 {
			count = n
		}
	}

	commits, err := h.service.GitLog(r.Context(), user.ID, id, count)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"commits": commits})
}

// HandleGitBranches returns the branches of a workspace.
func (h *Handler) HandleGitBranches(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	branches, err := h.service.GitBranches(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, branches)
}

// HandleGitDiff returns the diff for a workspace or specific file.
func (h *Handler) HandleGitDiff(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	path := r.URL.Query().Get("path")
	staged := r.URL.Query().Get("staged") == "true"

	diff, err := h.service.GitDiff(r.Context(), user.ID, id, path, staged)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"diff": diff})
}

// HandleGitRemotes returns the list of remotes with their URLs.
func (h *Handler) HandleGitRemotes(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	remotes, err := h.service.GitRemotes(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"remotes": remotes})
}

// HandleGitAction performs a git action (stage, commit, push, etc).
func (h *Handler) HandleGitAction(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req struct {
		Action    string   `json:"action"`
		Files     []string `json:"files"`
		Message   string   `json:"message"`
		Branch    string   `json:"branch"`
		Remote    string   `json:"remote"`
		URL       string   `json:"url"`
		NewName   string   `json:"newName"`
		UserName  string   `json:"userName"`
		UserEmail string   `json:"userEmail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Action == "" {
		writeError(w, http.StatusBadRequest, "action is required")
		return
	}

	result, err := h.service.GitAction(r.Context(), user.ID, id, req.Action, req.Files, req.Message, req.Branch, req.Remote, req.URL, req.NewName, req.UserName, req.UserEmail)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

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
