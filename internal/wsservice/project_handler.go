package wsservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
)

// ProjectAccess reads a project for a user, refusing one that is not
// theirs.
type ProjectAccess interface {
	Get(ctx context.Context, userID, projectID int64) (*project.Project, error)
}

// ProjectHandler holds the HTTP handlers for a project's services: those
// each new sandbox, and the project workspace when it is made or rebuilt,
// gets its own copy of.
type ProjectHandler struct {
	service  Service
	projects ProjectAccess
}

// NewProjectHandler creates a ProjectHandler.
func NewProjectHandler(service Service, projects ProjectAccess) *ProjectHandler {
	return &ProjectHandler{service: service, projects: projects}
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
