package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

// fakeSidecars records what a workspace asks of its services. A provisioned
// workspace's services are in its environment.
type fakeSidecars struct {
	calls        []string
	provisioned  map[int64]bool
	provisionErr error
	awaitErr     error
}

func (f *fakeSidecars) StartAll(_ context.Context, id int64, _ string) error {
	f.calls = append(f.calls, fmt.Sprintf("start-all %d", id))
	return nil
}
func (f *fakeSidecars) StopAll(_ context.Context, id int64) error {
	f.calls = append(f.calls, fmt.Sprintf("stop-all %d", id))
	return nil
}
func (f *fakeSidecars) DeleteAll(_ context.Context, id int64) error {
	f.calls = append(f.calls, fmt.Sprintf("delete-all %d", id))
	delete(f.provisioned, id)
	return nil
}
func (f *fakeSidecars) EnvVars(_ context.Context, id int64, _ string) ([]string, error) {
	if f.provisioned[id] {
		return []string{fmt.Sprintf("POSTGRES_HOST=devpad-svc-%d-postgres", id)}, nil
	}
	return nil, nil
}
func (f *fakeSidecars) Provision(_ context.Context, id, projectID int64, _ string, start bool) error {
	f.calls = append(f.calls, fmt.Sprintf("provision %d project %d start %v", id, projectID, start))
	if f.provisionErr != nil {
		return f.provisionErr
	}
	if f.provisioned == nil {
		f.provisioned = map[int64]bool{}
	}
	f.provisioned[id] = true
	return nil
}
func (f *fakeSidecars) AwaitReady(_ context.Context, id int64) error {
	f.calls = append(f.calls, fmt.Sprintf("await %d", id))
	return f.awaitErr
}

func setupServicesTest(t *testing.T) (*service, *mockContainerManager, *fakeSidecars) {
	t.Helper()
	svc, _ := setupSandboxService(t)
	s := svc.(*service)
	sidecars := &fakeSidecars{}
	s.SetSidecarService(sidecars)
	return s, s.container.(*mockContainerManager), sidecars
}

func TestCreateSandboxStartsTheProjectsServicesFirst(t *testing.T) {
	s, cm, sidecars := setupServicesTest(t)
	ctx := context.Background()

	ws, err := s.CreateSandbox(ctx, 1, sandboxSpec("feature"))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	want := []string{
		fmt.Sprintf("provision %d project 7 start true", ws.ID),
		fmt.Sprintf("await %d", ws.ID),
	}
	if !slices.Equal(sidecars.calls, want) {
		t.Fatalf("expected %v, got %v", want, sidecars.calls)
	}
	// Provisioned before the container was made, so its environment says
	// how to reach them.
	if env := cm.envs[len(cm.envs)-1]; !slices.Contains(env, fmt.Sprintf("POSTGRES_HOST=devpad-svc-%d-postgres", ws.ID)) {
		t.Fatalf("expected the container's environment to name the service, got %v", env)
	}
}

func TestCreateSandboxFailsWithoutItsServices(t *testing.T) {
	tests := []struct {
		name         string
		provisionErr error
		awaitErr     error
	}{
		{"not deployed", errors.New("pull failed"), nil},
		{"not ready", nil, errors.New("not ready")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cm, sidecars := setupServicesTest(t)
			sidecars.provisionErr, sidecars.awaitErr = tt.provisionErr, tt.awaitErr
			ctx := context.Background()

			if _, err := s.CreateSandbox(ctx, 1, sandboxSpec("feature")); err == nil {
				t.Fatal("expected the sandbox not to be made")
			}
			if last := sidecars.calls[len(sidecars.calls)-1]; last != "delete-all 1" {
				t.Fatalf("expected its services removed, got %v", sidecars.calls)
			}
			if list, _ := s.List(ctx, 1); len(list) != 0 {
				t.Fatalf("expected no workspace left, got %d", len(list))
			}
			if tt.awaitErr != nil && !slices.Contains(cm.removed, cm.lastCreatedID) {
				t.Fatalf("expected its container removed, got %v", cm.removed)
			}
		})
	}
}

func TestCreateProjectWorkspaceKeepsGoingWithoutItsServices(t *testing.T) {
	s, _, sidecars := setupServicesTest(t)
	sidecars.provisionErr = errors.New("pull failed")
	ctx := context.Background()

	ws, err := s.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("expected the project workspace to be made, got %v", err)
	}
	// Not waited for either: the user's own workspace is not held up.
	want := []string{fmt.Sprintf("provision %d project 7 start true", ws.ID)}
	if !slices.Equal(sidecars.calls, want) {
		t.Fatalf("expected %v, got %v", want, sidecars.calls)
	}
}

func TestStandaloneWorkspacesGetNoProjectServices(t *testing.T) {
	s, _, sidecars := setupServicesTest(t)
	if _, err := s.create(context.Background(), &Workspace{UserID: 1, Name: "old", Kind: KindInteractive}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(sidecars.calls) != 0 {
		t.Fatalf("expected no services asked for, got %v", sidecars.calls)
	}
}

func TestRebuildAddsTheProjectsNewServices(t *testing.T) {
	s, cm, sidecars := setupServicesTest(t)
	ctx := context.Background()
	ws, err := s.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}

	for _, running := range []bool{true, false} {
		if !running {
			if _, err := s.Stop(ctx, 1, ws.ID); err != nil {
				t.Fatalf("stop: %v", err)
			}
		}
		sidecars.calls = nil
		delete(sidecars.provisioned, ws.ID)
		if _, err := s.RebuildProjectContainer(ctx, 1, ws.ID, "devpad-project-7:bbb"); err != nil {
			t.Fatalf("rebuild: %v", err)
		}
		// Started only if the workspace runs.
		want := fmt.Sprintf("provision %d project 7 start %v", ws.ID, running)
		if !slices.Contains(sidecars.calls, want) {
			t.Fatalf("expected %q, got %v", want, sidecars.calls)
		}
		if env := cm.envs[len(cm.envs)-1]; !slices.Contains(env, fmt.Sprintf("POSTGRES_HOST=devpad-svc-%d-postgres", ws.ID)) {
			t.Fatalf("expected the rebuilt container's environment to name the service, got %v", env)
		}
	}
}

// A service added while a workspace runs, or while it is stopped, is not in
// its container's environment, so its agent is told the variables.
func TestServiceVariablesReachTheAgent(t *testing.T) {
	svc, ra := setupSandboxService(t)
	s := svc.(*service)
	sidecars := &fakeSidecars{}
	s.SetSidecarService(sidecars)
	ctx := context.Background()

	ws, err := s.CreateSandbox(ctx, 1, sandboxSpec("feature"))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	// Made with them in its environment.
	if got := ra.paths(); slices.Contains(got, "/api/env/services?") {
		t.Fatalf("create sent the variables, which its environment has: %v", got)
	}
	want := fmt.Sprintf(`{"env":["POSTGRES_HOST=devpad-svc-%d-postgres"]}`, ws.ID)

	if err := s.SyncServiceEnv(ctx, 1, ws.ID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := ra.bodies["/api/env/services"]; got != want {
		t.Fatalf("sync sent %q, want %q", got, want)
	}

	// A stopped workspace is told when it starts.
	if _, err := s.Stop(ctx, 1, ws.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	delete(ra.bodies, "/api/env/services")
	if err := s.SyncServiceEnv(ctx, 1, ws.ID); err != nil {
		t.Fatalf("sync while stopped: %v", err)
	}
	if _, ok := ra.bodies["/api/env/services"]; ok {
		t.Fatal("a stopped workspace's agent was sent the variables")
	}
	delete(sidecars.provisioned, ws.ID)
	if _, err := s.StartSandbox(ctx, 1, ws.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Its services were removed while it was stopped.
	if got := ra.bodies["/api/env/services"]; got != `{"env":[]}` {
		t.Fatalf("start sent %q, want no variables", got)
	}

	// Another user's workspace is refused.
	if err := s.SyncServiceEnv(ctx, 2, ws.ID); err == nil {
		t.Fatal("synced another user's workspace")
	}
}
