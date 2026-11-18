package wsservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

// fakeProjects gives user 1 project 1, whose project workspace is ws.
type fakeProjects struct {
	ws *workspace.Workspace
}

// Get gives user 1 project 1; any other project is not found, and project 2
// is another user's.
func (fakeProjects) Get(_ context.Context, userID, projectID int64) (*project.Project, error) {
	switch {
	case projectID == 2:
		return nil, project.ErrForbidden
	case projectID != 1:
		return nil, project.ErrNotFound
	case userID != 1:
		return nil, project.ErrForbidden
	}
	return &project.Project{ID: 1, UserID: 1}, nil
}

func (f fakeProjects) Workspace(ctx context.Context, userID, projectID int64) (*workspace.Workspace, error) {
	if _, err := f.Get(ctx, userID, projectID); err != nil {
		return nil, err
	}
	if f.ws == nil {
		return nil, project.ErrNoWorkspace
	}
	return f.ws, nil
}

// recordingProvisioner records each workspace given its project's
// services, failing with err.
type recordingProvisioner struct {
	provisioned []int64
	err         error
}

func (r *recordingProvisioner) ProvisionProjectServices(_ context.Context, _, workspaceID int64) error {
	r.provisioned = append(r.provisioned, workspaceID)
	return r.err
}

func serveProject(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 1}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestProjectHandlerAddsListsAndRemovesServices(t *testing.T) {
	h := NewProjectHandler(NewService(&fakeRepository{}, &fakeContainerManager{}), fakeProjects{}, &recordingProvisioner{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/services", h.HandleList)
	mux.HandleFunc("POST /api/projects/{id}/services", h.HandleAdd)
	mux.HandleFunc("DELETE /api/projects/{id}/services/{type}", h.HandleRemove)

	if rec := serveProject(t, mux, "POST", "/api/projects/1/services", `{"serviceType":"postgres"}`); rec.Code != http.StatusCreated {
		t.Fatalf("add postgres: %d %s", rec.Code, rec.Body)
	}
	if rec := serveProject(t, mux, "POST", "/api/projects/1/services", `{"serviceType":"postgres"}`); rec.Code != http.StatusConflict {
		t.Fatalf("expected a second postgres to conflict, got %d", rec.Code)
	}
	if rec := serveProject(t, mux, "POST", "/api/projects/1/services", `{"serviceType":"sqlserver"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected sqlserver without its licence to be refused, got %d", rec.Code)
	}
	if rec := serveProject(t, mux, "POST", "/api/projects/1/services", `{"serviceType":"sqlserver","acceptLicense":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("add sqlserver: %d %s", rec.Code, rec.Body)
	}

	rec := serveProject(t, mux, "GET", "/api/projects/1/services", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var listed struct {
		Services []projectServiceJSON `json:"services"`
		Catalog  []CatalogEntry       `json:"catalog"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if len(listed.Services) != 2 || len(listed.Catalog) == 0 {
		t.Fatalf("expected 2 services and a catalog, got %+v", listed)
	}
	for _, s := range listed.Services {
		if (s.ServiceType == ServiceSQLServer) != (s.LicenseAcceptedAt != nil) {
			t.Fatalf("expected only sqlserver to show a licence acceptance, got %+v", s)
		}
	}

	if rec := serveProject(t, mux, "DELETE", "/api/projects/1/services/postgres", ""); rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if rec := serveProject(t, mux, "DELETE", "/api/projects/1/services/postgres", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("expected removing it again to be 404, got %d", rec.Code)
	}

	// Another user's project, and one that does not exist, look the same.
	for _, path := range []string{"/api/projects/2/services", "/api/projects/9/services"} {
		if rec := serveProject(t, mux, "GET", path, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", path, rec.Code)
		}
		if rec := serveProject(t, mux, "POST", path, `{"serviceType":"postgres"}`); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: expected adding to be 404, got %d", path, rec.Code)
		}
	}
}

func TestProjectHandlerAddsTheProjectsServicesToItsWorkspace(t *testing.T) {
	svc := NewService(&fakeRepository{}, &fakeContainerManager{})
	ws := &workspace.Workspace{ID: 10, UserID: 1, ProjectID: 1, NetworkName: "devpad-net-10", Status: workspace.StatusRunning}
	serve := func(projects fakeProjects, provisioner *recordingProvisioner) *httptest.ResponseRecorder {
		h := NewProjectHandler(svc, projects, provisioner)
		mux := http.NewServeMux()
		mux.HandleFunc("POST /api/projects/{id}/workspace/services", h.HandleAddToWorkspace)
		return serveProject(t, mux, "POST", "/api/projects/1/workspace/services", `{}`)
	}

	provisioner := &recordingProvisioner{}
	if rec := serve(fakeProjects{ws: ws}, provisioner); rec.Code != http.StatusOK {
		t.Fatalf("add to workspace: %d %s", rec.Code, rec.Body)
	}
	if len(provisioner.provisioned) != 1 || provisioner.provisioned[0] != 10 {
		t.Fatalf("expected workspace 10 given the project's services, got %v", provisioner.provisioned)
	}

	// A workspace being rebuilt, started or stopped is not changed meanwhile.
	for _, err := range []error{workspace.ErrRebuilding, workspace.ErrLifecycleBusy, workspace.ErrConverting} {
		if rec := serve(fakeProjects{ws: ws}, &recordingProvisioner{err: err}); rec.Code != http.StatusConflict {
			t.Fatalf("%v: expected 409, got %d", err, rec.Code)
		}
	}
	// A licence to accept again says so.
	rec := serve(fakeProjects{ws: ws}, &recordingProvisioner{err: fmt.Errorf("adding: %w; add it again", ErrLicenseOutdated)})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "add it again") {
		t.Fatalf("expected the outdated licence explained, got %d %s", rec.Code, rec.Body)
	}

	// Without a project workspace there is nothing to add to.
	if rec := serve(fakeProjects{}, &recordingProvisioner{}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 without a workspace, got %d", rec.Code)
	}
}

func TestProjectHandlerAddsTheWorkspacesServicesToTheProject(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepository{}
	svc := NewService(repo, &fakeContainerManager{})
	if _, err := svc.Create(ctx, 10, ServiceMariaDB, CreateOptions{UserID: 1}); err != nil {
		t.Fatalf("create: %v", err)
	}
	ws := &workspace.Workspace{ID: 10, UserID: 1, ProjectID: 1, NetworkName: "devpad-net-10", Status: workspace.StatusRunning}
	h := NewProjectHandler(svc, fakeProjects{ws: ws}, &recordingProvisioner{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{id}/services/adopt", h.HandleAdopt)

	rec := serveProject(t, mux, "POST", "/api/projects/1/services/adopt", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("adopt: %d %s", rec.Code, rec.Body)
	}
	var listed struct {
		Services []projectServiceJSON `json:"services"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(listed.Services) != 1 || listed.Services[0].ServiceType != ServiceMariaDB {
		t.Fatalf("expected the project to list mariadb, got %+v", listed.Services)
	}
}
