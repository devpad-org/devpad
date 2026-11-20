package wsservice

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/devpad-org/devpad/internal/workspace"
)

// fakeWorkspaces gives user 1 a running workspace 5, and records each time
// its agent is sent the services' variables.
type fakeWorkspaces struct {
	synced []int64
}

func (f *fakeWorkspaces) Get(_ context.Context, userID, workspaceID int64) (*workspace.Workspace, error) {
	if userID != 1 || workspaceID != 5 {
		return nil, workspace.ErrNotFound
	}
	return &workspace.Workspace{ID: 5, UserID: 1, Status: workspace.StatusRunning, NetworkName: "devpad-net-5"}, nil
}

func (f *fakeWorkspaces) SyncServiceEnv(_ context.Context, _, workspaceID int64) error {
	f.synced = append(f.synced, workspaceID)
	return nil
}

func TestHandlerTellsARunningWorkspaceAboutItsServices(t *testing.T) {
	workspaces := &fakeWorkspaces{}
	h := NewHandler(NewService(&fakeRepository{}, &fakeContainerManager{}), workspaces)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{id}/services", h.HandleCreate)
	mux.HandleFunc("POST /api/workspaces/{id}/services/{serviceId}/start", h.HandleStart)
	mux.HandleFunc("DELETE /api/workspaces/{id}/services/{serviceId}", h.HandleDelete)

	// Added to a running workspace, it starts at once and its variables
	// go to the agent.
	if rec := serveProject(t, mux, "POST", "/api/workspaces/5/services", `{"serviceType":"postgres"}`); rec.Code != http.StatusCreated {
		t.Fatalf("add postgres: %d %s", rec.Code, rec.Body)
	}
	if want := []int64{5}; !slices.Equal(workspaces.synced, want) {
		t.Fatalf("synced %v after adding, want %v", workspaces.synced, want)
	}

	if rec := serveProject(t, mux, "POST", "/api/workspaces/5/services/1/start", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	if rec := serveProject(t, mux, "DELETE", "/api/workspaces/5/services/1", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if want := []int64{5, 5, 5}; !slices.Equal(workspaces.synced, want) {
		t.Fatalf("synced %v, want %v", workspaces.synced, want)
	}

	// Another user's workspace is refused before anything is synced.
	if rec := serveProject(t, mux, "POST", "/api/workspaces/6/services", `{"serviceType":"postgres"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("expected another workspace to be refused, got %d", rec.Code)
	}
	if len(workspaces.synced) != 3 {
		t.Fatalf("synced %v after a refused add", workspaces.synced)
	}
}
