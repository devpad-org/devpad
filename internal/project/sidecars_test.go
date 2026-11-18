package project

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/workspace"
)

// fakeSidecars records what the project asks of its workspaces' services.
// ready, when set, answers AwaitReady and may block.
type fakeSidecars struct {
	mu      sync.Mutex
	adopted []string
	pulled  []int64
	awaited []int64
	ready   func(ctx context.Context, workspaceID int64) error
}

func (f *fakeSidecars) AdoptWorkspaceServices(_ context.Context, workspaceID, projectID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adopted = append(f.adopted, fmt.Sprintf("workspace %d project %d", workspaceID, projectID))
	return nil
}

func (f *fakeSidecars) PullImages(_ context.Context, projectID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pulled = append(f.pulled, projectID)
	return nil
}

func (f *fakeSidecars) AwaitReady(ctx context.Context, workspaceID int64) error {
	f.mu.Lock()
	f.awaited = append(f.awaited, workspaceID)
	ready := f.ready
	f.mu.Unlock()
	if ready == nil {
		return nil
	}
	return ready(ctx, workspaceID)
}

func TestSandboxSetupWaitsForItsServices(t *testing.T) {
	e := setupTest(t)
	sidecars := &fakeSidecars{}
	gate := make(chan struct{})
	sidecars.ready = func(ctx context.Context, _ int64) error {
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	e.svc.SetSidecarServices(sidecars)
	p := e.createProject(t, CreateInput{Name: "proj", SetupScript: "npm ci"})

	// Made without waiting for its services, which its callers would
	// otherwise wait for under their locks.
	ws, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if st := waitForSetup(t, e, ws.ID, SetupPending); st.Script != "npm ci" {
		t.Fatalf("unexpected pending setup: %+v", st)
	}
	if got := e.workspaces.command(ws.ID); got != "" {
		t.Fatalf("expected the script to wait for the services, but %q started", got)
	}

	close(gate)
	if st := e.awaitSetup(t, p, ws); st == nil || st.Status != SetupSucceeded {
		t.Fatalf("expected the setup to run once the services were ready, got %+v", st)
	}
	if got := e.workspaces.command(ws.ID); got != "npm ci" {
		t.Fatalf("setup command = %q", got)
	}
}

func TestSandboxWhoseServicesAreNotReady(t *testing.T) {
	e := setupTest(t)
	sidecars := &fakeSidecars{}
	notReady := errors.New("the postgres service was not ready after 4m0s")
	sidecars.ready = func(context.Context, int64) error { return notReady }
	e.svc.SetSidecarServices(sidecars)
	p := e.createProject(t, CreateInput{Name: "proj", SetupScript: "npm ci"})
	ws, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = e.svc.AwaitSetup(ctx, e.userID, p.ID, ws.ID)
	if !errors.Is(err, ErrServicesNotReady) || !strings.Contains(err.Error(), notReady.Error()) {
		t.Fatalf("expected ErrServicesNotReady saying why, got %v", err)
	}
	// The script never ran against missing services, and runs once they
	// are ready, such as when the task is started again.
	if st, _ := e.svc.repo.GetSetup(ctx, ws.ID); st == nil || st.Status != SetupPending {
		t.Fatalf("expected the setup left pending, got %+v", st)
	}
	sidecars.mu.Lock()
	sidecars.ready = nil
	sidecars.mu.Unlock()
	if st := e.awaitSetup(t, p, ws); st == nil || st.Status != SetupSucceeded {
		t.Fatalf("expected the setup to run on the next wait, got %+v", st)
	}
}

func TestStoppedSandboxLeavesItsSetupPending(t *testing.T) {
	e := setupTest(t)
	sidecars := &fakeSidecars{}
	waiting := make(chan struct{})
	gate := make(chan struct{})
	sidecars.ready = func(context.Context, int64) error {
		close(waiting)
		<-gate
		return nil
	}
	e.svc.SetSidecarServices(sidecars)
	p := e.createProject(t, CreateInput{Name: "proj", SetupScript: "npm ci"})
	ws, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	// Stopped while its services get ready.
	<-waiting
	e.workspaces.setStatus(ws.ID, workspace.StatusStopped)
	close(gate)
	st := e.awaitSetup(t, p, ws)
	if st == nil || st.Status != SetupPending {
		t.Fatalf("expected the setup left pending for when it starts again, got %+v", st)
	}
	if got := e.workspaces.command(ws.ID); got != "" {
		t.Fatalf("expected no script started in a stopped sandbox, got %q", got)
	}
}

func TestReconcileStartsSetupsLeftPending(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	sidecars := &fakeSidecars{}
	e.svc.SetSidecarServices(sidecars)
	p := e.createProject(t, CreateInput{Name: "proj"})
	ws, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	e.awaitSetup(t, p, ws)
	// A run the previous server process left waiting for the services.
	if err := e.svc.repo.PutSetup(ctx, &Setup{WorkspaceID: ws.ID, UserID: e.userID, Script: "make deps", Status: SetupPending, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("put setup: %v", err)
	}

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitForSetup(t, e, ws.ID, SetupSucceeded)
	if got := e.workspaces.command(ws.ID); got != "make deps" {
		t.Fatalf("setup command = %q", got)
	}
}

func TestAwaitEnvironmentPullsTheServicesImages(t *testing.T) {
	e := setupTest(t)
	sidecars := &fakeSidecars{}
	e.svc.SetSidecarServices(sidecars)
	p := e.createProject(t, CreateInput{Name: "proj"})

	if _, err := e.svc.AwaitEnvironment(context.Background(), e.userID, p.ID); err != nil {
		t.Fatalf("await environment: %v", err)
	}
	sidecars.mu.Lock()
	defer sidecars.mu.Unlock()
	if !slices.Equal(sidecars.pulled, []int64{p.ID}) {
		t.Fatalf("expected the project's images pulled, got %v", sidecars.pulled)
	}
}
