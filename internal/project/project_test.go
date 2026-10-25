package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/agentbin"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/container"
	appdb "github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/workspace"
)

// fakeContainers records the Docker resources a project creates and removes.
type fakeContainers struct {
	mu       sync.Mutex
	ip       string
	created  []string
	networks map[string]bool
	volumes  map[string]bool
	removed  []string
	copied   []string
	// onCopy runs when a file is copied into a container, standing in for
	// the agent binary being replaced.
	onCopy func()
}

func newFakeContainers(ip string) *fakeContainers {
	return &fakeContainers{ip: ip, networks: map[string]bool{}, volumes: map[string]bool{}}
}

func (f *fakeContainers) Create(_ context.Context, name, volumeName, networkName string, env []string, _, _ int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, fmt.Sprintf("%s vol=%s net=%s", name, volumeName, networkName))
	return "container-" + name, nil
}
func (f *fakeContainers) Start(context.Context, string) error   { return nil }
func (f *fakeContainers) Stop(context.Context, string) error    { return nil }
func (f *fakeContainers) Restart(context.Context, string) error { return nil }
func (f *fakeContainers) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return nil
}
func (f *fakeContainers) UpdateResources(context.Context, string, int64, int64) error { return nil }
func (f *fakeContainers) GetIP(context.Context, string, string) (string, error)       { return f.ip, nil }
func (f *fakeContainers) GetEnv(context.Context, string) ([]string, error)            { return nil, nil }
func (f *fakeContainers) Stats(context.Context, string) (*container.ContainerStats, error) {
	return &container.ContainerStats{}, nil
}
func (f *fakeContainers) CreateNetwork(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networks[name] = true
	return nil
}
func (f *fakeContainers) RemoveNetwork(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.networks, name)
	return nil
}
func (f *fakeContainers) ConnectToNetwork(context.Context, string, string) error { return nil }
func (f *fakeContainers) EnsureSelfAttached(context.Context, string) error       { return nil }
func (f *fakeContainers) DetachSelf(context.Context, string) error               { return nil }
func (f *fakeContainers) CreateVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumes[name] = true
	return nil
}
func (f *fakeContainers) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.volumes, name)
	return nil
}
func (f *fakeContainers) Exec(context.Context, string, []string) (string, error) { return "", nil }
func (f *fakeContainers) ExecAttach(context.Context, string) (container.HijackedResponse, error) {
	return container.HijackedResponse{}, nil
}
func (f *fakeContainers) ExecResize(context.Context, string, uint, uint) error { return nil }
func (f *fakeContainers) CopyFileToContainer(_ context.Context, id, path string, _ []byte, _ int64) error {
	f.mu.Lock()
	f.copied = append(f.copied, id+":"+path)
	onCopy := f.onCopy
	f.mu.Unlock()
	if onCopy != nil {
		onCopy()
	}
	return nil
}
func (f *fakeContainers) BuildImage(context.Context, []byte, []byte, string) error { return nil }
func (f *fakeContainers) CreateSidecar(context.Context, string, string, string, string, []string, int) (string, error) {
	return "", nil
}

// fakeRepoAgent stands in for the agent in a project's repo container.
type fakeRepoAgent struct {
	mu        sync.Mutex
	version   string // reported agent version; empty means current
	token     string
	initErr   string
	initReq   map[string]any
	branches  []agent.RepoRef
	ahead     map[string]int // head branch -> commits ahead of base
	merges    []agent.RepoMergeRequest
	mergeErr  map[string]any // error body returned with 409 when set
	pushed    []string
	bundled   []string // branches requested as bundles
	imported  map[string][]byte
	importErr string
	// importStatus is the status returned with importErr; 400 when unset.
	importStatus int
	// importedFF lists branches imported fast-forward only.
	importedFF []string
	// initialImports records each initial import's default branch and bundle.
	initialImports []string
	initialErr     string
}

func (a *fakeRepoAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		w.Write([]byte(`{"status":"ok"}`))
		return
	case "/api/version":
		a.mu.Lock()
		version := a.version
		a.mu.Unlock()
		if version == "" {
			version = agentbin.Version
		}
		json.NewEncoder(w).Encode(map[string]string{"version": version})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token == "" || r.Header.Get("X-Devpad-Agent-Token") != a.token {
		http.Error(w, `{"error":"invalid agent token"}`, http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/api/repo/init":
		json.NewDecoder(r.Body).Decode(&a.initReq)
		if a.initErr != "" {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": a.initErr})
			return
		}
		branch, _ := a.initReq["defaultBranch"].(string)
		json.NewEncoder(w).Encode(agent.RepoRefs{DefaultBranch: branch})
	case "/api/repo/refs":
		json.NewEncoder(w).Encode(agent.RepoRefs{DefaultBranch: "main", Branches: a.branches})
	case "/api/repo/bundle":
		branch := r.URL.Query().Get("branch")
		a.bundled = append(a.bundled, branch)
		w.Write([]byte("bundle-of-" + branch))
	case "/api/repo/diff":
		q := r.URL.Query()
		json.NewEncoder(w).Encode(agent.RepoDiff{BaseSHA: q.Get("base"), HeadSHA: q.Get("head"), Files: []agent.RepoDiffFile{{Path: "a.txt", Status: "A", Additions: 1}}})
	case "/api/repo/file":
		w.Write([]byte("<script>alert(1)</script> at " + r.URL.Query().Get("rev")))
	case "/api/repo/merge":
		var req agent.RepoMergeRequest
		json.NewDecoder(r.Body).Decode(&req)
		a.merges = append(a.merges, req)
		if a.mergeErr != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(a.mergeErr)
			return
		}
		json.NewEncoder(w).Encode(agent.RepoMergeResult{Target: req.Target, SHA: "merged", Strategy: req.Strategy})
	case "/api/repo/push":
		a.pushed = append(a.pushed, r.URL.Query().Get("branch"))
		json.NewEncoder(w).Encode(agent.RepoBranch{Branch: r.URL.Query().Get("branch"), SHA: "pushed"})
	case "/api/repo/compare":
		q := r.URL.Query()
		json.NewEncoder(w).Encode(agent.RepoComparison{Base: q.Get("base"), Head: q.Get("head"), Ahead: a.ahead[q.Get("head")]})
	case "/api/repo/import-initial":
		if a.initialErr != "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": a.initialErr})
			return
		}
		body, _ := io.ReadAll(r.Body)
		branch := r.URL.Query().Get("defaultBranch")
		a.initialImports = append(a.initialImports, branch+" <- "+string(body))
		json.NewEncoder(w).Encode(agent.RepoRefs{DefaultBranch: branch, Branches: []agent.RepoRef{{Name: branch, SHA: "abc"}}})
	case "/api/repo/import":
		if a.importErr != "" {
			status := a.importStatus
			if status == 0 {
				status = http.StatusBadRequest
			}
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": a.importErr})
			return
		}
		body, _ := io.ReadAll(r.Body)
		branch := r.URL.Query().Get("branch")
		a.imported[branch] = body
		if r.URL.Query().Get("ffOnly") == "true" {
			a.importedFF = append(a.importedFF, branch)
		}
		json.NewEncoder(w).Encode(agent.RepoBranch{Branch: branch, SHA: "abc123"})
	default:
		http.NotFound(w, r)
	}
}

// fakeWorkspaces is an in-memory workspace service for sandboxes.
type fakeWorkspaces struct {
	mu            sync.Mutex
	nextID        int64
	byID          map[int64]*workspace.Workspace
	seeds         map[int64]string // workspace ID -> base branch + bundle seen
	seedErr       error
	deleteErr     error
	commands      map[int64]string
	deleted       []int64
	sandboxBundle string
	bundled       []string // project workspace branches bundled

	// Conversion of standalone workspaces.
	converting  map[int64]bool
	convertErr  map[string]error // by step: init, bundle, attach
	discovery   *agent.RepoDiscovery
	initCalls   []string
	bundledAll  []string
	undone      []string
	onBundleAll func()
	stripped    [][]string
	restarted   []int64
	restartErr  error
	// steps records the conversion steps in order.
	steps []string
}

func newFakeWorkspaces() *fakeWorkspaces {
	return &fakeWorkspaces{
		nextID: 100, byID: map[int64]*workspace.Workspace{}, seeds: map[int64]string{}, commands: map[int64]string{},
		converting: map[int64]bool{}, convertErr: map[string]error{},
	}
}

func (f *fakeWorkspaces) add(ws *workspace.Workspace) *workspace.Workspace {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	ws.ID = f.nextID
	f.byID[ws.ID] = ws
	return ws
}

func (f *fakeWorkspaces) Get(_ context.Context, userID, id int64) (*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ws, ok := f.byID[id]
	if !ok {
		return nil, workspace.ErrNotFound
	}
	if ws.UserID != userID {
		return nil, workspace.ErrForbidden
	}
	return ws, nil
}

func (f *fakeWorkspaces) CreateSandbox(_ context.Context, userID int64, spec workspace.SandboxSpec) (*workspace.Workspace, error) {
	return f.add(&workspace.Workspace{
		UserID: userID, Name: spec.Name, ProjectID: spec.ProjectID, Kind: spec.Kind, Branch: spec.Branch,
		Status: workspace.StatusRunning,
	}), nil
}

func (f *fakeWorkspaces) CreateProjectWorkspace(_ context.Context, userID int64, spec workspace.ProjectWorkspaceSpec) (*workspace.Workspace, error) {
	f.mu.Lock()
	for _, ws := range f.byID {
		if ws.ProjectID == spec.ProjectID && ws.IsProjectWorkspace() {
			f.mu.Unlock()
			return nil, fmt.Errorf("inserting workspace: UNIQUE constraint failed")
		}
	}
	f.mu.Unlock()
	return f.add(&workspace.Workspace{
		UserID: userID, Name: spec.Name, ProjectID: spec.ProjectID, Kind: workspace.KindInteractive,
		Status: workspace.StatusRunning,
	}), nil
}

func (f *fakeWorkspaces) CheckoutProjectBranch(_ context.Context, _, id int64, branch string, bundle io.Reader) (*agent.RepoCheckout, error) {
	body, _ := io.ReadAll(bundle)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seeds[id] = "checkout " + branch + " <- " + string(body)
	return &agent.RepoCheckout{Branch: branch, Upstream: "devpad/" + branch}, nil
}

func (f *fakeWorkspaces) BundleProjectBranch(_ context.Context, _, id int64, branch string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bundled = append(f.bundled, branch)
	return io.NopCloser(strings.NewReader("workspace-bundle-of-" + branch)), nil
}

func (f *fakeWorkspaces) ListByProject(_ context.Context, userID, projectID int64) ([]*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*workspace.Workspace
	for _, ws := range f.byID {
		if ws.ProjectID == projectID && ws.UserID == userID {
			out = append(out, ws)
		}
	}
	return out, nil
}

func (f *fakeWorkspaces) SeedSandboxRepo(ctx context.Context, _, id int64, base string, bundle io.Reader) (*agent.RepoBranch, error) {
	body, _ := io.ReadAll(bundle)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.seedErr != nil {
		return nil, f.seedErr
	}
	f.seeds[id] = base + " <- " + string(body)
	f.byID[id].RepoSeeded = true
	return &agent.RepoBranch{Branch: f.byID[id].Branch}, nil
}

func (f *fakeWorkspaces) BundleSandboxBranch(_ context.Context, _, id int64) (string, io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byID[id].Branch, io.NopCloser(strings.NewReader(f.sandboxBundle)), nil
}

func (f *fakeWorkspaces) FetchSandboxBase(_ context.Context, _, id int64, branch string, bundle io.Reader) (*agent.RepoBranch, error) {
	body, _ := io.ReadAll(bundle)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seeds[id] = "devpad/" + branch + " <- " + string(body)
	return &agent.RepoBranch{Branch: "devpad/" + branch}, nil
}

func (f *fakeWorkspaces) Delete(_ context.Context, _, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.byID, id)
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeWorkspaces) Start(_ context.Context, _, id int64) (*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[id].Status = workspace.StatusRunning
	return f.byID[id], nil
}

func (f *fakeWorkspaces) setStatus(id int64, status workspace.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[id].Status = status
}

func (f *fakeWorkspaces) StartCommand(_ context.Context, _, id int64, command, _ string) (*agent.ManagedCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[id] = command
	return &agent.ManagedCommand{}, nil
}

type testEnv struct {
	svc        *service
	db         *appdb.DB
	containers *fakeContainers
	repoAgent  *fakeRepoAgent
	workspaces *fakeWorkspaces
	keys       *fakeKeys
	userID     int64
	otherID    int64
}

// fakeKeys stands in for the settings service that rotates SSH keys.
type fakeKeys struct {
	mu      sync.Mutex
	rotated []int64
	err     error
	// onRotate runs before rotating, to observe the conversion's state.
	onRotate func()
}

func (k *fakeKeys) GenerateSSHKey(_ context.Context, userID int64) (string, error) {
	k.mu.Lock()
	hook := k.onRotate
	k.mu.Unlock()
	if hook != nil {
		hook()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return "", k.err
	}
	k.rotated = append(k.rotated, userID)
	return "ssh-ed25519 NEWKEY devpad-owner", nil
}

func setupTest(t *testing.T) *testEnv {
	t.Helper()
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ra := &fakeRepoAgent{imported: map[string][]byte{}}
	srv := httptest.NewServer(ra)
	t.Cleanup(srv.Close)
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	containers := newFakeContainers(host)
	workspaces := newFakeWorkspaces()
	keys := &fakeKeys{}
	svc := NewService(NewRepository(db.Conn()), containers, workspaces, auth.NewUserRepository(db.Conn()), nil, keys).(*service)
	svc.agentPort = port

	return &testEnv{
		svc: svc, db: db, containers: containers, repoAgent: ra, workspaces: workspaces, keys: keys,
		userID: insertUser(t, db, "owner"), otherID: insertUser(t, db, "other"),
	}
}

func insertUser(t *testing.T, db *appdb.DB, name string) int64 {
	t.Helper()
	res, err := db.Conn().Exec(`INSERT INTO users (username, email, password) VALUES (?, ?, 'x')`, name, name+"@example.com")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// createProject creates a ready project, wiring the fake repo agent to the
// token the service generated.
func (e *testEnv) createProject(t *testing.T, in CreateInput) *Project {
	t.Helper()
	e.svc.repo = &tokenCapturingRepo{Repository: NewRepository(e.db.Conn()), agent: e.repoAgent}
	p, err := e.svc.Create(context.Background(), e.userID, in)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

// tokenCapturingRepo hands the generated repo agent token to the fake agent
// as soon as the project row is inserted, like the env var on a real container.
type tokenCapturingRepo struct {
	Repository
	agent *fakeRepoAgent
}

func (r *tokenCapturingRepo) Create(ctx context.Context, p *Project) error {
	r.agent.mu.Lock()
	r.agent.token = p.RepoAgentToken
	r.agent.mu.Unlock()
	return r.Repository.Create(ctx, p)
}

func TestCreateProvisionsIsolatedRepoContainer(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "  Devpad ", UpstreamURL: "https://example.com/repo.git"})

	if p.Status != StatusReady || p.Error != "" {
		t.Fatalf("expected ready project, got %s %q", p.Status, p.Error)
	}
	if p.Name != "Devpad" || p.DefaultBranch != DefaultBranch || p.MaxSandboxes != DefaultMaxSandboxes {
		t.Fatalf("unexpected defaults: %+v", p)
	}
	want := fmt.Sprintf("repo-%d vol=devpad-repo-vol-%d net=devpad-repo-net-%d", p.ID, p.ID, p.ID)
	if len(e.containers.created) != 1 || e.containers.created[0] != want {
		t.Fatalf("created %v, want [%s]", e.containers.created, want)
	}
	if e.repoAgent.initReq["defaultBranch"] != "main" || e.repoAgent.initReq["upstreamUrl"] != "https://example.com/repo.git" {
		t.Fatalf("unexpected init request: %v", e.repoAgent.initReq)
	}

	stored, err := e.svc.Get(context.Background(), e.userID, p.ID)
	if err != nil || stored.Status != StatusReady || stored.RepoContainerID == "" {
		t.Fatalf("stored project not ready: %+v, %v", stored, err)
	}
}

func TestCreateUpdatesOutdatedRepoAgentBeforeInit(t *testing.T) {
	e := setupTest(t)
	e.repoAgent.version = "old"
	e.containers.onCopy = func() {
		e.repoAgent.mu.Lock()
		e.repoAgent.version = ""
		e.repoAgent.mu.Unlock()
	}

	p := e.createProject(t, CreateInput{Name: "proj"})
	if p.Status != StatusReady {
		t.Fatalf("expected ready project, got %s: %s", p.Status, p.Error)
	}
	if len(e.containers.copied) != 1 || !strings.HasSuffix(e.containers.copied[0], ":usr/local/bin/devpad-agent") {
		t.Fatalf("expected the agent binary replaced before init, got %v", e.containers.copied)
	}
	if e.repoAgent.initReq == nil {
		t.Fatal("repo init never reached the updated agent")
	}
}

func TestCreateRecordsRepoInitFailure(t *testing.T) {
	e := setupTest(t)
	e.repoAgent.initErr = "cloning upstream: repository not found"
	p := e.createProject(t, CreateInput{Name: "broken", UpstreamURL: "git@example.com:missing.git"})

	if p.Status != StatusError || !strings.Contains(p.Error, "repository not found") {
		t.Fatalf("expected recorded error, got %s %q", p.Status, p.Error)
	}
	if _, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("expected ErrNotReady for sandbox on broken project, got %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	tests := []struct {
		name string
		in   CreateInput
	}{
		{"empty name", CreateInput{Name: " "}},
		{"flag branch", CreateInput{Name: "x", DefaultBranch: "-main"}},
		{"bad branch", CreateInput{Name: "x", DefaultBranch: "a..b"}},
		{"flag upstream", CreateInput{Name: "x", UpstreamURL: "--upload-pack=evil"}},
		{"too many sandboxes", CreateInput{Name: "x", MaxSandboxes: MaxSandboxesLimit + 1}},
		{"negative sandboxes", CreateInput{Name: "x", MaxSandboxes: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupTest(t)
			if _, err := e.svc.Create(context.Background(), e.userID, tt.in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
			if len(e.containers.created) != 0 {
				t.Fatalf("invalid input created containers: %v", e.containers.created)
			}
		})
	}
}

func TestGetChecksOwnership(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "mine"})
	if _, err := e.svc.Get(context.Background(), e.otherID, p.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if _, err := e.svc.Get(context.Background(), e.userID, p.ID+99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateSandboxSeedsFromDefaultBranch(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj", SetupScript: "npm ci"})

	ws, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if ws.Kind != workspace.KindTask || !strings.HasPrefix(ws.Branch, "sandbox-") || ws.ProjectID != p.ID {
		t.Fatalf("unexpected sandbox: %+v", ws)
	}
	if got := e.workspaces.seeds[ws.ID]; got != "main <- bundle-of-main" {
		t.Fatalf("seed = %q", got)
	}
	if got := e.workspaces.commands[ws.ID]; got != "npm ci" {
		t.Fatalf("setup command = %q", got)
	}
}

func TestCreateSandboxResumesExistingBranch(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	e.repoAgent.branches = []agent.RepoRef{{Name: "main"}, {Name: "feature/login"}}

	ws, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{Branch: "feature/login", Kind: "review"})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if ws.Kind != workspace.KindReview {
		t.Fatalf("expected review sandbox, got %s", ws.Kind)
	}
	if got := e.workspaces.seeds[ws.ID]; got != "feature/login <- bundle-of-feature/login" {
		t.Fatalf("seed = %q", got)
	}
}

func TestCreateSandboxRejections(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj", MaxSandboxes: 2})
	ctx := context.Background()
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "taken"}); err != nil {
		t.Fatalf("first sandbox: %v", err)
	}

	tests := []struct {
		name string
		in   SandboxInput
		want error
	}{
		{"default branch", SandboxInput{Branch: "main"}, ErrDefaultBranch},
		{"branch in use", SandboxInput{Branch: "taken"}, ErrBranchInUse},
		{"interactive kind", SandboxInput{Kind: "interactive"}, ErrInvalidInput},
		{"unknown kind", SandboxInput{Kind: "root"}, ErrInvalidInput},
		{"flag branch", SandboxInput{Branch: "-D"}, ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}

	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{}); err != nil {
		t.Fatalf("second sandbox: %v", err)
	}
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{}); !errors.Is(err, ErrSandboxLimit) {
		t.Fatalf("expected ErrSandboxLimit, got %v", err)
	}
	if _, err := e.svc.CreateSandbox(ctx, e.otherID, p.ID, SandboxInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for other user, got %v", err)
	}
}

func TestCreateSandboxLimitHoldsUnderConcurrency(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj", MaxSandboxes: 3})

	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{})
			switch {
			case err == nil:
				mu.Lock()
				created++
				mu.Unlock()
			case !errors.Is(err, ErrSandboxLimit):
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if created != 3 {
		t.Fatalf("created %d sandboxes, limit is 3", created)
	}
}

func TestSandboxBeingCreatedIsCountedOnce(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj", MaxSandboxes: 2})
	ctx := context.Background()

	// The first sandbox's row exists but its creation has not finished, so
	// its reservation is still held.
	if err := e.svc.reserveSandbox(ctx, p, "first", false); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	e.workspaces.add(&workspace.Workspace{UserID: e.userID, ProjectID: p.ID, Kind: workspace.KindTask, Branch: "first", Status: workspace.StatusRunning})

	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "second"}); err != nil {
		t.Fatalf("second sandbox fits the limit of 2: %v", err)
	}
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "third"}); !errors.Is(err, ErrSandboxLimit) {
		t.Fatalf("expected ErrSandboxLimit for a third, got %v", err)
	}
	if err := e.svc.reserveSandbox(ctx, p, "first", false); !errors.Is(err, ErrBranchInUse) {
		t.Fatalf("expected ErrBranchInUse for a branch being created, got %v", err)
	}
	e.svc.releaseSandbox(p.ID, "first")
}

func TestStoppedSandboxesDoNotCountTowardLimit(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj", MaxSandboxes: 1})
	ctx := context.Background()

	first, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "one"})
	if err != nil {
		t.Fatalf("first sandbox: %v", err)
	}
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "two"}); !errors.Is(err, ErrSandboxLimit) {
		t.Fatalf("expected ErrSandboxLimit while first runs, got %v", err)
	}

	e.workspaces.setStatus(first.ID, workspace.StatusStopped)
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "one"}); !errors.Is(err, ErrBranchInUse) {
		t.Fatalf("a stopped sandbox must keep its branch, got %v", err)
	}
	second, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "two"})
	if err != nil {
		t.Fatalf("second sandbox after first stopped: %v", err)
	}

	if _, err := e.svc.StartSandbox(ctx, e.userID, p.ID, first.ID); !errors.Is(err, ErrSandboxLimit) {
		t.Fatalf("restarting over the limit: expected ErrSandboxLimit, got %v", err)
	}
	e.workspaces.setStatus(second.ID, workspace.StatusStopped)
	ws, err := e.svc.StartSandbox(ctx, e.userID, p.ID, first.ID)
	if err != nil || ws.Status != workspace.StatusRunning {
		t.Fatalf("restart within limit: ws=%+v err=%v", ws, err)
	}
}

func TestStartSandboxRejectsOtherWorkspaces(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	other := e.createProject(t, CreateInput{Name: "other"})
	ctx := context.Background()
	plain := e.workspaces.add(&workspace.Workspace{UserID: e.userID, Kind: workspace.KindInteractive, Status: workspace.StatusStopped})
	foreign := e.workspaces.add(&workspace.Workspace{UserID: e.userID, ProjectID: other.ID, Kind: workspace.KindTask, Branch: "x", Status: workspace.StatusStopped})

	for _, id := range []int64{plain.ID, foreign.ID, 9999} {
		if _, err := e.svc.StartSandbox(ctx, e.userID, p.ID, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("workspace %d: expected ErrNotFound, got %v", id, err)
		}
	}
}

func TestCompareDefaultsBaseToDefaultBranch(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	e.repoAgent.ahead = map[string]int{"task/1": 2}
	ctx := context.Background()

	cmp, err := e.svc.Compare(ctx, e.userID, p.ID, "", "task/1")
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if cmp.Base != "main" || cmp.Head != "task/1" || cmp.Ahead != 2 {
		t.Fatalf("unexpected comparison: %+v", cmp)
	}
	if _, err := e.svc.Compare(ctx, e.userID, p.ID, "", "-x"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCreateSandboxRemovesSandboxWhenSeedFails(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	e.workspaces.seedErr = errors.New("clone failed")

	if _, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{}); err == nil {
		t.Fatal("expected seed failure")
	}
	if len(e.workspaces.deleted) != 1 || len(e.workspaces.byID) != 0 {
		t.Fatalf("expected unseeded sandbox removed, deleted=%v remaining=%d", e.workspaces.deleted, len(e.workspaces.byID))
	}
}

func TestPublishSandboxImportsOnlyItsOwnBranch(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	ws, err := e.svc.CreateSandbox(context.Background(), e.userID, p.ID, SandboxInput{Branch: "feature"})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	e.workspaces.sandboxBundle = "sandbox-bundle"

	result, err := e.svc.PublishSandbox(context.Background(), e.userID, p.ID, ws.ID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if result.Branch != "feature" || string(e.repoAgent.imported["feature"]) != "sandbox-bundle" || len(e.repoAgent.imported) != 1 {
		t.Fatalf("unexpected import: %+v %v", result, e.repoAgent.imported)
	}
}

func TestPublishSandboxRejections(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	other := e.createProject(t, CreateInput{Name: "other"})
	foreign, err := e.svc.CreateSandbox(ctx, e.userID, other.ID, SandboxInput{})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	interactive := e.workspaces.add(&workspace.Workspace{UserID: e.userID, Kind: workspace.KindInteractive})

	tests := []struct {
		name        string
		workspaceID int64
	}{
		{"sandbox of another project", foreign.ID},
		{"interactive workspace", interactive.ID},
		{"missing workspace", 9999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.svc.PublishSandbox(ctx, e.userID, p.ID, tt.workspaceID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("expected ErrNotFound, got %v", err)
			}
		})
	}
	// The fake repo agent holds the token of the last project created.
	review, err := e.svc.CreateSandbox(ctx, e.userID, other.ID, SandboxInput{Kind: "review", Branch: "review/1"})
	if err != nil {
		t.Fatalf("create review sandbox: %v", err)
	}
	if _, err := e.svc.PublishSandbox(ctx, e.userID, other.ID, review.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a review sandbox must not publish, got %v", err)
	}
	if len(e.repoAgent.imported) != 0 {
		t.Fatalf("rejected publishes imported: %v", e.repoAgent.imported)
	}
}

func TestCreateSandboxFromAnotherBranch(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	e.repoAgent.branches = []agent.RepoRef{{Name: "main"}, {Name: "task/1"}, {Name: "review/2"}}
	ctx := context.Background()

	ws, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Kind: "review", Branch: "review/1", Base: "task/1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := e.workspaces.seeds[ws.ID]; got != "task/1 <- bundle-of-task/1" {
		t.Fatalf("seed = %q", got)
	}
	resumed, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Kind: "review", Branch: "review/2", Base: "task/1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := e.workspaces.seeds[resumed.ID]; got != "review/2 <- bundle-of-review/2" {
		t.Fatalf("an existing branch should resume, seed = %q", got)
	}
	if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "review/3", Base: "nope"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for a missing base, got %v", err)
	}
}

func TestSyncSandboxBaseFetchesDefaultBranch(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	ctx := context.Background()
	ws, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{Branch: "task/1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := e.svc.SyncSandboxBase(ctx, e.userID, p.ID, ws.ID)
	if err != nil || got.Branch != "devpad/main" || e.workspaces.seeds[ws.ID] != "devpad/main <- bundle-of-main" {
		t.Fatalf("sync: %+v %v seed=%q", got, err, e.workspaces.seeds[ws.ID])
	}
	plain := e.workspaces.add(&workspace.Workspace{UserID: e.userID, Kind: workspace.KindInteractive})
	if _, err := e.svc.SyncSandboxBase(ctx, e.userID, p.ID, plain.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a non-sandbox, got %v", err)
	}
}

func TestMergeCommitsAsUserIntoDefaultBranch(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	ctx := context.Background()

	result, err := e.svc.Merge(ctx, e.userID, p.ID, MergeInput{Source: "task/1", Strategy: "squash", ExpectedSourceSHA: "abc"})
	if err != nil || result.SHA != "merged" {
		t.Fatalf("merge: %+v %v", result, err)
	}
	want := agent.RepoMergeRequest{Source: "task/1", Target: "main", Strategy: "squash", ExpectedSourceSHA: "abc", Name: "owner", Email: "owner@example.com"}
	if got := e.repoAgent.merges; len(got) != 1 || got[0] != want {
		t.Fatalf("merge request = %+v", got)
	}
	for _, source := range []string{"main", "", "-x"} {
		if _, err := e.svc.Merge(ctx, e.userID, p.ID, MergeInput{Source: source}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("source %q: expected ErrInvalidInput, got %v", source, err)
		}
	}
	if _, err := e.svc.Merge(ctx, e.otherID, p.ID, MergeInput{Source: "task/1"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for another user, got %v", err)
	}

	e.repoAgent.mergeErr = map[string]any{"error": "task/1 conflicts with main", "conflicts": []string{"a.go"}}
	_, err = e.svc.Merge(ctx, e.userID, p.ID, MergeInput{Source: "task/1"})
	var agentErr *agent.AgentError
	if !errors.As(err, &agentErr) || agentErr.StatusCode != http.StatusConflict || len(agentErr.Conflicts) != 1 {
		t.Fatalf("expected a conflict error listing a.go, got %v", err)
	}
}

func TestPushRequiresUpstream(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	local := e.createProject(t, CreateInput{Name: "local"})
	if _, err := e.svc.Push(ctx, e.userID, local.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput without an upstream, got %v", err)
	}
	cloned := e.createProject(t, CreateInput{Name: "cloned", UpstreamURL: "https://example.com/repo.git"})
	if _, err := e.svc.Push(ctx, e.userID, cloned.ID); err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(e.repoAgent.pushed) != 1 || e.repoAgent.pushed[0] != "main" {
		t.Fatalf("pushed = %v", e.repoAgent.pushed)
	}
}

func TestFileResponseIsInert(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteFileContent(rec, []byte("<html>"))
	if rec.Header().Get("Content-Type") != "application/octet-stream" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Disposition") != "attachment" {
		t.Fatalf("unexpected headers: %v", rec.Header())
	}
}

func TestDeleteRemovesSandboxesThenRepo(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	for i := 0; i < 2; i++ {
		if _, err := e.svc.CreateSandbox(ctx, e.userID, p.ID, SandboxInput{}); err != nil {
			t.Fatalf("create sandbox: %v", err)
		}
	}

	if err := e.svc.Delete(ctx, e.userID, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(e.workspaces.deleted) != 2 {
		t.Fatalf("expected 2 sandboxes deleted, got %v", e.workspaces.deleted)
	}
	if len(e.containers.volumes) != 0 || len(e.containers.networks) != 0 || len(e.containers.removed) != 1 {
		t.Fatalf("repo resources left: volumes=%v networks=%v removed=%v", e.containers.volumes, e.containers.networks, e.containers.removed)
	}
	if _, err := e.svc.Get(ctx, e.userID, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected project gone, got %v", err)
	}
}

func TestMigrationEnforcesSandboxBranchOwnership(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	p := e.createProject(t, CreateInput{Name: "proj"})
	repo := workspace.NewRepository(e.db.Conn())

	insert := func(projectID int64, branch string) error {
		return repo.Create(ctx, &workspace.Workspace{UserID: e.userID, Name: "ws", ProjectID: projectID, Kind: workspace.KindTask, Branch: branch, Status: workspace.StatusRunning})
	}
	if err := insert(p.ID, "feature"); err != nil {
		t.Fatalf("first sandbox: %v", err)
	}
	if err := insert(p.ID, "feature"); err == nil {
		t.Fatal("expected duplicate branch in one project to fail")
	}
	// Plain workspaces have no project and no branch; any number may exist.
	for i := 0; i < 2; i++ {
		if err := repo.Create(ctx, &workspace.Workspace{UserID: e.userID, Name: "plain", Status: workspace.StatusRunning}); err != nil {
			t.Fatalf("plain workspace: %v", err)
		}
	}
	// A project with sandboxes cannot be deleted out from under them.
	if err := NewRepository(e.db.Conn()).Delete(ctx, p.ID); err == nil {
		t.Fatal("expected project delete to fail while sandboxes reference it")
	}

	got, err := repo.ListByProjectID(ctx, p.ID)
	if err != nil || len(got) != 1 || got[0].Branch != "feature" || got[0].Kind != workspace.KindTask {
		t.Fatalf("unexpected project workspaces: %+v, %v", got, err)
	}
}

func TestHandlerNeverExposesRepoToken(t *testing.T) {
	e := setupTest(t)
	p := e.createProject(t, CreateInput{Name: "proj"})
	h := NewHandler(e.svc)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+strconv.FormatInt(p.ID, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(p.ID, 10))
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: e.userID}))
	rec := httptest.NewRecorder()
	h.HandleGet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(p.RepoAgentToken)) {
		t.Fatal("response leaked the repo agent token")
	}
}

func TestHandlerMapsErrors(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{ErrNotFound, http.StatusNotFound},
		{workspace.ErrForbidden, http.StatusForbidden},
		{invalid("bad"), http.StatusBadRequest},
		{ErrDefaultBranch, http.StatusBadRequest},
		{ErrSandboxLimit, http.StatusConflict},
		{ErrBranchInUse, http.StatusConflict},
		{ErrBranchPublishing, http.StatusConflict},
		{ErrDeleting, http.StatusConflict},
		{fmt.Errorf("%w: wait", ErrBusy), http.StatusConflict},
		{ErrNotReady, http.StatusConflict},
		{&agent.AgentError{StatusCode: 400, Message: "invalid bundle"}, http.StatusBadRequest},
		{&agent.AgentError{StatusCode: 409, Message: "conflicts", Conflicts: []string{"a.go"}}, http.StatusConflict},
		{&agent.AgentError{StatusCode: 500, Message: "git crashed"}, http.StatusBadGateway},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleServiceError(rec, tt.err)
			if rec.Code != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, rec.Code)
			}
		})
	}
}

func (f *fakeWorkspaces) standalone(userID, id int64) (*workspace.Workspace, error) {
	ws, ok := f.byID[id]
	switch {
	case !ok:
		return nil, workspace.ErrNotFound
	case ws.UserID != userID:
		return nil, workspace.ErrForbidden
	case !ws.ReceivesUserCredentials():
		return nil, workspace.ErrNotStandalone
	case ws.Status != workspace.StatusRunning:
		return nil, workspace.ErrNotRunning
	}
	return ws, nil
}

func (f *fakeWorkspaces) DiscoverRepos(_ context.Context, userID, id int64) (*agent.RepoDiscovery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.standalone(userID, id); err != nil {
		return nil, err
	}
	if f.discovery == nil {
		return &agent.RepoDiscovery{Repos: []agent.DiscoveredRepo{{Path: ".", Branch: "main"}}}, nil
	}
	return f.discovery, nil
}

func (f *fakeWorkspaces) BeginConversion(_ context.Context, userID, id int64) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.standalone(userID, id); err != nil {
		return nil, err
	}
	if f.converting[id] {
		return nil, workspace.ErrConverting
	}
	f.converting[id] = true
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.converting, id)
	}, nil
}

func (f *fakeWorkspaces) InitRepoFromContents(_ context.Context, userID, id int64, branch string, includeSensitive bool) (*agent.RepoContentsInit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.convertErr["init"]; err != nil {
		return nil, err
	}
	f.initCalls = append(f.initCalls, fmt.Sprintf("%s sensitive=%v", branch, includeSensitive))
	return &agent.RepoContentsInit{Branch: branch, SHA: "init-sha", Files: 3}, nil
}

func (f *fakeWorkspaces) UndoInitRepoFromContents(_ context.Context, _, _ int64, sha string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.undone = append(f.undone, sha)
	return nil
}

func (f *fakeWorkspaces) BundleAllBranches(_ context.Context, userID, id int64, dir string) (io.ReadCloser, error) {
	f.mu.Lock()
	hook := f.onBundleAll
	err := f.convertErr["bundle"]
	if err == nil {
		f.bundledAll = append(f.bundledAll, dir)
	}
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader("all-of-" + dir)), nil
}

func (f *fakeWorkspaces) AttachToProject(_ context.Context, userID, id, projectID int64, strip []string) (*workspace.AttachResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stripped = append(f.stripped, strip)
	if err := f.convertErr["attach"]; err != nil {
		return nil, err
	}
	ws, err := f.standalone(userID, id)
	if err != nil {
		return nil, err
	}
	ws.ProjectID, ws.RepoSeeded = projectID, true
	f.steps = append(f.steps, "attach")
	var stripped []agent.CredentialItem
	for _, id := range strip {
		stripped = append(stripped, agent.CredentialItem{ID: id})
	}
	return &workspace.AttachResult{
		SSH:      &agent.SSHCleanup{Removed: []string{"~/.ssh/id_ed25519"}, Kept: []string{}, Absent: []string{}},
		Stripped: stripped,
	}, nil
}

func (f *fakeWorkspaces) RestartConverted(_ context.Context, _, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, "restart")
	if f.restartErr != nil {
		return f.restartErr
	}
	f.restarted = append(f.restarted, id)
	return nil
}
