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
