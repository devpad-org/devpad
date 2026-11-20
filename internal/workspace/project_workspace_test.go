package workspace

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestCreateProjectWorkspaceNeverInjectsSSHKeys(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()

	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}
	if ws.ProjectID != 7 || ws.Kind != KindInteractive || ws.Branch != "" || ws.IsSandbox() || !ws.IsProjectWorkspace() {
		t.Fatalf("unexpected project workspace: %+v", ws)
	}
	if ws.ReceivesUserCredentials() {
		t.Fatal("a project workspace must not receive the user's credentials")
	}
	if got := ra.paths(); len(got) != 0 {
		t.Fatalf("project workspace create sent agent requests %v; SSH keys must not be injected", got)
	}

	if _, err := svc.Stop(ctx, 1, ws.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := svc.Start(ctx, 1, ws.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := ra.paths(); len(got) != 0 {
		t.Fatalf("project workspace start sent agent requests %v; SSH keys must not be injected", got)
	}

	if _, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{Name: "none"}); err == nil {
		t.Fatal("expected a project workspace without a project to fail")
	}
}

func TestProjectWorkspaceRepoOperations(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()
	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}

	if ws.RepoSeeded {
		t.Fatal("a new project workspace should not be marked seeded")
	}
	// Owning no branch, it is seeded onto the base branch itself.
	if _, err := svc.SeedSandboxRepo(ctx, 1, ws.ID, "main", strings.NewReader("main-bundle")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got, err := svc.Get(ctx, 1, ws.ID); err != nil || !got.RepoSeeded {
		t.Fatalf("expected the seed recorded: %+v %v", got, err)
	}
	if got := ra.paths(); len(got) != 1 || got[0] != "/api/repo/seed?base=main&branch=main&email=test%40test.com&name=testuser" {
		t.Fatalf("unexpected seed request: %v", got)
	}

	checkout, err := svc.CheckoutProjectBranch(ctx, 1, ws.ID, "task/1", strings.NewReader("task-bundle"))
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if checkout.Branch != "task/1" || checkout.Upstream != "devpad/task/1" || !checkout.Created || ra.bodies["/api/repo/checkout"] != "task-bundle" {
		t.Fatalf("unexpected checkout: %+v body=%q", checkout, ra.bodies["/api/repo/checkout"])
	}

	bundle, err := svc.BundleProjectBranch(ctx, 1, ws.ID, "feature/y")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	defer bundle.Close()
	if body, _ := io.ReadAll(bundle); string(body) != "bundle:feature/y" {
		t.Fatalf("bundle body = %q", body)
	}

	if got, err := svc.FetchSandboxBase(ctx, 1, ws.ID, "main", strings.NewReader("latest-main")); err != nil || got.Branch != "devpad/main" {
		t.Fatalf("fetch base: %+v %v", got, err)
	}
}

func TestProjectWorkspaceOperationsRejectOtherWorkspaces(t *testing.T) {
	svc, _ := setupSandboxService(t)
	ctx := context.Background()
	plain, err := createLegacyWorkspace(ctx, svc, 1, "mine", "")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sandbox, err := svc.CreateSandbox(ctx, 1, sandboxSpec("feature"))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	pw, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}

	for _, id := range []int64{plain.ID, sandbox.ID} {
		if _, err := svc.CheckoutProjectBranch(ctx, 1, id, "main", strings.NewReader("")); !errors.Is(err, ErrNotProjectWorkspace) {
			t.Fatalf("checkout on workspace %d: expected ErrNotProjectWorkspace, got %v", id, err)
		}
		if _, err := svc.BundleProjectBranch(ctx, 1, id, "main"); !errors.Is(err, ErrNotProjectWorkspace) {
			t.Fatalf("bundle on workspace %d: expected ErrNotProjectWorkspace, got %v", id, err)
		}
		// Only a project's own workspace is served git requests, or fetched
		// into through the devpad remote.
		if _, a, err := svc.GitBridgeAgent(ctx, 1, id); !errors.Is(err, ErrNotProjectWorkspace) || a != nil {
			t.Fatalf("git bridge agent for workspace %d: expected ErrNotProjectWorkspace, got %v", id, err)
		}
		if _, err := svc.FetchProjectBranches(ctx, 1, id, []string{"main"}, strings.NewReader("")); !errors.Is(err, ErrNotProjectWorkspace) {
			t.Fatalf("fetch into workspace %d: expected ErrNotProjectWorkspace, got %v", id, err)
		}
	}
	// The sandbox-only bundle reads a recorded branch, which a project
	// workspace does not have.
	if _, _, err := svc.BundleSandboxBranch(ctx, 1, pw.ID); !errors.Is(err, ErrNotSandbox) {
		t.Fatalf("expected ErrNotSandbox, got %v", err)
	}
	if _, err := svc.BundleProjectBranch(ctx, 2, pw.ID, "main"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for another user, got %v", err)
	}
}

func TestRebuildProjectContainerSwapsContainersKeepingTheVolume(t *testing.T) {
	svc, _ := setupSandboxService(t)
	cm := svc.(*service).container.(*mockContainerManager)
	ctx := context.Background()
	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj", Image: "devpad-project-7:aaa"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}
	id := strconv.FormatInt(ws.ID, 10)
	if got := cm.created[len(cm.created)-1]; got != id+" devpad-project-7:aaa" {
		t.Fatalf("expected the container made from the project's image, got %q", got)
	}
	oldID := ws.ContainerID

	rebuilt, err := svc.RebuildProjectContainer(ctx, 1, ws.ID, "devpad-project-7:bbb")
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	created := cm.created[len(cm.created)-1]
	newName, _, _ := strings.Cut(created, " ")
	newID := "mock-container-" + newName
	if !strings.HasPrefix(newName, id+"-") || newName == id {
		t.Fatalf("expected the new container made under a name of its own, got %q", created)
	}
	if rebuilt.ContainerID != newID || rebuilt.Image != "devpad-project-7:bbb" || rebuilt.Status != StatusRunning || rebuilt.VolumeName != ws.VolumeName {
		t.Fatalf("unexpected rebuilt workspace: %+v", rebuilt)
	}
	if !strings.HasSuffix(created, " devpad-project-7:bbb") {
		t.Fatalf("expected the new container made from the new image, got %q", created)
	}
	if len(cm.removed) != 1 || cm.removed[0] != oldID {
		t.Fatalf("expected the old container removed, got %v", cm.removed)
	}
	if len(cm.renamed) != 1 || cm.renamed[0] != newID+" devpad-ws-"+id {
		t.Fatalf("expected the new container given the workspace's name, got %v", cm.renamed)
	}
	if cm.started[len(cm.started)-1] != newID {
		t.Fatalf("expected the new container started, got %v", cm.started)
	}
	stored, err := svc.Get(ctx, 1, ws.ID)
	if err != nil || stored.ContainerID != newID || stored.Image != "devpad-project-7:bbb" {
		t.Fatalf("expected the new container recorded: %+v %v", stored, err)
	}
}

func TestRebuildProjectContainerLeavesTheWorkspaceWhenTheOldContainerStays(t *testing.T) {
	svc, _ := setupSandboxService(t)
	cm := svc.(*service).container.(*mockContainerManager)
	ctx := context.Background()
	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}
	cm.removeErr = errors.New("docker unavailable")
	if _, err := svc.RebuildProjectContainer(ctx, 1, ws.ID, "devpad-project-7:bbb"); err == nil {
		t.Fatal("expected the rebuild to fail")
	}
	stored, _ := svc.Get(ctx, 1, ws.ID)
	if stored.ContainerID != ws.ContainerID || stored.Image != "" {
		t.Fatalf("a failed rebuild changed the workspace: %+v", stored)
	}
	if cm.started[len(cm.started)-1] != ws.ContainerID {
		t.Fatalf("expected the old container started again, got %v", cm.started)
	}
}

func TestRebuildProjectContainerRefusesOtherWorkspaces(t *testing.T) {
	svc, _ := setupSandboxService(t)
	ctx := context.Background()
	sandbox, err := svc.CreateSandbox(ctx, 1, SandboxSpec{ProjectID: 7, Kind: KindTask, Branch: "task/1", Name: "t"})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if _, err := svc.RebuildProjectContainer(ctx, 1, sandbox.ID, "x"); !errors.Is(err, ErrNotProjectWorkspace) {
		t.Fatalf("expected ErrNotProjectWorkspace, got %v", err)
	}
}

func TestRebuildExcludesOtherLifecycleChanges(t *testing.T) {
	svc, _ := setupSandboxService(t)
	s := svc.(*service)
	ctx := context.Background()
	ws, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}
	done, err := s.beginRebuild(ws.ID)
	if err != nil {
		t.Fatalf("begin rebuild: %v", err)
	}
	if _, err := svc.Stop(ctx, 1, ws.ID); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("expected a stop refused mid-rebuild, got %v", err)
	}
	if _, err := s.UpdateResourceLimits(ctx, ws.ID, DefaultMemoryLimit, DefaultNanoCPUs); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("expected a limits change refused mid-rebuild, got %v", err)
	}
	if _, err := svc.RebuildProjectContainer(ctx, 1, ws.ID, "x"); !errors.Is(err, ErrLifecycleBusy) {
		t.Fatalf("expected a second rebuild refused, got %v", err)
	}
	done()

	stopDone, err := s.beginLifecycle(ws.ID)
	if err != nil {
		t.Fatalf("begin lifecycle: %v", err)
	}
	defer stopDone()
	if _, err := svc.RebuildProjectContainer(ctx, 1, ws.ID, "x"); !errors.Is(err, ErrLifecycleBusy) {
		t.Fatalf("expected a rebuild refused while a stop is in flight, got %v", err)
	}
}
