package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/encrypt"
)

// recordingAgent is a fake workspace agent that records every authenticated
// request it receives.
type recordingAgent struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string]string
}

func (a *recordingAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		return
	case "/api/version":
		json.NewEncoder(w).Encode(map[string]string{"version": "test"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.requests = append(a.requests, r.URL.Path+"?"+r.URL.RawQuery)
	a.bodies[r.URL.Path] = string(body)
	a.mu.Unlock()

	switch r.URL.Path {
	case "/api/command":
		json.NewEncoder(w).Encode(map[string]any{"output": "", "exit_code": 0})
	case "/api/repo/seed":
		json.NewEncoder(w).Encode(map[string]string{"branch": r.URL.Query().Get("branch"), "sha": "abc"})
	case "/api/repo/bundle":
		w.Write([]byte("bundle:" + r.URL.Query().Get("branch")))
	case "/api/repo/fetch-base":
		json.NewEncoder(w).Encode(map[string]string{"branch": "devpad/" + r.URL.Query().Get("branch"), "sha": "def"})
	case "/api/repo/checkout":
		branch := r.URL.Query().Get("branch")
		json.NewEncoder(w).Encode(map[string]any{"branch": branch, "sha": "local", "upstream": "devpad/" + branch, "upstreamSha": "up", "created": true})
	default:
		http.NotFound(w, r)
	}
}

func (a *recordingAgent) paths() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.requests...)
}

// setupSandboxService returns a workspace service whose user 1 has an SSH key
// configured, backed by a recording agent.
func setupSandboxService(t *testing.T) (Service, *recordingAgent) {
	t.Helper()
	db := setupTestDB(t)

	ra := &recordingAgent{bodies: map[string]string{}}
	srv := httptest.NewServer(ra)
	t.Cleanup(srv.Close)
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	key, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatalf("generating encryption key: %v", err)
	}
	cipher, err := encrypt.NewCipher(key)
	if err != nil {
		t.Fatalf("creating cipher: %v", err)
	}
	encrypted, err := cipher.Encrypt("PRIVATE KEY\n")
	if err != nil {
		t.Fatalf("encrypting key: %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET ssh_private_key = ?, ssh_public_key = 'PUBLIC KEY' WHERE id = 1`, encrypted); err != nil {
		t.Fatalf("setting SSH key: %v", err)
	}

	svc := NewService(NewRepository(db), &mockContainerManager{ipOverride: host}, auth.NewUserRepository(db), cipher)
	svc.(*service).agentPort = port
	return svc, ra
}

func sandboxSpec(branch string) SandboxSpec {
	return SandboxSpec{ProjectID: 7, Kind: KindTask, Branch: branch, Name: "sandbox"}
}

func TestCreateSandboxNeverInjectsSSHKeys(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()

	ws, err := svc.CreateSandbox(ctx, 1, sandboxSpec("feature"))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if ws.ProjectID != 7 || ws.Kind != KindTask || ws.Branch != "feature" || !ws.IsSandbox() {
		t.Fatalf("unexpected sandbox: %+v", ws)
	}
	if got := ra.paths(); len(got) != 0 {
		t.Fatalf("sandbox create sent agent requests %v; SSH keys must not be injected", got)
	}

	// Restarting a sandbox must not inject keys either.
	if _, err := svc.Stop(ctx, 1, ws.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := svc.Start(ctx, 1, ws.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := ra.paths(); len(got) != 0 {
		t.Fatalf("sandbox start sent agent requests %v; SSH keys must not be injected", got)
	}

	// The same user's interactive workspace still gets them.
	if _, err := createLegacyWorkspace(ctx, svc, 1, "mine", ""); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if got := ra.paths(); len(got) == 0 || !strings.HasPrefix(got[0], "/api/command") {
		t.Fatalf("expected SSH injection commands for interactive workspace, got %v", got)
	}
}

// Legacy standalone workspaces keep receiving the user's SSH key each time
// they start, which is the only way they get it now that devpad no longer
// creates them.
func TestLegacyWorkspaceStartInjectsSSHKeys(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()

	ws, err := createLegacyWorkspace(ctx, svc, 1, "legacy", "")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := svc.Stop(ctx, 1, ws.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	before := len(ra.paths())

	if _, err := svc.Start(ctx, 1, ws.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	after := ra.paths()[before:]
	if len(after) == 0 {
		t.Fatal("start sent no agent requests; expected SSH key injection")
	}
	for _, p := range after {
		if !strings.HasPrefix(p, "/api/command") {
			t.Fatalf("unexpected agent request %q on start", p)
		}
	}
	ra.mu.Lock()
	body := ra.bodies["/api/command"]
	ra.mu.Unlock()
	if !strings.Contains(body, ".ssh") {
		t.Fatalf("expected the injection commands to write ~/.ssh, last command body: %s", body)
	}
}

func TestCreateSandboxValidation(t *testing.T) {
	svc, _ := setupSandboxService(t)
	tests := []struct {
		name string
		spec SandboxSpec
	}{
		{"no project", SandboxSpec{Kind: KindTask, Branch: "b"}},
		{"interactive kind", SandboxSpec{ProjectID: 7, Kind: KindInteractive, Branch: "b"}},
		{"unknown kind", SandboxSpec{ProjectID: 7, Kind: "admin", Branch: "b"}},
		{"no branch", SandboxSpec{ProjectID: 7, Kind: KindReview}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.CreateSandbox(context.Background(), 1, tt.spec); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSandboxRepoOperationsUseRecordedBranch(t *testing.T) {
	svc, ra := setupSandboxService(t)
	ctx := context.Background()
	ws, err := svc.CreateSandbox(ctx, 1, sandboxSpec("feature/x"))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if _, err := svc.SeedSandboxRepo(ctx, 1, ws.ID, "main", strings.NewReader("main-bundle")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	branch, bundle, err := svc.BundleSandboxBranch(ctx, 1, ws.ID)
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	defer bundle.Close()
	body, _ := io.ReadAll(bundle)

	if branch != "feature/x" || string(body) != "bundle:feature/x" {
		t.Fatalf("bundle branch=%q body=%q", branch, body)
	}
	paths := ra.paths()
	if len(paths) != 2 || paths[0] != "/api/repo/seed?base=main&branch=feature%2Fx&email=test%40test.com&name=testuser" {
		t.Fatalf("unexpected agent requests: %v", paths)
	}
	if ra.bodies["/api/repo/seed"] != "main-bundle" {
		t.Fatalf("seed body = %q", ra.bodies["/api/repo/seed"])
	}

	got, err := svc.FetchSandboxBase(ctx, 1, ws.ID, "main", strings.NewReader("latest-main"))
	if err != nil || got.Branch != "devpad/main" || ra.bodies["/api/repo/fetch-base"] != "latest-main" {
		t.Fatalf("fetch base: %+v %v body=%q", got, err, ra.bodies["/api/repo/fetch-base"])
	}
}

func TestSandboxRepoOperationsRejectOtherWorkspaces(t *testing.T) {
	svc, _ := setupSandboxService(t)
	ctx := context.Background()
	interactive, err := createLegacyWorkspace(ctx, svc, 1, "mine", "")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sandbox, err := svc.CreateSandbox(ctx, 1, sandboxSpec("feature"))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if _, _, err := svc.BundleSandboxBranch(ctx, 1, interactive.ID); !errors.Is(err, ErrNotSandbox) {
		t.Fatalf("expected ErrNotSandbox, got %v", err)
	}
	if _, err := svc.SeedSandboxRepo(ctx, 1, interactive.ID, "main", strings.NewReader("")); !errors.Is(err, ErrNotSandbox) {
		t.Fatalf("expected ErrNotSandbox, got %v", err)
	}
	if _, err := svc.FetchSandboxBase(ctx, 1, interactive.ID, "main", strings.NewReader("")); !errors.Is(err, ErrNotSandbox) {
		t.Fatalf("expected ErrNotSandbox, got %v", err)
	}
	if _, _, err := svc.BundleSandboxBranch(ctx, 2, sandbox.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for another user, got %v", err)
	}
}

func TestListByProjectFiltersByOwner(t *testing.T) {
	svc, _ := setupSandboxService(t)
	ctx := context.Background()
	if _, err := svc.CreateSandbox(ctx, 1, sandboxSpec("a")); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if _, err := svc.CreateSandbox(ctx, 2, sandboxSpec("b")); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if _, err := createLegacyWorkspace(ctx, svc, 1, "plain", ""); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	got, err := svc.ListByProject(ctx, 1, 7)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Branch != "a" {
		t.Fatalf("expected only user 1's sandbox, got %+v", got)
	}
}
