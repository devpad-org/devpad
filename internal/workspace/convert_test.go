package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/agentbin"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/encrypt"
)

// convertAgent is a fake current agent for conversion tests. It records
// requests and the SSH files it was asked to remove.
type convertAgent struct {
	mu        sync.Mutex
	version   string
	requests  []string
	removeReq struct {
		Files []agent.ManagedSSHFile `json:"files"`
	}
}

func (a *convertAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch r.URL.Path {
	case "/healthz":
		return
	case "/api/version":
		version := a.version
		if version == "" {
			version = agentbin.Version
		}
		json.NewEncoder(w).Encode(map[string]string{"version": version})
		return
	}
	a.requests = append(a.requests, r.URL.Path)
	switch r.URL.Path {
	case "/api/command":
		json.NewEncoder(w).Encode(map[string]any{"output": "", "exit_code": 0})
	case "/api/ssh/remove-managed":
		json.NewDecoder(r.Body).Decode(&a.removeReq)
		json.NewEncoder(w).Encode(agent.SSHCleanup{Removed: []string{"~/.ssh/id_ed25519"}, Kept: []string{"~/.ssh/config"}})
	case "/api/repo/discover":
		json.NewEncoder(w).Encode(agent.RepoDiscovery{Repos: []agent.DiscoveredRepo{{Path: ".", Branch: "main"}}})
	case "/api/repo/bundle-all":
		w.Write([]byte("all-of:" + r.URL.Query().Get("dir")))
	default:
		http.NotFound(w, r)
	}
}

func (a *convertAgent) paths() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.requests...)
}

func (a *convertAgent) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = nil
}

func setupConvertService(t *testing.T) (*service, *convertAgent) {
	t.Helper()
	db := setupTestDB(t)
	ca := &convertAgent{}
	srv := httptest.NewServer(ca)
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
	if _, err := db.Exec(`UPDATE users SET ssh_private_key = ?, ssh_public_key = ? WHERE id = 1`, encrypted, "PUBLIC KEY\n"); err != nil {
		t.Fatalf("setting SSH key: %v", err)
	}
	svc := NewService(NewRepository(db), &mockContainerManager{ipOverride: host}, auth.NewUserRepository(db), cipher).(*service)
	svc.agentPort = port
	return svc, ca
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestAttachToProjectRemovesDevpadKeys(t *testing.T) {
	svc, ca := setupConvertService(t)
	ctx := context.Background()
	ws, err := svc.Create(ctx, 1, "mine", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ca.reset()

	cleanup, err := svc.AttachToProject(ctx, 1, ws.ID, 7)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if !slices.Equal(cleanup.Removed, []string{"~/.ssh/id_ed25519"}) || !slices.Equal(cleanup.Kept, []string{"~/.ssh/config"}) {
		t.Fatalf("unexpected cleanup: %+v", cleanup)
	}
	// Exactly what WriteSSHKeys writes is offered for removal.
	want := []agent.ManagedSSHFile{
		{Name: "id_ed25519", SHA256: sha("PRIVATE KEY\n")},
		{Name: "id_ed25519.pub", SHA256: sha("PUBLIC KEY\n")},
		{Name: "known_hosts", SHA256: sha(knownHosts)},
		{Name: "config", SHA256: sha(sshConfig)},
	}
	if !slices.Equal(ca.removeReq.Files, want) {
		t.Fatalf("remove request = %+v, want %+v", ca.removeReq.Files, want)
	}

	got, err := svc.Get(ctx, 1, ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.IsProjectWorkspace() || got.ProjectID != 7 || !got.RepoSeeded || got.ReceivesUserCredentials() {
		t.Fatalf("workspace not attached: %+v", got)
	}

	// From now on starting it never puts the keys back.
	ca.reset()
	if _, err := svc.Stop(ctx, 1, ws.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := svc.Start(ctx, 1, ws.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if slices.Contains(ca.paths(), "/api/command") {
		t.Fatalf("keys injected into a converted workspace: %v", ca.paths())
	}

	if _, err := svc.AttachToProject(ctx, 1, ws.ID, 8); !errors.Is(err, ErrNotStandalone) {
		t.Fatalf("expected ErrNotStandalone attaching twice, got %v", err)
	}
}

// failingAttachRepo fails attaching, as if the database write did.
type failingAttachRepo struct {
	Repository
}

func (failingAttachRepo) AttachToProject(context.Context, int64, int64) (bool, error) {
	return false, errors.New("disk full")
}

func TestAttachToProjectPutsKeysBackOnFailure(t *testing.T) {
	svc, ca := setupConvertService(t)
	ctx := context.Background()
	ws, err := svc.Create(ctx, 1, "mine", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	svc.repo = failingAttachRepo{svc.repo}
	ca.reset()

	if _, err := svc.AttachToProject(ctx, 1, ws.ID, 7); err == nil {
		t.Fatal("expected attach to fail")
	}
	paths := ca.paths()
	if len(paths) < 2 || paths[0] != "/api/ssh/remove-managed" || !slices.Contains(paths[1:], "/api/command") {
		t.Fatalf("expected keys removed then written back, got %v", paths)
	}
}

func TestConversionRefusals(t *testing.T) {
	svc, ca := setupConvertService(t)
	ctx := context.Background()
	project, err := svc.CreateProjectWorkspace(ctx, 1, ProjectWorkspaceSpec{ProjectID: 7, Name: "proj"})
	if err != nil {
		t.Fatalf("create project workspace: %v", err)
	}
	sandbox, err := svc.CreateSandbox(ctx, 1, sandboxSpec("feature"))
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	for _, id := range []int64{project.ID, sandbox.ID} {
		if _, err := svc.DiscoverRepos(ctx, 1, id); !errors.Is(err, ErrNotStandalone) {
			t.Fatalf("discover on workspace %d: expected ErrNotStandalone, got %v", id, err)
		}
		if _, err := svc.BeginConversion(ctx, 1, id); !errors.Is(err, ErrNotStandalone) {
			t.Fatalf("convert workspace %d: expected ErrNotStandalone, got %v", id, err)
		}
	}

	ws, err := svc.Create(ctx, 1, "mine", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Stop(ctx, 1, ws.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := svc.BeginConversion(ctx, 1, ws.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("expected ErrNotRunning for a stopped workspace, got %v", err)
	}
	if _, err := svc.Start(ctx, 1, ws.ID); err != nil {
		t.Fatalf("start: %v", err)
	}

	ca.mu.Lock()
	ca.version = "old"
	ca.mu.Unlock()
	if _, err := svc.DiscoverRepos(ctx, 1, ws.ID); !errors.Is(err, ErrAgentOutdated) {
		t.Fatalf("expected ErrAgentOutdated, got %v", err)
	}
	if _, err := svc.AttachToProject(ctx, 1, ws.ID, 7); !errors.Is(err, ErrAgentOutdated) {
		t.Fatalf("expected ErrAgentOutdated attaching, got %v", err)
	}
	if _, err := svc.BeginConversion(ctx, 2, ws.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for another user, got %v", err)
	}
}

func TestConversionLocksTheWorkspace(t *testing.T) {
	svc, _ := setupConvertService(t)
	ctx := context.Background()
	ws, err := svc.Create(ctx, 1, "mine", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	release, err := svc.BeginConversion(ctx, 1, ws.ID)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := svc.BeginConversion(ctx, 1, ws.ID); !errors.Is(err, ErrConverting) {
		t.Fatalf("expected ErrConverting for a second conversion, got %v", err)
	}
	if err := svc.Delete(ctx, 1, ws.ID); !errors.Is(err, ErrConverting) {
		t.Fatalf("expected ErrConverting deleting mid-conversion, got %v", err)
	}
	release()
	if err := svc.Delete(ctx, 1, ws.ID); err != nil {
		t.Fatalf("delete after release: %v", err)
	}
}

func TestDiscoverAndBundleAllBranches(t *testing.T) {
	svc, ca := setupConvertService(t)
	ctx := context.Background()
	ws, err := svc.Create(ctx, 1, "mine", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := svc.DiscoverRepos(ctx, 1, ws.ID)
	if err != nil || len(found.Repos) != 1 || found.Repos[0].Path != "." {
		t.Fatalf("discover: %+v %v", found, err)
	}
	bundle, err := svc.BundleAllBranches(ctx, 1, ws.ID, "src/app")
	if err != nil {
		t.Fatalf("bundle all: %v", err)
	}
	defer bundle.Close()
	if body, _ := io.ReadAll(bundle); string(body) != "all-of:src/app" {
		t.Fatalf("bundle body = %q", body)
	}
	if !slices.Contains(ca.paths(), "/api/repo/bundle-all") {
		t.Fatalf("no bundle request: %v", ca.paths())
	}
}
