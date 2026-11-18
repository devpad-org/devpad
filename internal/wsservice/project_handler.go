package wsservice

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

// ProjectAccess reads a project, and its project workspace, for a user,
// refusing one that is not theirs.
type ProjectAccess interface {
	Get(ctx context.Context, userID, projectID int64) (*project.Project, error)
	// Workspace returns the project workspace, or project.ErrNoWorkspace.
	Workspace(ctx context.Context, userID, projectID int64) (*workspace.Workspace, error)
}

// ProjectHandler holds the HTTP handlers for a project's services: those
// each new sandbox, and the project workspace when it is made or rebuilt or
// asks for them, gets its own copy of.
type ProjectHandler struct {
	service    Service
	projects   ProjectAccess
	workspaces workspace.ProjectServiceProvisioner
}

// NewProjectHandler creates a ProjectHandler.
func NewProjectHandler(service Service, projects ProjectAccess, workspaces workspace.ProjectServiceProvisioner) *ProjectHandler {
	return &ProjectHandler{service: service, projects: projects, workspaces: workspaces}
}

// HandleList returns a project's services and the catalog to add from.
func (h *ProjectHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.ownedProject(w, r)
	if !ok {
		return
	}
	services, err := h.service.ProjectServices(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list services")
		return
	}
	items := make([]projectServiceJSON, 0, len(services))
	for _, ps := range services {
		items = append(items, projectServiceResponse(ps))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"services": items,
		"catalog":  h.service.Catalog(r.Context()),
	})
}

// HandleAdd adds a service to a project.
func (h *ProjectHandler) HandleAdd(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.ownedProject(w, r)
	if !ok {
		return
	}
	var req struct {
		ServiceType string `json:"serviceType"`
		// AcceptLicense is the user accepting the service's licence terms
		// for every copy devpad deploys for the project.
		AcceptLicense bool `json:"acceptLicense"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ServiceType == "" {
		writeError(w, http.StatusBadRequest, "serviceType is required")
		return
	}
	user := auth.UserFromContext(r.Context())
	ps, err := h.service.AddProjectService(r.Context(), projectID, ServiceType(req.ServiceType), CreateOptions{
		UserID:        user.ID,
		AcceptLicense: req.AcceptLicense,
	})
	if err != nil {
		if errors.Is(err, ErrDuplicateType) {
			writeError(w, http.StatusConflict, "the project already has a service of this type")
			return
		}
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"service": projectServiceResponse(ps)})
}

// HandleRemove removes a service from a project. Workspaces keep the
// copies they have.
func (h *ProjectHandler) HandleRemove(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.ownedProject(w, r)
	if !ok {
		return
	}
	if err := h.service.RemoveProjectService(r.Context(), projectID, ServiceType(r.PathValue("type"))); err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleAddToWorkspace gives the project workspace its own copy of each of
// the project's services it lacks, started when the workspace runs. A
// running workspace's agent is sent their variables, so new terminals have
// them without a rebuild (workspace.ProvisionProjectServices).
func (h *ProjectHandler) HandleAddToWorkspace(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.ownedProject(w, r)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())
	ws, ok := h.projectWorkspace(w, r, user.ID, projectID)
	if !ok {
		return
	}
	if err := h.workspaces.ProvisionProjectServices(r.Context(), user.ID, ws.ID); err != nil {
		switch {
		case errors.Is(err, ErrLicenseOutdated), errors.Is(err, ErrUnsupportedArch):
			// Both say what to do about it.
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, workspace.ErrLifecycleBusy), errors.Is(err, workspace.ErrRebuilding), errors.Is(err, workspace.ErrConverting):
			writeError(w, http.StatusConflict, "the workspace is busy; try again once it is running or stopped")
		default:
			log.Printf("project %d: adding its services to workspace %d: %v", projectID, ws.ID, err)
			writeError(w, http.StatusInternalServerError, "failed to add the services")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

// HandleAdopt adds the services the project workspace has to the project's,
// so new sandboxes get a copy of each. A licence accepted for the
// workspace's copy covers the project's.
func (h *ProjectHandler) HandleAdopt(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.ownedProject(w, r)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())
	ws, ok := h.projectWorkspace(w, r, user.ID, projectID)
	if !ok {
		return
	}
	if err := h.service.AdoptWorkspaceServices(r.Context(), ws.ID, projectID); err != nil {
		log.Printf("project %d: adopting workspace %d's services: %v", projectID, ws.ID, err)
		writeError(w, http.StatusInternalServerError, "failed to add the services")
		return
	}
	h.HandleList(w, r)
}

// projectWorkspace reads the project workspace, writing the error and
// returning false when there is none.
func (h *ProjectHandler) projectWorkspace(w http.ResponseWriter, r *http.Request, userID, projectID int64) (*workspace.Workspace, bool) {
	ws, err := h.projects.Workspace(r.Context(), userID, projectID)
	switch {
	case errors.Is(err, project.ErrNoWorkspace):
		writeError(w, http.StatusConflict, "the project has no workspace yet")
		return nil, false
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return nil, false
	}
	return ws, true
}

// ownedProject reads the project in the path, writing the error and
// returning false when the user cannot change it.
func (h *ProjectHandler) ownedProject(w http.ResponseWriter, r *http.Request) (int64, bool) {
	projectID, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return 0, false
	}
	user := auth.UserFromContext(r.Context())
	if _, err := h.projects.Get(r.Context(), user.ID, projectID); err != nil {
		switch {
		case errors.Is(err, project.ErrNotFound), errors.Is(err, project.ErrForbidden):
			writeError(w, http.StatusNotFound, "project not found")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return 0, false
	}
	return projectID, true
}

type projectServiceJSON struct {
	ServiceType ServiceType `json:"serviceType"`
	// LicenseAcceptedAt is when the user accepted the service's licence
	// terms, for a service that has them.
	LicenseAcceptedAt *time.Time `json:"licenseAcceptedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
}

func projectServiceResponse(ps *ProjectService) projectServiceJSON {
	out := projectServiceJSON{ServiceType: ps.ServiceType, CreatedAt: ps.CreatedAt}
	if ps.LicenseAcceptance != nil {
		at := ps.LicenseAcceptance.AcceptedAt
		out.LicenseAcceptedAt = &at
	}
	return out
}
