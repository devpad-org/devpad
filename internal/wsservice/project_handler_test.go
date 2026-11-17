package wsservice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/project"
)

type fakeProjects struct{}

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

func serveProject(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: 1}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestProjectHandlerAddsListsAndRemovesServices(t *testing.T) {
	h := NewProjectHandler(NewService(&fakeRepository{}, &fakeContainerManager{}), fakeProjects{})
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
