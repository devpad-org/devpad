package project

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Handler holds HTTP handlers for project endpoints.
type Handler struct {
	service Service
}

// NewHandler creates a new project Handler.
func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

// HandleList returns all projects for the authenticated user.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	projects, err := h.service.List(r.Context(), user.ID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		items = append(items, projectResponse(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": items})
}

// HandleCreate creates a project and its canonical repo.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	var req struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		DefaultBranch string `json:"defaultBranch"`
		UpstreamURL   string `json:"upstreamUrl"`
		SetupScript   string `json:"setupScript"`
		MaxSandboxes  int    `json:"maxSandboxes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := h.service.Create(r.Context(), user.ID, CreateInput(req))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": projectResponse(p)})
}

// HandleGet returns a single project.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := h.service.Get(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": projectResponse(p)})
}

// HandleUpdate updates a project's editable fields.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		SetupScript  string `json:"setupScript"`
		MaxSandboxes int    `json:"maxSandboxes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := h.service.Update(r.Context(), user.ID, id, UpdateInput(req))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": projectResponse(p)})
}

// HandleDelete deletes a project, its sandboxes, and its repo.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.service.Delete(r.Context(), user.ID, id); err != nil {
		handleServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleBranches lists the canonical repo's branches.
func (h *Handler) HandleBranches(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	refs, err := h.service.Branches(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, refs)
}

// HandleListSandboxes lists a project's sandboxes.
func (h *Handler) HandleListSandboxes(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sandboxes, err := h.service.ListSandboxes(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(sandboxes))
	for _, ws := range sandboxes {
		items = append(items, sandboxResponse(ws))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sandboxes": items})
}

// HandleCreateSandbox starts a sandbox seeded from the project repo.
func (h *Handler) HandleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ws, err := h.service.CreateSandbox(r.Context(), user.ID, id, SandboxInput(req))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"sandbox": sandboxResponse(ws)})
}

// HandlePublishSandbox copies a sandbox's branch into the canonical repo.
func (h *Handler) HandlePublishSandbox(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	workspaceID, ok := pathID(w, r, "workspaceId")
	if !ok {
		return
	}
	result, err := h.service.PublishSandbox(r.Context(), user.ID, id, workspaceID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleCompare reports how far a branch has diverged from another, the
// default branch unless base is given.
func (h *Handler) HandleCompare(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	q := r.URL.Query()
	cmp, err := h.service.Compare(r.Context(), user.ID, id, q.Get("base"), q.Get("head"))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cmp)
}

// HandleStartSandbox restarts a stopped sandbox within the project's limit.
func (h *Handler) HandleStartSandbox(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	workspaceID, ok := pathID(w, r, "workspaceId")
	if !ok {
		return
	}
	ws, err := h.service.StartSandbox(r.Context(), user.ID, id, workspaceID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sandbox": sandboxResponse(ws)})
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return id, true
}

func handleServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, workspace.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrForbidden), errors.Is(err, workspace.ErrForbidden):
		writeError(w, http.StatusForbidden, "access denied")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrDefaultBranch):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNotReady), errors.Is(err, workspace.ErrNotRunning),
		errors.Is(err, ErrSandboxLimit), errors.Is(err, ErrBranchInUse):
		writeError(w, http.StatusConflict, err.Error())
	default:
		var agentErr *agent.AgentError
		if errors.As(err, &agentErr) {
			writeError(w, http.StatusBadGateway, agentErr.Message)
			return
		}
		log.Printf("project handler: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func projectResponse(p *Project) map[string]any {
	return map[string]any{
		"id":            p.ID,
		"name":          p.Name,
		"description":   p.Description,
		"defaultBranch": p.DefaultBranch,
		"upstreamUrl":   p.UpstreamURL,
		"setupScript":   p.SetupScript,
		"maxSandboxes":  p.MaxSandboxes,
		"status":        p.Status,
		"error":         p.Error,
		"createdAt":     p.CreatedAt,
		"updatedAt":     p.UpdatedAt,
	}
}

func sandboxResponse(ws *workspace.Workspace) map[string]any {
	return map[string]any{
		"id":        ws.ID,
		"name":      ws.Name,
		"status":    ws.Status,
		"kind":      ws.Kind,
		"branch":    ws.Branch,
		"projectId": ws.ProjectID,
		"createdAt": ws.CreatedAt,
		"updatedAt": ws.UpdatedAt,
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
