package project

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/workspace"
)

const nodeEnvironment = "RUN apt-get update && apt-get install -y nodejs\nENV NODE_ENV=development\n"

// waitForBuild waits until the project's latest build has the status.
func waitForBuild(t *testing.T, e *testEnv, projectID int64, status BuildStatus) *Build {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := e.svc.repo.GetBuild(context.Background(), projectID)
		if err != nil {
			t.Fatalf("get build: %v", err)
		}
		e.svc.mu.Lock()
		running := e.svc.builds[projectID] != nil
		e.svc.mu.Unlock()
		if b != nil && b.Status == status && !running {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("build never became %s: %+v", status, b)
		}
		time.Sleep(time.Millisecond)
	}
}

func (e *testEnv) updateEnvironment(t *testing.T, p *Project, body string) {
	t.Helper()
	if _, err := e.svc.Update(context.Background(), e.userID, p.ID, UpdateInput{Name: p.Name, SetupScript: p.SetupScript, Environment: body}); err != nil {
		t.Fatalf("update environment: %v", err)
	}
}

func TestValidateEnvironment(t *testing.T) {
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{"plain", nodeEnvironment, true},
		{"empty", "", true},
		{"from", "FROM node:22\nRUN true\n", false},
		{"lowercase from", "  from node:22\n", false},
		{"from in a continued run", "RUN echo \\\n  FROM is just text here\n", true},
		{"from after a comment in a continued run", "RUN echo \\\n# a comment\nFROM still continued\n", true},
		{"from after a continued run ends", "RUN echo \\\n  done\nFROM node:22\n", false},
		{"commented from", "# FROM node:22\nRUN true\n", true},
		{"too large", strings.Repeat("RUN true\n", environmentLimit/9+1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvironment(tt.body)
			if tt.ok && err != nil {
				t.Fatalf("expected it accepted, got %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

func TestEnvironmentDockerfileFramesTheBody(t *testing.T) {
	got := string(environmentDockerfile("USER nobody\nWORKDIR /tmp\nENTRYPOINT [\"sh\"]\n"))
	if !strings.HasPrefix(got, "FROM "+container.WorkspaceImage+"\nUSER root\n") {
		t.Fatalf("expected devpad's image as root first:\n%s", got)
	}
	// Whatever the body changed, devpad's user, directory and agent win.
	if !strings.HasSuffix(got, "USER devpad\nWORKDIR /workspace\nENTRYPOINT [\"/usr/local/bin/devpad-agent\"]\nCMD []\n") {
		t.Fatalf("expected devpad's frame last:\n%s", got)
	}
}

func TestEnvironmentBuildsAndNewWorkspacesUseIt(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})
	b := waitForBuild(t, e, p.ID, BuildSucceeded)

	if !strings.HasPrefix(b.Image, "devpad-project-"+strconv.FormatInt(p.ID, 10)+":") || b.FinishedAt == nil {
		t.Fatalf("unexpected build: %+v", b)
	}
	if got := e.project(t, p.ID).EnvironmentImage; got != b.Image {
		t.Fatalf("expected the image recorded on the project, got %q", got)
	}
	build := e.containers.builds[0]
	if !strings.Contains(build.dockerfile, nodeEnvironment) || !strings.HasPrefix(build.dockerfile, "FROM devpad-workspace:latest") {
		t.Fatalf("unexpected Dockerfile:\n%s", build.dockerfile)
	}
	if !strings.HasPrefix(build.opts.NetworkMode, "devpad-build-") || build.opts.Memory != buildMemory || build.opts.NanoCPUs != buildNanoCPUs {
		t.Fatalf("expected the build bounded and on its own network: %+v", build.opts)
	}
	if build.opts.Labels["devpad.project"] != strconv.FormatInt(p.ID, 10) {
		t.Fatalf("expected the image labelled with its project: %v", build.opts.Labels)
	}
	e.containers.mu.Lock()
	leftover := e.containers.networks[build.opts.NetworkMode]
	repoCreated := e.containers.created[0]
	e.containers.mu.Unlock()
	if leftover {
		t.Fatal("the build network was left behind")
	}
	if strings.Contains(repoCreated, "image=") {
		t.Fatalf("the repo container must keep devpad's image: %s", repoCreated)
	}

	sandbox, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if sandbox.Image != b.Image || ws.Image != b.Image {
		t.Fatalf("expected new workspaces made from %s, got %q and %q", b.Image, sandbox.Image, ws.Image)
	}
	env, err := e.svc.ProjectEnvironment(ctx, e.userID, p.ID)
	if err != nil || !env.Current || env.WorkspaceStale || env.Image != b.Image {
		t.Fatalf("expected a current environment: %+v %v", env, err)
	}
	if EnvironmentPrompt(env) != "" {
		t.Fatal("a current environment needs no word to the agent")
	}
}

func TestProjectWithoutEnvironmentUsesDevpadsImage(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	sandbox, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{})
	if err != nil || sandbox.Image != "" {
		t.Fatalf("expected devpad's image: %+v %v", sandbox, err)
	}
	if env, err := e.svc.AwaitEnvironment(ctx, e.userID, p.ID); env != nil || err != nil {
		t.Fatalf("expected no environment: %+v %v", env, err)
	}
	if len(e.containers.builtTags()) != 0 {
		t.Fatal("nothing should be built without an environment")
	}
}

func TestFailedBuildFallsBackToTheLastImageThatBuilt(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	e.containers.buildErr = errors.New("The command '/bin/sh -c apt-get install -y nodej' returned a non-zero code: 100")
	e.containers.buildOutput = "E: Unable to locate package nodej\n"
	p := e.createProject(t, CreateInput{Name: "proj", Environment: "RUN apt-get install -y nodej\n"})
	b := waitForBuild(t, e, p.ID, BuildFailed)
	if !strings.Contains(b.Error, "non-zero code: 100") || !strings.Contains(b.Log, "Unable to locate package") {
		t.Fatalf("unexpected failed build: %+v", b)
	}

	// Nothing has built yet, so new workspaces get devpad's image.
	sandbox, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{})
	if err != nil || sandbox.Image != "" {
		t.Fatalf("expected devpad's image: %+v %v", sandbox, err)
	}
	env, err := e.svc.AwaitEnvironment(ctx, e.userID, p.ID)
	if err != nil || env.Current {
		t.Fatalf("expected a failed environment: %+v %v", env, err)
	}
	prompt := EnvironmentPrompt(env)
	for _, want := range []string{"failed to build", "devpad's own image without it", "Unable to locate package nodej", "not instructions to you"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}

	// The same Dockerfile on the same image is not built again by itself.
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := len(e.containers.builtTags()); got != 1 {
		t.Fatalf("a failed build was retried unasked: %d builds", got)
	}

	// Asked, it is; this time it builds.
	e.containers.mu.Lock()
	e.containers.buildErr = nil
	e.containers.mu.Unlock()
	if _, err := e.svc.BuildEnvironment(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("build again: %v", err)
	}
	good := waitForBuild(t, e, p.ID, BuildSucceeded)

	// A broken change leaves new workspaces on the last image that built.
	e.containers.mu.Lock()
	e.containers.buildErr = errors.New("returned a non-zero code: 1")
	e.containers.mu.Unlock()
	e.updateEnvironment(t, e.project(t, p.ID), "RUN false\n")
	waitForBuild(t, e, p.ID, BuildFailed)
	next, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{})
	if err != nil || next.Image != good.Image {
		t.Fatalf("expected the last image that built, %s: %+v %v", good.Image, next, err)
	}
	env, _ = e.svc.AwaitEnvironment(ctx, e.userID, p.ID)
	if prompt := EnvironmentPrompt(env); !strings.Contains(prompt, "the last version of it that built") {
		t.Fatalf("unexpected prompt:\n%s", prompt)
	}
}

func TestNewerEnvironmentReplacesABuildInProgress(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	gate := make(chan struct{})
	e.containers.buildGate = gate
	p := e.createProject(t, CreateInput{Name: "proj", Environment: "RUN echo one\n"})

	waited := make(chan *Environment, 1)
	go func() {
		env, _ := e.svc.AwaitEnvironment(ctx, e.userID, p.ID)
		waited <- env
	}()
	time.Sleep(20 * time.Millisecond)
	e.updateEnvironment(t, e.project(t, p.ID), "RUN echo two\n")
	close(gate)

	b := waitForBuild(t, e, p.ID, BuildSucceeded)
	tags := e.containers.builtTags()
	if len(tags) != 2 || b.Image != tags[1] {
		t.Fatalf("expected the second build recorded, got %+v from %v", b, tags)
	}
	if got := e.project(t, p.ID).EnvironmentImage; got != tags[1] {
		t.Fatalf("expected the newer image, got %q", got)
	}
	select {
	case env := <-waited:
		if env == nil || !env.Current || env.Image != tags[1] {
			t.Fatalf("the wait should last until the newer build: %+v", env)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiting for the environment never ended")
	}
}

func TestBuildThatRunsTooLongIsStopped(t *testing.T) {
	e := setupTest(t)
	e.svc.buildTimeout = 50 * time.Millisecond
	e.containers.buildGate = make(chan struct{})
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})
	if b := waitForBuild(t, e, p.ID, BuildFailed); !strings.Contains(b.Error, "stopped after running for 50ms") {
		t.Fatalf("unexpected build: %+v", b)
	}
}

func TestNewDevpadImageRebuildsEnvironments(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})
	first := waitForBuild(t, e, p.ID, BuildSucceeded)

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := len(e.containers.builtTags()); got != 1 {
		t.Fatalf("an up-to-date environment was rebuilt: %d builds", got)
	}

	e.containers.mu.Lock()
	e.containers.baseID = "sha256:base2"
	e.containers.mu.Unlock()
	if env, _ := e.svc.ProjectEnvironment(ctx, e.userID, p.ID); env.Current {
		t.Fatal("an image on the old devpad image is not current")
	}
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	second := waitForBuild(t, e, p.ID, BuildSucceeded)
	if second.Hash == first.Hash || e.project(t, p.ID).EnvironmentImage != second.Image {
		t.Fatalf("expected a new image on the new devpad image: %+v after %+v", second, first)
	}
}

func TestReconcileRestartsABuildCutShort(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	// The project's environment was building when devpad stopped.
	if _, err := e.db.Conn().Exec(`UPDATE projects SET environment = ? WHERE id = ?`, nodeEnvironment, p.ID); err != nil {
		t.Fatalf("set environment: %v", err)
	}
	hash, _ := e.svc.environmentHash(ctx, e.project(t, p.ID))
	if err := e.svc.repo.PutBuild(ctx, &Build{ID: "old", ProjectID: p.ID, Hash: hash, Image: projectImage(p.ID, hash), Status: BuildRunning, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("put build: %v", err)
	}
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if b := waitForBuild(t, e, p.ID, BuildSucceeded); b.ID == "old" {
		t.Fatalf("expected a new build: %+v", b)
	}
}

func TestOpenWorkspaceWaitsForABuildInProgress(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	gate := make(chan struct{})
	e.containers.buildGate = gate
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})

	opened := make(chan *workspace.Workspace, 1)
	go func() {
		ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
		if err != nil {
			t.Errorf("open: %v", err)
		}
		opened <- ws
	}()
	select {
	case <-opened:
		t.Fatal("the workspace was made before the build ended")
	case <-time.After(30 * time.Millisecond):
	}
	close(gate)
	ws := <-opened
	if ws == nil || ws.Image == "" || ws.Image != e.project(t, p.ID).EnvironmentImage {
		t.Fatalf("expected the new image: %+v", ws)
	}
}

func TestRebuildWorkspaceMovesItToTheCurrentImage(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})
	first := waitForBuild(t, e, p.ID, BuildSucceeded)
	ws, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// The workspace's container holds its image, so Docker keeps it.
	e.containers.mu.Lock()
	e.containers.inUse[first.Image] = true
	e.containers.mu.Unlock()

	e.updateEnvironment(t, e.project(t, p.ID), nodeEnvironment+"RUN npm install -g pnpm\n")
	second := waitForBuild(t, e, p.ID, BuildSucceeded)
	env, err := e.svc.ProjectEnvironment(ctx, e.userID, p.ID)
	if err != nil || !env.WorkspaceStale || !env.Current {
		t.Fatalf("expected the workspace behind a current environment: %+v %v", env, err)
	}
	if _, ok := e.containers.images[first.Image]; !ok {
		t.Fatal("an image the workspace still uses was removed")
	}

	if _, err := e.svc.RebuildWorkspace(ctx, e.otherID, p.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for another user, got %v", err)
	}
	rebuilt, err := e.svc.RebuildWorkspace(ctx, e.userID, p.ID)
	if err != nil || rebuilt.Image != second.Image {
		t.Fatalf("rebuild: %+v %v", rebuilt, err)
	}
	if len(e.workspaces.rebuilt) != 1 || e.workspaces.rebuilt[0] != strconv.FormatInt(ws.ID, 10)+" "+second.Image {
		t.Fatalf("unexpected rebuilds: %v", e.workspaces.rebuilt)
	}
	// Once nothing uses the old image, the next prune removes it.
	e.containers.mu.Lock()
	e.containers.inUse[first.Image] = false
	e.containers.mu.Unlock()
	if _, err := e.svc.RebuildWorkspace(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if _, ok := e.containers.images[first.Image]; ok {
		t.Fatal("the old image was kept")
	}
	if env, _ := e.svc.ProjectEnvironment(ctx, e.userID, p.ID); env.WorkspaceStale {
		t.Fatal("the rebuilt workspace should be current")
	}
}

func TestRemovingTheEnvironmentReturnsToDevpadsImage(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})
	b := waitForBuild(t, e, p.ID, BuildSucceeded)
	if _, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("open: %v", err)
	}

	e.updateEnvironment(t, e.project(t, p.ID), "  \n")
	if got := e.project(t, p.ID).EnvironmentImage; got != "" {
		t.Fatalf("expected the image forgotten, got %q", got)
	}
	if b, _ := e.svc.repo.GetBuild(ctx, p.ID); b != nil {
		t.Fatalf("expected the build forgotten: %+v", b)
	}
	if _, ok := e.containers.images[b.Image]; ok {
		t.Fatal("the unused image was kept")
	}
	sandbox, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{})
	if err != nil || sandbox.Image != "" {
		t.Fatalf("expected devpad's image: %+v %v", sandbox, err)
	}
	env, err := e.svc.ProjectEnvironment(ctx, e.userID, p.ID)
	if err != nil || env == nil || !env.WorkspaceStale {
		t.Fatalf("expected the workspace offered a rebuild back to devpad's image: %+v %v", env, err)
	}
}

func TestDeletingAProjectRemovesItsImages(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})
	b := waitForBuild(t, e, p.ID, BuildSucceeded)
	if err := e.svc.Delete(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := e.containers.images[b.Image]; ok {
		t.Fatal("the project's image outlived it")
	}
}

func TestEnvironmentRejectsFrom(t *testing.T) {
	e := setupTest(t)
	if _, err := e.svc.Create(context.Background(), e.userID, CreateInput{Name: "proj", Environment: "FROM node:22\n"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
	if len(e.containers.created) != 0 {
		t.Fatal("an invalid environment made containers")
	}
}

func TestEnvironmentHandlers(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	e.containers.buildErr = errors.New("returned a non-zero code: 1")
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})
	waitForBuild(t, e, p.ID, BuildFailed)
	if _, _, err := e.svc.OpenWorkspace(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("open: %v", err)
	}

	id := strconv.FormatInt(p.ID, 10)
	serve := func(method string, handle http.HandlerFunc) (int, map[string]any) {
		req := httptest.NewRequest(method, "/api/projects/"+id+"/environment", strings.NewReader(`{}`))
		req.SetPathValue("id", id)
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: e.userID}))
		rec := httptest.NewRecorder()
		handle(rec, req)
		var body map[string]any
		json.NewDecoder(rec.Body).Decode(&body)
		return rec.Code, body
	}
	h := NewHandler(e.svc)

	code, body := serve(http.MethodGet, h.HandleEnvironment)
	env, _ := body["environment"].(map[string]any)
	build, _ := env["build"].(map[string]any)
	if code != http.StatusOK || build["status"] != "failed" || env["custom"] != false || env["current"] != false || env["workspaceStale"] != false {
		t.Fatalf("unexpected GET: %d %v", code, body)
	}
	if _, ok := env["image"]; ok {
		t.Fatal("the response should not name Docker images")
	}

	e.containers.mu.Lock()
	e.containers.buildErr = nil
	e.containers.mu.Unlock()
	if code, body = serve(http.MethodPost, h.HandleBuildEnvironment); code != http.StatusOK {
		t.Fatalf("unexpected build: %d %v", code, body)
	}
	waitForBuild(t, e, p.ID, BuildSucceeded)
	_, body = serve(http.MethodGet, h.HandleEnvironment)
	if env, _ := body["environment"].(map[string]any); env["current"] != true || env["custom"] != true || env["workspaceStale"] != true {
		t.Fatalf("expected a current environment the workspace lacks: %v", body)
	}

	code, body = serve(http.MethodPost, h.HandleRebuildWorkspace)
	if ws, _ := body["workspace"].(map[string]any); code != http.StatusOK || ws["kind"] != "interactive" {
		t.Fatalf("unexpected rebuild: %d %v", code, body)
	}
}

func TestEnvironmentForAnOlderImage(t *testing.T) {
	current := &Environment{Build: &Build{Status: BuildSucceeded}, Image: "devpad-project-1:new", Current: true}
	if got := current.For("devpad-project-1:new"); !got.Current || EnvironmentPrompt(got) != "" {
		t.Fatalf("a sandbox on the current image is current: %+v", got)
	}
	older := current.For("devpad-project-1:old")
	if older.Current || !strings.Contains(EnvironmentPrompt(older), "the last version of it that built") {
		t.Fatalf("a sandbox on an older image should be told: %+v\n%s", older, EnvironmentPrompt(older))
	}
	if current.Build.Status != BuildSucceeded {
		t.Fatal("For must not change the environment it was called on")
	}
	base := current.For("")
	if !strings.Contains(EnvironmentPrompt(base), "devpad's own image without it") {
		t.Fatalf("a sandbox on devpad's image should be told:\n%s", EnvironmentPrompt(base))
	}
	var none *Environment
	if none.For("x") != nil {
		t.Fatal("no environment stays none")
	}
}

func TestValidateEnvironmentReadsLinesLikeDocker(t *testing.T) {
	for _, body := range []string{
		"# a comment that ends in a backslash \\\nFROM ubuntu\n",
		"FROM\tubuntu\n",
		"RUN echo one \\\n\n  two\nFROM ubuntu\n",
	} {
		if err := validateEnvironment(body); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("expected FROM found in %q, got %v", body, err)
		}
	}
	if err := validateEnvironment("RUN echo one \\\n\n  FROM is still the run\n"); err != nil {
		t.Errorf("a blank line does not end a continued instruction: %v", err)
	}
}

func TestPruneKeepsWhatABuildJustMade(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})
	first := waitForBuild(t, e, p.ID, BuildSucceeded)

	// A newer build has tagged its image but not yet recorded it.
	gate := make(chan struct{})
	e.containers.mu.Lock()
	e.containers.buildGate = gate
	e.containers.mu.Unlock()
	e.updateEnvironment(t, e.project(t, p.ID), nodeEnvironment+"RUN true\n")
	running, _ := e.svc.repo.GetBuild(ctx, p.ID)
	e.containers.mu.Lock()
	e.containers.images[running.Image] = map[string]string{"devpad.project": strconv.FormatInt(p.ID, 10)}
	e.containers.mu.Unlock()

	e.svc.pruneImages(ctx, p.ID)
	e.containers.mu.Lock()
	_, keptNew := e.containers.images[running.Image]
	_, keptCurrent := e.containers.images[first.Image]
	e.containers.mu.Unlock()
	if !keptNew || !keptCurrent {
		t.Fatalf("prune removed an image in use: new kept %v, current kept %v", keptNew, keptCurrent)
	}
	close(gate)
	second := waitForBuild(t, e, p.ID, BuildSucceeded)

	// The image it replaced stays one more round, for a workspace being
	// made from it.
	e.containers.mu.Lock()
	_, keptPrevious := e.containers.images[first.Image]
	e.containers.mu.Unlock()
	if !keptPrevious || second.Image != running.Image {
		t.Fatalf("expected the previous image kept after the build: %v", keptPrevious)
	}
}

func TestClearingAndRestoringTheEnvironmentQuickly(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj", Environment: nodeEnvironment})
	waitForBuild(t, e, p.ID, BuildSucceeded)
	// Two saves close together: the stored one decides.
	e.updateEnvironment(t, e.project(t, p.ID), "")
	e.updateEnvironment(t, e.project(t, p.ID), nodeEnvironment)
	b := waitForBuild(t, e, p.ID, BuildSucceeded)
	if got := e.project(t, p.ID).EnvironmentImage; got != b.Image {
		t.Fatalf("expected the restored environment's image recorded, got %q for %+v", got, b)
	}
}
