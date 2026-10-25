package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/workspace"
)

// standalone adds a running standalone workspace for the test user.
func (e *testEnv) standalone() *workspace.Workspace {
	return e.workspaces.add(&workspace.Workspace{UserID: e.userID, Name: "mine", Kind: workspace.KindInteractive, Status: workspace.StatusRunning})
}

func (e *testEnv) convert(wsID int64, in ConvertInput) (*ConvertResult, error) {
	e.svc.repo = &tokenCapturingRepo{Repository: NewRepository(e.db.Conn()), agent: e.repoAgent}
	return e.svc.ConvertWorkspace(context.Background(), e.userID, wsID, in)
}

func (e *testEnv) projectCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.Conn().QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
		t.Fatalf("counting projects: %v", err)
	}
	return n
}

func TestConvertWorkspaceInPlace(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()

	result, err := e.convert(ws.ID, ConvertInput{
		RepoPath: ".", Name: "App", DefaultBranch: "trunk", UpstreamURL: "git@github.com:o/app.git", SetupScript: "make",
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	p := result.Project
	if p.Status != StatusReady || p.Name != "App" || p.DefaultBranch != "trunk" || p.UpstreamURL != "git@github.com:o/app.git" {
		t.Fatalf("unexpected project: %+v", p)
	}
	// The canonical repo starts empty and records the upstream for pushes.
	if e.repoAgent.initReq["empty"] != true || e.repoAgent.initReq["upstreamUrl"] != "git@github.com:o/app.git" {
		t.Fatalf("unexpected init request: %v", e.repoAgent.initReq)
	}
	if !slices.Equal(e.repoAgent.initialImports, []string{"trunk <- all-of-."}) {
		t.Fatalf("unexpected initial imports: %v", e.repoAgent.initialImports)
	}
	if result.Workspace == nil || result.Workspace.ID != ws.ID || result.Workspace.ProjectID != p.ID || !result.Workspace.RepoSeeded {
		t.Fatalf("workspace not attached: %+v", result.Workspace)
	}
	if result.SSH == nil || !slices.Equal(result.SSH.Removed, []string{"~/.ssh/id_ed25519"}) {
		t.Fatalf("unexpected SSH cleanup: %+v", result.SSH)
	}
	// The workspace is already set up, so the setup script does not run.
	if len(e.workspaces.commands) != 0 {
		t.Fatalf("setup script ran in the converted workspace: %v", e.workspaces.commands)
	}

	// Opening the project's workspace now returns the converted one.
	opened, created, err := e.svc.OpenWorkspace(context.Background(), e.userID, p.ID)
	if err != nil || created || opened.ID != ws.ID {
		t.Fatalf("open after convert: %+v created=%v err=%v", opened, created, err)
	}

	if _, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "Again"}); !errors.Is(err, workspace.ErrNotStandalone) {
		t.Fatalf("expected ErrNotStandalone converting twice, got %v", err)
	}
}

func TestConvertWorkspaceFromContents(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()

	result, err := e.convert(ws.ID, ConvertInput{Name: "Site", IncludeSensitive: true})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !slices.Equal(e.workspaces.initCalls, []string{"main sensitive=true"}) {
		t.Fatalf("unexpected init calls: %v", e.workspaces.initCalls)
	}
	if !slices.Equal(e.workspaces.bundledAll, []string{"."}) || result.Workspace == nil {
		t.Fatalf("expected the new root repo imported and attached: bundled=%v result=%+v", e.workspaces.bundledAll, result)
	}
	if len(e.workspaces.undone) != 0 {
		t.Fatalf("a successful conversion undid its repo: %v", e.workspaces.undone)
	}
}

func TestConvertWorkspaceSubdirectoryLeavesWorkspaceAlone(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()

	result, err := e.convert(ws.ID, ConvertInput{RepoPath: "src/app/", Name: "App"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if result.Workspace != nil || result.SSH != nil || result.Project.Status != StatusReady {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !slices.Equal(e.workspaces.bundledAll, []string{"src/app"}) {
		t.Fatalf("bundled %v", e.workspaces.bundledAll)
	}
	if got, _ := e.workspaces.Get(context.Background(), e.userID, ws.ID); got.ProjectID != 0 {
		t.Fatalf("the workspace was attached: %+v", got)
	}
	// The project gets a fresh workspace when first opened.
	opened, created, err := e.svc.OpenWorkspace(context.Background(), e.userID, result.Project.ID)
	if err != nil || !created || opened.ID == ws.ID {
		t.Fatalf("open after convert: %+v created=%v err=%v", opened, created, err)
	}
}

func TestConvertWorkspaceCleansUpOnFailure(t *testing.T) {
	cases := []struct {
		name     string
		repoPath string
		setup    func(e *testEnv)
	}{
		{"repo init fails", ".", func(e *testEnv) { e.repoAgent.initErr = "disk full" }},
		{"bundling fails", ".", func(e *testEnv) { e.workspaces.convertErr["bundle"] = errors.New("agent gone") }},
		{"import fails", ".", func(e *testEnv) { e.repoAgent.initialErr = "bad bundle" }},
		{"attach fails", ".", func(e *testEnv) { e.workspaces.convertErr["attach"] = errors.New("disk full") }},
		{"import fails after making a repo from files", "", func(e *testEnv) { e.repoAgent.initialErr = "bad bundle" }},
		{"attach fails after making a repo from files", "", func(e *testEnv) { e.workspaces.convertErr["attach"] = errors.New("disk full") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setupTest(t)
			ws := e.standalone()
			c.setup(e)

			if _, err := e.convert(ws.ID, ConvertInput{RepoPath: c.repoPath, Name: "App"}); err == nil {
				t.Fatal("expected the conversion to fail")
			}
			if n := e.projectCount(t); n != 0 {
				t.Fatalf("%d projects left behind", n)
			}
			if len(e.containers.created) != 1 || len(e.containers.removed) != 1 || len(e.containers.volumes) != 0 || len(e.containers.networks) != 0 {
				t.Fatalf("repo container not cleaned up: created=%v removed=%v volumes=%v networks=%v",
					e.containers.created, e.containers.removed, e.containers.volumes, e.containers.networks)
			}
			if got, _ := e.workspaces.Get(context.Background(), e.userID, ws.ID); got.ProjectID != 0 || got.RepoSeeded {
				t.Fatalf("workspace changed: %+v", got)
			}
			wantUndo := []string(nil)
			if c.repoPath == "" {
				wantUndo = []string{"init-sha"}
			}
			if !slices.Equal(e.workspaces.undone, wantUndo) {
				t.Fatalf("undone = %v, want %v", e.workspaces.undone, wantUndo)
			}
			if e.workspaces.converting[ws.ID] {
				t.Fatal("the workspace is still marked as converting")
			}
		})
	}
}

func TestConvertWorkspaceFailsCleanlyWhenFilesCannotBecomeARepo(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.workspaces.convertErr["init"] = &agent.AgentError{StatusCode: http.StatusRequestEntityTooLarge, Message: "too big"}

	_, err := e.convert(ws.ID, ConvertInput{Name: "Site"})
	var agentErr *agent.AgentError
	if !errors.As(err, &agentErr) || agentErr.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected the agent's refusal, got %v", err)
	}
	if e.projectCount(t) != 0 || len(e.containers.created) != 0 {
		t.Fatal("a project was made anyway")
	}
}

func TestConvertWorkspaceRefusals(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()

	for _, in := range []ConvertInput{
		{RepoPath: ".", Name: " "},
		{RepoPath: ".", Name: "App", DefaultBranch: "bad..branch"},
		{RepoPath: ".", Name: "App", UpstreamURL: "https://user:token@github.com/o/r.git"},
		{RepoPath: ".", Name: "App", UpstreamURL: "https://ghp_x@github.com/o/r.git"},
		{RepoPath: ".", Name: "App", UpstreamURL: "--upload-pack=x"},
		{RepoPath: "../elsewhere", Name: "App"},
		{RepoPath: "/etc", Name: "App"},
	} {
		if _, err := e.convert(ws.ID, in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("convert %+v: expected ErrInvalidInput, got %v", in, err)
		}
	}

	e.workspaces.discovery = &agent.RepoDiscovery{Repos: []agent.DiscoveredRepo{{Path: ".", HasDevpadRemote: true}}}
	if _, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "App"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected a repo with a devpad remote refused, got %v", err)
	}
	e.workspaces.discovery = nil

	e.workspaces.setStatus(ws.ID, workspace.StatusStopped)
	if _, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "App"}); !errors.Is(err, workspace.ErrNotRunning) {
		t.Fatalf("expected ErrNotRunning, got %v", err)
	}
	e.workspaces.setStatus(ws.ID, workspace.StatusRunning)

	sandbox := e.workspaces.add(&workspace.Workspace{UserID: e.userID, ProjectID: 9, Kind: workspace.KindTask, Branch: "x", Status: workspace.StatusRunning})
	if _, err := e.convert(sandbox.ID, ConvertInput{RepoPath: ".", Name: "App"}); !errors.Is(err, workspace.ErrNotStandalone) {
		t.Fatalf("expected ErrNotStandalone for a sandbox, got %v", err)
	}
	if e.projectCount(t) != 0 {
		t.Fatal("a refused conversion made a project")
	}
}

func TestConvertWorkspaceKeepsProjectClosedUntilAttached(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	var during error
	e.workspaces.onBundleAll = func() {
		var p *Project
		if projects, _ := e.svc.List(context.Background(), e.userID); len(projects) == 1 {
			p = projects[0]
		}
		if p == nil {
			during = fmt.Errorf("project not listed during conversion")
			return
		}
		if _, _, err := e.svc.OpenWorkspace(context.Background(), e.userID, p.ID); !errors.Is(err, ErrNotReady) {
			during = fmt.Errorf("open during conversion: %v", err)
		}
		if _, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{}); !errors.Is(err, ErrNotReady) {
			during = fmt.Errorf("sandbox during conversion: %v", err)
		}
	}

	if _, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "App"}); err != nil {
		t.Fatalf("convert: %v", err)
	}
	if during != nil {
		t.Fatal(during)
	}
}

func TestConvertHandlers(t *testing.T) {
	e := setupTest(t)
	e.svc.repo = &tokenCapturingRepo{Repository: NewRepository(e.db.Conn()), agent: e.repoAgent}
	h := NewHandler(e.svc)
	ws := e.standalone()
	user := &auth.User{ID: e.userID}
	call := func(handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.SetPathValue("id", fmt.Sprint(ws.ID))
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, user))
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	rec := call(h.HandleDiscoverConversion, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path":"."`) {
		t.Fatalf("discover: %d %s", rec.Code, rec.Body)
	}

	e.workspaces.converting[ws.ID] = true
	if rec := call(h.HandleConvertWorkspace, http.MethodPost, "/", `{"repoPath":".","name":"App"}`); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 mid-conversion, got %d %s", rec.Code, rec.Body)
	}
	delete(e.workspaces.converting, ws.ID)

	rec = call(h.HandleConvertWorkspace, http.MethodPost, "/", `{"repoPath":".","name":"App","defaultBranch":"main"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("convert: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Project   map[string]any    `json:"project"`
		Workspace map[string]any    `json:"workspace"`
		SSH       *agent.SSHCleanup `json:"ssh"`
	}
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body.Project["status"] != "ready" || body.Workspace["id"] != float64(ws.ID) || body.SSH == nil {
		t.Fatalf("unexpected response: %s", rec.Body)
	}
}

func (e *testEnv) giveUserKey(t *testing.T) {
	t.Helper()
	if _, err := e.db.Conn().Exec(`UPDATE users SET ssh_private_key = 'encrypted', ssh_public_key = 'ssh-ed25519 OLDKEY' WHERE id = ?`, e.userID); err != nil {
		t.Fatalf("setting key: %v", err)
	}
}

var (
	ghToken = agent.CredentialItem{ID: "gh-token:~/.config/gh/hosts.yml", Kind: "gh-token", Location: "~/.config/gh/hosts.yml"}
	netrc   = agent.CredentialItem{ID: "netrc:~/.netrc", Kind: "netrc", Location: "~/.netrc"}
)

func TestConvertWorkspaceRefusesCredentialsWithoutConsent(t *testing.T) {
	for _, repoPath := range []string{".", ""} {
		t.Run("repo "+repoPath, func(t *testing.T) {
			e := setupTest(t)
			ws := e.standalone()
			e.workspaces.discovery = &agent.RepoDiscovery{
				Repos:       []agent.DiscoveredRepo{{Path: ".", Branch: "main"}},
				Credentials: []agent.CredentialItem{ghToken, netrc},
			}
			if repoPath == "" {
				e.workspaces.discovery.Repos = nil
			}

			_, err := e.convert(ws.ID, ConvertInput{RepoPath: repoPath, Name: "App", StripCredentials: []string{ghToken.ID}})
			var credErr *workspace.CredentialsError
			if !errors.As(err, &credErr) || !slices.Equal(credErr.Items, []agent.CredentialItem{netrc}) {
				t.Fatalf("expected the unconsented netrc refused, got %v", err)
			}
			if e.projectCount(t) != 0 || len(e.containers.created) != 0 || len(e.workspaces.initCalls) != 0 || len(e.workspaces.stripped) != 0 {
				t.Fatal("a refused conversion changed something")
			}
			if e.workspaces.converting[ws.ID] {
				t.Fatal("the workspace is still marked as converting")
			}
		})
	}
}

func TestConvertWorkspaceSubdirectoryIgnoresCredentials(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.giveUserKey(t)
	e.workspaces.discovery = &agent.RepoDiscovery{
		Repos:       []agent.DiscoveredRepo{{Path: "src/app", Branch: "main"}},
		Credentials: []agent.CredentialItem{netrc},
	}

	result, err := e.convert(ws.ID, ConvertInput{RepoPath: "src/app", Name: "App"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// The old workspace stays standalone, so nothing is stripped, restarted
	// or rotated.
	if len(e.workspaces.stripped) != 0 || len(e.workspaces.restarted) != 0 || len(e.keys.rotated) != 0 || result.KeyRotated {
		t.Fatalf("stripped=%v restarted=%v rotated=%v", e.workspaces.stripped, e.workspaces.restarted, e.keys.rotated)
	}
}

func TestConvertWorkspaceStripsRestartsAndRotates(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.giveUserKey(t)
	e.workspaces.discovery = &agent.RepoDiscovery{
		Repos:       []agent.DiscoveredRepo{{Path: ".", Branch: "main"}},
		Credentials: []agent.CredentialItem{ghToken, netrc},
	}
	var during error
	e.keys.onRotate = func() {
		// The key is rotated last: the workspace is attached and restarted
		// and the project is ready.
		got, _ := e.workspaces.Get(context.Background(), e.userID, ws.ID)
		projects, _ := e.svc.List(context.Background(), e.userID)
		if got.ProjectID == 0 || len(e.workspaces.restarted) != 1 || len(projects) != 1 || projects[0].Status != StatusReady {
			during = fmt.Errorf("rotated too early: workspace=%+v restarted=%v projects=%+v", got, e.workspaces.restarted, projects)
		}
	}

	consent := []string{ghToken.ID, netrc.ID}
	result, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "App", StripCredentials: consent})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if during != nil {
		t.Fatal(during)
	}
	if len(e.workspaces.stripped) != 1 || !slices.Equal(e.workspaces.stripped[0], consent) || len(result.Stripped) != 2 {
		t.Fatalf("stripped %v, result %+v", e.workspaces.stripped, result.Stripped)
	}
	if !slices.Equal(e.workspaces.steps, []string{"attach", "restart"}) || !slices.Equal(e.workspaces.restarted, []int64{ws.ID}) {
		t.Fatalf("steps = %v, restarted = %v", e.workspaces.steps, e.workspaces.restarted)
	}
	if !result.KeyRotated || result.PublicKey != "ssh-ed25519 NEWKEY devpad-owner" || !slices.Equal(e.keys.rotated, []int64{e.userID}) {
		t.Fatalf("key not rotated: %+v", result)
	}
}

func TestConvertWorkspaceWithoutKeyDoesNotRotate(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()

	result, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "App"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if result.KeyRotated || result.RotationError != "" || len(e.keys.rotated) != 0 {
		t.Fatalf("rotated a key the workspace never held: %+v", result)
	}
	if !slices.Equal(e.workspaces.restarted, []int64{ws.ID}) {
		t.Fatalf("restarted %v", e.workspaces.restarted)
	}
}

func TestConvertWorkspaceReportsRestartAndRotationFailures(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.giveUserKey(t)
	e.workspaces.restartErr = errors.New("docker gone")
	e.keys.err = errors.New("disk full")

	result, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "App"})
	if err != nil {
		t.Fatalf("a failed restart or rotation must not undo the conversion: %v", err)
	}
	if result.Workspace == nil || result.Project.Status != StatusReady {
		t.Fatalf("conversion did not stand: %+v", result)
	}
	if result.RestartError != "docker gone" || result.KeyRotated || result.RotationError != "disk full" {
		t.Fatalf("failures not reported: %+v", result)
	}
}

func TestConvertWorkspaceCleansUpAfterItsDeadline(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.svc.convertTimeout = 100 * time.Millisecond
	e.workspaces.convertErr["bundle"] = errors.New("agent stalled")
	e.workspaces.onBundleAll = func() { time.Sleep(200 * time.Millisecond) }

	if _, err := e.convert(ws.ID, ConvertInput{Name: "Site"}); err == nil {
		t.Fatal("expected the conversion to fail")
	}
	if n := e.projectCount(t); n != 0 {
		t.Fatalf("%d projects left behind after the deadline", n)
	}
	if len(e.containers.removed) != 1 || len(e.containers.volumes) != 0 {
		t.Fatalf("repo container left behind: removed=%v volumes=%v", e.containers.removed, e.containers.volumes)
	}
	if !slices.Equal(e.workspaces.undone, []string{"init-sha"}) {
		t.Fatalf("repo made from files not undone: %v", e.workspaces.undone)
	}
}

func TestConvertWorkspaceRecordsRepoContainerAndHoldsOffDelete(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	var during error
	e.workspaces.onBundleAll = func() {
		projects, _ := e.svc.List(context.Background(), e.userID)
		if len(projects) != 1 || projects[0].RepoContainerID == "" {
			during = fmt.Errorf("repo container not recorded during conversion: %+v", projects)
			return
		}
		if err := e.svc.Delete(context.Background(), e.userID, projects[0].ID); !errors.Is(err, ErrBusy) {
			during = fmt.Errorf("delete during conversion: %v", err)
		}
	}

	result, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "App"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if during != nil {
		t.Fatal(during)
	}
	// Once converted, Delete removes the repo container it reads afresh.
	if err := e.svc.Delete(context.Background(), e.userID, result.Project.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !slices.Equal(e.containers.removed, []string{result.Project.RepoContainerID}) {
		t.Fatalf("removed %v, want the repo container %q", e.containers.removed, result.Project.RepoContainerID)
	}
}

func TestConvertHandlerListsCredentialsToStrip(t *testing.T) {
	e := setupTest(t)
	e.svc.repo = &tokenCapturingRepo{Repository: NewRepository(e.db.Conn()), agent: e.repoAgent}
	ws := e.standalone()
	e.workspaces.discovery = &agent.RepoDiscovery{
		Repos:       []agent.DiscoveredRepo{{Path: ".", Branch: "main"}},
		Credentials: []agent.CredentialItem{netrc},
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"repoPath":".","name":"App"}`))
	req.SetPathValue("id", fmt.Sprint(ws.ID))
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: e.userID}))
	rec := httptest.NewRecorder()
	NewHandler(e.svc).HandleConvertWorkspace(rec, req)

	var body struct {
		Error       string                 `json:"error"`
		Credentials []agent.CredentialItem `json:"credentials"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusConflict || !slices.Equal(body.Credentials, []agent.CredentialItem{netrc}) || !strings.Contains(body.Error, "~/.netrc") {
		t.Fatalf("unexpected response %d: %s", rec.Code, rec.Body)
	}
}
