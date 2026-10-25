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

// HandleOverview summarizes activity across the user's projects and links
// each project workspace and sandbox to its task or merge request.
func (h *Handler) HandleOverview(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	overviews, links, err := h.service.Overviews(r.Context(), user.ID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	projects := make([]map[string]any, 0, len(overviews))
	for _, o := range overviews {
		projects = append(projects, map[string]any{
			"projectId":         o.ProjectID,
			"runningAgents":     o.RunningAgents,
			"activeTasks":       o.ActiveTasks,
			"openMergeRequests": o.OpenMergeRequests,
			"awaitingReview":    o.AwaitingReview,
			"lastActivityAt":    o.LastActivityAt,
		})
	}
	workspaces := make([]map[string]any, 0, len(links))
	for _, l := range links {
		workspaces = append(workspaces, map[string]any{
			"workspaceId":    l.WorkspaceID,
			"projectId":      l.ProjectID,
			"kind":           l.Kind,
			"taskId":         l.TaskID,
			"mergeRequestId": l.MergeRequestID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects, "workspaces": workspaces})
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
		Base   string `json:"base"`
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

// HandleDiff lists the files a branch or commit changed since it diverged
// from base, the default branch unless given.
func (h *Handler) HandleDiff(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	q := r.URL.Query()
	diff, err := h.service.Diff(r.Context(), user.ID, id, q.Get("base"), q.Get("head"))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

// HandleFile returns a file's contents at a branch or commit.
func (h *Handler) HandleFile(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	q := r.URL.Query()
	content, err := h.service.FileAt(r.Context(), user.ID, id, q.Get("rev"), q.Get("path"))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	WriteFileContent(w, content)
}

// WriteFileContent serves repo file contents as an inert download, so a file
// from an agent's branch can never render as a page on devpad's origin.
func WriteFileContent(w http.ResponseWriter, content []byte) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	w.Write(content)
}

// HandlePush pushes the default branch to the project's upstream.
func (h *Handler) HandlePush(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	result, err := h.service.Push(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
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

// HandleGetWorkspace returns the project's workspace, or 404 before it has
// been opened.
func (h *Handler) HandleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ws, err := h.service.Workspace(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace": sandboxResponse(ws)})
}

// HandleOpenWorkspace returns the project's workspace, creating it on first
// use.
func (h *Handler) HandleOpenWorkspace(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ws, created, err := h.service.OpenWorkspace(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"workspace": sandboxResponse(ws)})
}

// HandleCheckoutWorkspaceBranch checks out a canonical branch in the
// project workspace.
func (h *Handler) HandleCheckoutWorkspaceBranch(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, branch, ok := workspaceBranchRequest(w, r)
	if !ok {
		return
	}
	result, err := h.service.CheckoutWorkspaceBranch(r.Context(), user.ID, id, branch)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleWorkspaceBranchStatus compares a branch of the project workspace
// with the project's.
func (h *Handler) HandleWorkspaceBranchStatus(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	status, err := h.service.WorkspaceBranchStatus(r.Context(), user.ID, id, r.URL.Query().Get("branch"))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// HandlePublishWorkspaceBranch copies a branch of the project workspace into
// the canonical repo.
func (h *Handler) HandlePublishWorkspaceBranch(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, branch, ok := workspaceBranchRequest(w, r)
	if !ok {
		return
	}
	result, err := h.service.PublishWorkspaceBranch(r.Context(), user.ID, id, branch)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleSyncWorkspace refreshes the project workspace's copy of the default
// branch.
func (h *Handler) HandleSyncWorkspace(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	result, err := h.service.SyncWorkspaceBase(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func workspaceBranchRequest(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return 0, "", false
	}
	var req struct {
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return 0, "", false
	}
	return id, req.Branch, true
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
	var credErr *workspace.CredentialsError
	switch {
	case errors.As(err, &credErr):
		// The client lists them again, for the user to agree to strip, and
		// those that could not be stripped, with why.
		items, failed := credErr.Items, credErr.Failed
		if items == nil {
			items = []agent.CredentialItem{}
		}
		if failed == nil {
			failed = []workspace.CredentialFailure{}
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": credErr.Error(), "credentials": items, "failed": failed})
	case errors.Is(err, ErrNoWorkspace):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrNotFound), errors.Is(err, workspace.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrForbidden), errors.Is(err, workspace.ErrForbidden):
		writeError(w, http.StatusForbidden, "access denied")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrDefaultBranch), errors.Is(err, ErrPublishDefault):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, workspace.ErrNotStandalone), errors.Is(err, workspace.ErrConverting),
		errors.Is(err, workspace.ErrAgentOutdated), errors.Is(err, workspace.ErrLifecycleBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrNotReady), errors.Is(err, workspace.ErrNotRunning),
		errors.Is(err, ErrSandboxLimit), errors.Is(err, ErrBranchInUse), errors.Is(err, ErrBranchPublishing),
		errors.Is(err, ErrDeleting), errors.Is(err, ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	default:
		var agentErr *agent.AgentError
		if errors.As(err, &agentErr) {
			WriteAgentError(w, agentErr)
			return
		}
		log.Printf("project handler: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// WriteAgentError passes on a repo agent's refusal, such as a merge conflict
// or a missing file, with its status; anything else is a bad gateway.
func WriteAgentError(w http.ResponseWriter, err *agent.AgentError) {
	switch err.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge:
		body := map[string]any{"error": err.Message}
		if len(err.Conflicts) > 0 {
			body["conflicts"] = err.Conflicts
		}
		writeJSON(w, err.StatusCode, body)
	default:
		writeError(w, http.StatusBadGateway, err.Message)
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

// HandleDiscoverConversion lists the repos in a standalone workspace that
// could become a project, or previews a repo made from its files.
func (h *Handler) HandleDiscoverConversion(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	found, err := h.service.DiscoverConversion(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, found)
}

// HandleConvertWorkspace makes a project from a standalone workspace.
func (h *Handler) HandleConvertWorkspace(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		RepoPath         string   `json:"repoPath"`
		IncludeSensitive bool     `json:"includeSensitive"`
		StripCredentials []string `json:"stripCredentials"`
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		DefaultBranch    string   `json:"defaultBranch"`
		UpstreamURL      string   `json:"upstreamUrl"`
		SetupScript      string   `json:"setupScript"`
		MaxSandboxes     int      `json:"maxSandboxes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.service.ConvertWorkspace(r.Context(), user.ID, id, ConvertInput(req))
	if err != nil {
		handleServiceError(w, err)
		return
	}
	stripped := result.Stripped
	if stripped == nil {
		stripped = []agent.CredentialItem{}
	}
	body := map[string]any{
		"project":       projectResponse(result.Project),
		"inPlace":       result.InPlace,
		"workspace":     nil,
		"ssh":           result.SSH,
		"stripped":      stripped,
		"restartError":  result.RestartError,
		"keyRotated":    result.KeyRotated,
		"publicKey":     result.PublicKey,
		"rotationError": result.RotationError,
		"progressError": result.ProgressError,
	}
	if result.Workspace != nil {
		body["workspace"] = sandboxResponse(result.Workspace)
	}
	writeJSON(w, http.StatusCreated, body)
}

// HandleConversionNotice returns what converting a workspace into the
// project did, until the user dismisses it.
func (h *Handler) HandleConversionNotice(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	conv, err := h.service.ConversionNotice(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	if conv == nil {
		writeJSON(w, http.StatusOK, map[string]any{"conversion": nil})
		return
	}
	stripped := conv.Stripped
	if stripped == nil {
		stripped = []agent.CredentialItem{}
	}
	sshKept := conv.SSHKept
	if sshKept == nil {
		sshKept = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversion": map[string]any{
		"finished":        conv.State == ConversionDone,
		"stripped":        stripped,
		"sshKept":         sshKept,
		"restartPending":  conv.RestartPending,
		"restartError":    conv.RestartError,
		"rotationPending": conv.RotationPending,
		"keyRotated":      conv.KeyRotated,
		"publicKey":       conv.PublicKey,
		"rotationError":   conv.RotationError,
		"progressError":   conv.ProgressError,
		"createdAt":       conv.CreatedAt,
	}})
}

// HandleDismissConversionNotice stops showing what a conversion did.
func (h *Handler) HandleDismissConversionNotice(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.service.DismissConversionNotice(r.Context(), user.ID, id); err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": true})
}
