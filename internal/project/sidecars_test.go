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

	// Starting it again starts nothing by itself: its task or review readies
	// it before its agent runs, so the script never runs beside one.
	e.workspaces.setStatus(ws.ID, workspace.StatusRunning)
	started, err := e.workspaces.Get(context.Background(), e.userID, ws.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	e.svc.WorkspaceStarted(context.Background(), started)
	time.Sleep(20 * time.Millisecond)
	if got := e.workspaces.command(ws.ID); got != "" {
		t.Fatalf("expected no script started by the sandbox starting, got %q", got)
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

// Stopping a sandbox stops its services, which then never get ready: that
// is no failure of theirs, so the run is left for when it starts again.
func TestSandboxStoppedWhileItsServicesGetReady(t *testing.T) {
	e := setupTest(t)
	sidecars := &fakeSidecars{}
	e.svc.SetSidecarServices(sidecars)
	p := e.createProject(t, CreateInput{Name: "proj", SetupScript: "npm ci"})
	sidecars.ready = func(_ context.Context, workspaceID int64) error {
		e.workspaces.setStatus(workspaceID, workspace.StatusStopped)
		return errors.New("the postgres service is not running")
	}
	ws, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := e.svc.AwaitSetup(ctx, e.userID, p.ID, ws.ID)
	if err != nil {
		t.Fatalf("expected no failure for a stopped sandbox, got %v", err)
	}
	if st == nil || st.Status != SetupPending {
		t.Fatalf("expected the setup left pending, got %+v", st)
	}
}

func TestProjectWorkspaceSetupWaitsForItsServices(t *testing.T) {
	e := setupTest(t)
	sidecars := &fakeSidecars{}
	gate := make(chan struct{})
	sidecars.ready = func(context.Context, int64) error {
		<-gate
		return nil
	}
	e.svc.SetSidecarServices(sidecars)
	p := e.createProject(t, CreateInput{Name: "proj", SetupScript: "make migrate"})

	ws, _, err := e.svc.OpenWorkspace(context.Background(), e.userID, p.ID)
	if err != nil {
		close(gate)
		t.Fatalf("open: %v", err)
	}
	waitForSetup(t, e, ws.ID, SetupPending)
	if got := e.workspaces.command(ws.ID); got != "" {
		close(gate)
		t.Fatalf("expected the script to wait for the services, but %q started", got)
	}
	close(gate)
	waitForSetup(t, e, ws.ID, SetupSucceeded)
	if got := e.workspaces.command(ws.ID); got != "make migrate" {
		t.Fatalf("setup command = %q", got)
	}
}

// Nobody waits on the project workspace's run, so services that never get
// ready fail it, saying why, and the user can run it again.
func TestProjectWorkspaceWhoseServicesAreNotReady(t *testing.T) {
	e := setupTest(t)
	sidecars := &fakeSidecars{}
	notReady := errors.New("the postgres service was not ready after 4m0s")
	sidecars.ready = func(context.Context, int64) error { return notReady }
	e.svc.SetSidecarServices(sidecars)
	p := e.createProject(t, CreateInput{Name: "proj", SetupScript: "make migrate"})
	ctx := context.Background()

	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	st := waitForSetup(t, e, ws.ID, SetupFailed)
	if !strings.Contains(st.Error, "did not run") || !strings.Contains(st.Error, notReady.Error()) || st.FinishedAt == nil {
		t.Fatalf("expected the failure to say why, got %+v", st)
	}
	if got := e.workspaces.command(ws.ID); got != "" {
		t.Fatalf("expected no script started, got %q", got)
	}

	if _, err := e.svc.RunWorkspaceSetup(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("run again: %v", err)
	}
	waitForSetup(t, e, ws.ID, SetupSucceeded)
}

// A project workspace's run left pending while it was stopped starts when
// the workspace starts again, and reading it starts nothing.
func TestProjectWorkspaceSetupLeftPendingStartsWithIt(t *testing.T) {
	e := setupTest(t)
	e.svc.SetSidecarServices(&fakeSidecars{})
	p := e.createProject(t, CreateInput{Name: "proj", SetupScript: "make migrate"})
	ctx := context.Background()
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	waitForSetup(t, e, ws.ID, SetupSucceeded)

	e.workspaces.setStatus(ws.ID, workspace.StatusStopped)
	if err := e.svc.repo.PutSetup(ctx, &Setup{WorkspaceID: ws.ID, UserID: e.userID, Script: "make seed", Status: SetupPending, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("put setup: %v", err)
	}
	if st, err := e.svc.WorkspaceSetup(ctx, e.userID, p.ID); err != nil || st.Status != SetupPending {
		t.Fatalf("expected it pending while stopped, got %+v %v", st, err)
	}
	// Give a preparation started by that read time to (wrongly) start it.
	time.Sleep(20 * time.Millisecond)
	if st, _ := e.svc.repo.GetSetup(ctx, ws.ID); st.Status != SetupPending {
		t.Fatalf("expected it still pending in a stopped workspace, got %+v", st)
	}

	e.workspaces.setStatus(ws.ID, workspace.StatusRunning)
	if _, err := e.svc.WorkspaceSetup(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("read setup: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if st, _ := e.svc.repo.GetSetup(ctx, ws.ID); st.Status != SetupPending {
		t.Fatalf("expected reading it to start nothing, got %+v", st)
	}

	started, err := e.workspaces.Get(ctx, e.userID, ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	e.svc.WorkspaceStarted(ctx, started)
	waitForSetup(t, e, ws.ID, SetupSucceeded)
	if got := e.workspaces.command(ws.ID); got != "make seed" {
		t.Fatalf("setup command = %q", got)
	}
}
