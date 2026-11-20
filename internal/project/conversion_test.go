package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/encrypt"
	"github.com/devpad-org/devpad/internal/workspace"
)

// failReadyRepo fails marking a project ready a number of times, as a
// database write might.
type failReadyRepo struct {
	*tokenCapturingRepo
	mu    sync.Mutex
	fails int
}

func (r *failReadyRepo) failing(p *Project) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	fail := p.Status == StatusReady && r.fails > 0
	if fail {
		r.fails--
	}
	return fail
}

func (r *failReadyRepo) Update(ctx context.Context, p *Project) error {
	if r.failing(p) {
		return errors.New("disk full")
	}
	return r.tokenCapturingRepo.Update(ctx, p)
}

func (r *failReadyRepo) UpdateWithConversion(ctx context.Context, p *Project, c *Conversion) error {
	if r.failing(p) {
		return errors.New("disk full")
	}
	return r.tokenCapturingRepo.UpdateWithConversion(ctx, p, c)
}

func (e *testEnv) conversion(t *testing.T, projectID int64) *Conversion {
	t.Helper()
	conv, err := e.svc.repo.GetConversion(context.Background(), projectID)
	if err != nil || conv == nil {
		t.Fatalf("conversion of project %d: %+v %v", projectID, conv, err)
	}
	return conv
}

func (e *testEnv) project(t *testing.T, projectID int64) *Project {
	t.Helper()
	p, err := e.svc.repo.GetByID(context.Background(), projectID)
	if err != nil || p == nil {
		t.Fatalf("project %d: %+v %v", projectID, p, err)
	}
	return p
}

func TestConvertWorkspaceFinishesAfterAttachEvenWhenMarkingReadyFails(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.giveUserKey(t)
	e.svc.repo = &failReadyRepo{tokenCapturingRepo: &tokenCapturingRepo{Repository: NewRepository(e.db.Conn()), agent: e.repoAgent}, fails: 1}

	result, err := e.svc.ConvertWorkspace(context.Background(), e.userID, ws.ID, ConvertInput{RepoPath: ".", Name: "App"})
	if err != nil {
		t.Fatalf("the conversion stands once attached: %v", err)
	}
	// The restart and the rotation ran although the project is not ready.
	if !result.InPlace || !result.KeyRotated || !slices.Equal(e.workspaces.restarted, []int64{ws.ID}) {
		t.Fatalf("steps after attaching were skipped: %+v restarted=%v", result, e.workspaces.restarted)
	}
	p := e.project(t, result.Project.ID)
	if p.Status != StatusCreating {
		t.Fatalf("expected the project still creating, got %s", p.Status)
	}
	conv := e.conversion(t, p.ID)
	if conv.State != ConversionAttached || conv.RestartPending || conv.RotationPending || conv.PublicKey == "" {
		t.Fatalf("progress not recorded: %+v", conv)
	}

	// The next read of the project finishes it, without repeating a step.
	if _, err := e.svc.Get(context.Background(), e.userID, p.ID); err != nil {
		t.Fatalf("get: %v", err)
	}
	e.svc.background.Wait()
	if got := e.project(t, p.ID); got.Status != StatusReady {
		t.Fatalf("project not finished: %s", got.Status)
	}
	if conv := e.conversion(t, p.ID); conv.State != ConversionDone {
		t.Fatalf("conversion not finished: %+v", conv)
	}
	if len(e.workspaces.restarted) != 1 || len(e.keys.rotated) != 1 {
		t.Fatalf("a step ran twice: restarted=%v rotated=%v", e.workspaces.restarted, e.keys.rotated)
	}
}

// interruptedConversion records a conversion as a crash would leave it,
// with the workspace attached or not.
func (e *testEnv) interruptedConversion(t *testing.T, state ConversionState, attached bool, initSHA string) (*Project, *workspace.Workspace) {
	t.Helper()
	ws := e.standalone()
	p := &Project{
		UserID: e.userID, Name: "App", DefaultBranch: "main", MaxSandboxes: 1, Status: StatusCreating,
		RepoContainerID: "repo-container", RepoVolumeName: "repo-volume", RepoNetworkName: "repo-network", RepoAgentToken: "token",
	}
	conv := &Conversion{
		WorkspaceID: ws.ID, InPlace: true, InitSHA: initSHA, State: state,
		Stripped:       []agent.CredentialItem{netrc},
		RestartPending: true, RotationPending: true,
	}
	if err := e.svc.repo.CreateConverting(context.Background(), p, conv); err != nil {
		t.Fatalf("recording conversion: %v", err)
	}
	if attached {
		ws.ProjectID, ws.RepoSeeded = p.ID, true
	}
	return p, ws
}

func TestReconcileFinishesAConversionCutShortAfterAttaching(t *testing.T) {
	for _, state := range []ConversionState{ConversionAttached, ConversionImporting} {
		// A crash can also land between attaching and recording it.
		t.Run(string(state), func(t *testing.T) {
			e := setupTest(t)
			e.giveUserKey(t)
			p, ws := e.interruptedConversion(t, state, true, "")

			if err := e.svc.Reconcile(context.Background()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			e.svc.background.Wait()

			if got := e.project(t, p.ID); got.Status != StatusReady {
				t.Fatalf("project = %s", got.Status)
			}
			if !slices.Equal(e.workspaces.restarted, []int64{ws.ID}) || !slices.Equal(e.keys.rotated, []int64{e.userID}) {
				t.Fatalf("restarted=%v rotated=%v", e.workspaces.restarted, e.keys.rotated)
			}
			conv := e.conversion(t, p.ID)
			if conv.State != ConversionDone || !conv.KeyRotated || conv.PublicKey != "ssh-ed25519 NEWKEY devpad-owner" {
				t.Fatalf("conversion = %+v", conv)
			}

			// Once finished, nothing runs again.
			if err := e.svc.Reconcile(context.Background()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			e.svc.background.Wait()
			if len(e.workspaces.restarted) != 1 || len(e.keys.rotated) != 1 {
				t.Fatalf("a step ran twice: restarted=%v rotated=%v", e.workspaces.restarted, e.keys.rotated)
			}
		})
	}
}

func TestReconcileUndoesAConversionCutShortBeforeAttaching(t *testing.T) {
	e := setupTest(t)
	e.giveUserKey(t)
	p, ws := e.interruptedConversion(t, ConversionImporting, false, "init-sha")

	if err := e.svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	e.svc.background.Wait()

	if n := e.projectCount(t); n != 0 {
		t.Fatalf("%d projects left", n)
	}
	if !slices.Equal(e.containers.removed, []string{p.RepoContainerID}) {
		t.Fatalf("repo container not removed: %v", e.containers.removed)
	}
	if !slices.Equal(e.workspaces.undone, []string{"init-sha"}) {
		t.Fatalf("repo made from files not undone: %v", e.workspaces.undone)
	}
	if len(e.workspaces.restarted) != 0 || len(e.keys.rotated) != 0 {
		t.Fatalf("an undone conversion restarted or rotated: %v %v", e.workspaces.restarted, e.keys.rotated)
	}
	if got, _ := e.workspaces.Get(context.Background(), e.userID, ws.ID); got.ProjectID != 0 {
		t.Fatalf("workspace changed: %+v", got)
	}
}

func TestConversionNoticeShowsWhatWasDoneUntilDismissed(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.giveUserKey(t)
	e.workspaces.discovery = &agent.RepoDiscovery{
		Repos:       []agent.DiscoveredRepo{{Path: ".", Branch: "main"}},
		Credentials: []agent.CredentialItem{netrc},
	}
	result, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "App", StripCredentials: []string{netrc.ID}})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	h := NewHandler(e.svc)
	call := func(handler http.HandlerFunc, method string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/", nil)
		req.SetPathValue("id", fmt.Sprint(result.Project.ID))
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: e.userID}))
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	// The notice holds what the response did, should it have been lost.
	rec := call(h.HandleConversionNotice, http.MethodGet)
	var body struct {
		Conversion *struct {
			Finished   bool                   `json:"finished"`
			Stripped   []agent.CredentialItem `json:"stripped"`
			KeyRotated bool                   `json:"keyRotated"`
			PublicKey  string                 `json:"publicKey"`
		} `json:"conversion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK || body.Conversion == nil {
		t.Fatalf("notice: %d %s", rec.Code, rec.Body)
	}
	if !body.Conversion.Finished || !body.Conversion.KeyRotated || body.Conversion.PublicKey != result.PublicKey || len(body.Conversion.Stripped) != 1 {
		t.Fatalf("unexpected notice: %s", rec.Body)
	}

	if rec := call(h.HandleDismissConversionNotice, http.MethodPost); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h.HandleConversionNotice, http.MethodGet); !strings.Contains(rec.Body.String(), `"conversion":null`) {
		t.Fatalf("notice after dismissing: %s", rec.Body)
	}

	// Another user sees nothing.
	if _, err := e.svc.ConversionNotice(context.Background(), e.otherID, result.Project.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestConversionNoticeIsEmptyForASubdirectory(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	result, err := e.convert(ws.ID, ConvertInput{RepoPath: "src/app", Name: "App"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if conv, err := e.svc.ConversionNotice(context.Background(), e.userID, result.Project.ID); err != nil || conv != nil {
		t.Fatalf("notice = %+v %v", conv, err)
	}
	if conv := e.conversion(t, result.Project.ID); conv.State != ConversionDone {
		t.Fatalf("conversion = %+v", conv)
	}
}

func TestConvertHandlerReportsCredentialsThatCouldNotBeStripped(t *testing.T) {
	e := setupTest(t)
	e.svc.repo = &tokenCapturingRepo{Repository: NewRepository(e.db.Conn()), agent: e.repoAgent}
	ws := e.standalone()
	e.workspaces.convertErr["attach"] = &workspace.CredentialsError{
		Failed: []workspace.CredentialFailure{{CredentialItem: netrc, Reason: "permission denied"}},
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"repoPath":".","name":"App"}`))
	req.SetPathValue("id", fmt.Sprint(ws.ID))
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: e.userID}))
	rec := httptest.NewRecorder()
	NewHandler(e.svc).HandleConvertWorkspace(rec, req)

	var body struct {
		Credentials []agent.CredentialItem        `json:"credentials"`
		Failed      []workspace.CredentialFailure `json:"failed"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusConflict || len(body.Credentials) != 0 || len(body.Failed) != 1 ||
		body.Failed[0].ID != netrc.ID || body.Failed[0].Reason != "permission denied" {
		t.Fatalf("unexpected response %d: %s", rec.Code, rec.Body)
	}
	if e.projectCount(t) != 0 {
		t.Fatal("the failed conversion left a project")
	}
}

func TestPushWritesTheCurrentKey(t *testing.T) {
	e := setupTest(t)
	key, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	cipher, err := encrypt.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	e.svc.cipher = cipher
	setKey := func(private string) {
		t.Helper()
		encrypted, err := cipher.Encrypt(private)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if _, err := e.db.Conn().Exec(`UPDATE users SET ssh_private_key = ?, ssh_public_key = 'ssh-ed25519 PUB' WHERE id = ?`, encrypted, e.userID); err != nil {
			t.Fatalf("setting key: %v", err)
		}
	}
	p := e.createProject(t, CreateInput{Name: "cloned", UpstreamURL: "git@github.com:o/r.git"})

	// A push after the key was rotated uses the new key.
	for _, private := range []string{"FIRST-PRIVATE-KEY\n", "ROTATED-PRIVATE-KEY\n"} {
		setKey(private)
		e.repoAgent.mu.Lock()
		e.repoAgent.commands = nil
		e.repoAgent.mu.Unlock()
		if _, err := e.svc.Push(context.Background(), e.userID, p.ID, ""); err != nil {
			t.Fatalf("push: %v", err)
		}
		e.repoAgent.mu.Lock()
		commands := strings.Join(e.repoAgent.commands, "\n")
		e.repoAgent.mu.Unlock()
		if !strings.Contains(commands, "~/.ssh/id_ed25519") || !strings.Contains(commands, private) {
			t.Fatalf("push did not write %q: %s", private, commands)
		}
	}
}

// leftConversion records a conversion as a crash or a lost write would leave
// it: the project in status, with the record as given.
func (e *testEnv) leftConversion(t *testing.T, status Status, conv *Conversion) *Project {
	t.Helper()
	p := &Project{
		UserID: e.userID, Name: "App", DefaultBranch: "main", MaxSandboxes: 1, Status: status,
		RepoContainerID: "repo-container", RepoVolumeName: "repo-volume", RepoNetworkName: "repo-network", RepoAgentToken: "token",
	}
	if err := e.svc.repo.CreateConverting(context.Background(), p, conv); err != nil {
		t.Fatalf("recording conversion: %v", err)
	}
	return p
}

func (e *testEnv) reconcile(t *testing.T) {
	t.Helper()
	if err := e.svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	e.svc.background.Wait()
}

// A record left "importing" by a lost write must never remove a project
// that got further: it may hold published branches and merged work.
func TestReconcileNeverRemovesAReadyProject(t *testing.T) {
	t.Run("subdirectory", func(t *testing.T) {
		e := setupTest(t)
		ws := e.standalone()
		p := e.leftConversion(t, StatusReady, &Conversion{WorkspaceID: ws.ID, State: ConversionImporting})

		e.reconcile(t)

		if got := e.project(t, p.ID); got.Status != StatusReady {
			t.Fatalf("project = %s", got.Status)
		}
		if len(e.containers.removed) != 0 {
			t.Fatalf("repo container removed: %v", e.containers.removed)
		}
		if conv := e.conversion(t, p.ID); conv.State != ConversionDone {
			t.Fatalf("conversion = %+v", conv)
		}
	})

	t.Run("in place, workspace gone", func(t *testing.T) {
		e := setupTest(t)
		e.giveUserKey(t)
		p := e.leftConversion(t, StatusReady, &Conversion{WorkspaceID: 999, InPlace: true, InitSHA: "init-sha", State: ConversionImporting, RestartPending: true, RotationPending: true})

		e.reconcile(t)

		if got := e.project(t, p.ID); got.Status != StatusReady {
			t.Fatalf("project = %s", got.Status)
		}
		if len(e.containers.removed) != 0 || len(e.workspaces.undone) != 0 {
			t.Fatalf("removed=%v undone=%v", e.containers.removed, e.workspaces.undone)
		}
		// Devpad cannot tell what happened, so the record is left as it was.
		if conv := e.conversion(t, p.ID); conv.State != ConversionImporting || !conv.RotationPending {
			t.Fatalf("conversion = %+v", conv)
		}
	})

	t.Run("in place, attached", func(t *testing.T) {
		e := setupTest(t)
		e.giveUserKey(t)
		ws := e.standalone()
		p := e.leftConversion(t, StatusReady, &Conversion{WorkspaceID: ws.ID, InPlace: true, State: ConversionImporting, RestartPending: true, RotationPending: true})
		ws.ProjectID, ws.RepoSeeded = p.ID, true

		e.reconcile(t)

		if got := e.project(t, p.ID); got.Status != StatusReady {
			t.Fatalf("project = %s", got.Status)
		}
		if len(e.containers.removed) != 0 {
			t.Fatalf("repo container removed: %v", e.containers.removed)
		}
		if conv := e.conversion(t, p.ID); conv.State != ConversionDone || !conv.KeyRotated {
			t.Fatalf("conversion = %+v", conv)
		}
	})
}

func TestReconcileLeavesAConversionWhoseWorkspaceIsGone(t *testing.T) {
	e := setupTest(t)
	p := e.leftConversion(t, StatusCreating, &Conversion{WorkspaceID: 999, InPlace: true, State: ConversionImporting})

	e.reconcile(t)

	if got := e.project(t, p.ID); got.Status != StatusCreating {
		t.Fatalf("project = %s", got.Status)
	}
	if len(e.containers.removed) != 0 {
		t.Fatalf("repo container removed: %v", e.containers.removed)
	}
	if conv := e.conversion(t, p.ID); conv.State != ConversionImporting {
		t.Fatalf("conversion = %+v", conv)
	}
}

// An undo that cannot run yet keeps the project and its record, so it is
// tried again, and never runs under a conversion of the same workspace.
func TestReconcileRetriesAnUndoThatCouldNotRun(t *testing.T) {
	cases := map[string]struct {
		block   func(e *testEnv, ws *workspace.Workspace)
		unblock func(e *testEnv, ws *workspace.Workspace)
	}{
		"workspace stopped": {
			block:   func(e *testEnv, ws *workspace.Workspace) { e.workspaces.setStatus(ws.ID, workspace.StatusStopped) },
			unblock: func(e *testEnv, ws *workspace.Workspace) { e.workspaces.setStatus(ws.ID, workspace.StatusRunning) },
		},
		"workspace being converted": {
			block: func(e *testEnv, ws *workspace.Workspace) {
				e.workspaces.mu.Lock()
				e.workspaces.converting[ws.ID] = true
				e.workspaces.mu.Unlock()
			},
			unblock: func(e *testEnv, ws *workspace.Workspace) {
				e.workspaces.mu.Lock()
				delete(e.workspaces.converting, ws.ID)
				e.workspaces.mu.Unlock()
			},
		},
		"undo failed": {
			block: func(e *testEnv, _ *workspace.Workspace) {
				e.workspaces.convertErr["undo"] = errors.New("agent unreachable")
			},
			unblock: func(e *testEnv, _ *workspace.Workspace) { delete(e.workspaces.convertErr, "undo") },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := setupTest(t)
			p, ws := e.interruptedConversion(t, ConversionImporting, false, "init-sha")
			c.block(e, ws)

			e.reconcile(t)
			if got := e.project(t, p.ID); got.Status != StatusCreating {
				t.Fatalf("project = %s", got.Status)
			}
			if len(e.containers.removed) != 0 || len(e.workspaces.undone) != 0 {
				t.Fatalf("removed=%v undone=%v before the undo could run", e.containers.removed, e.workspaces.undone)
			}

			c.unblock(e, ws)
			e.reconcile(t)
			if n := e.projectCount(t); n != 0 {
				t.Fatalf("%d projects left", n)
			}
			if !slices.Equal(e.workspaces.undone, []string{"init-sha"}) {
				t.Fatalf("undone = %v", e.workspaces.undone)
			}
		})
	}
}

// A workspace converted again since keeps the repo devpad made: the new
// conversion is using it. The stale project still goes.
func TestReconcileLeavesTheRepoOfAWorkspaceConvertedSince(t *testing.T) {
	e := setupTest(t)
	p, ws := e.interruptedConversion(t, ConversionImporting, false, "init-sha")
	other := e.createProject(t, CreateInput{Name: "other"})
	ws.ProjectID = other.ID

	e.reconcile(t)

	if len(e.workspaces.undone) != 0 {
		t.Fatalf("undid a repo in use: %v", e.workspaces.undone)
	}
	if got, err := e.svc.repo.GetByID(context.Background(), p.ID); err != nil || got != nil {
		t.Fatalf("stale project kept: %+v %v", got, err)
	}
	if got := e.project(t, other.ID); got.Status != StatusReady {
		t.Fatalf("other project = %s", got.Status)
	}
}

func TestConvertWorkspaceKeepsAProjectWhoseUndoCannotRun(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.workspaces.discovery = &agent.RepoDiscovery{}
	e.workspaces.convertErr["bundle"] = errors.New("bundle failed")
	e.workspaces.convertErr["undo"] = errors.New("agent unreachable")

	if _, err := e.convert(ws.ID, ConvertInput{Name: "App"}); err == nil {
		t.Fatal("expected the conversion to fail")
	}
	// The repo container goes, but the project and its record stay, so the
	// repo made from the files is removed later.
	projects, err := e.svc.repo.ListByUserID(context.Background(), e.userID)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects = %v %v", projects, err)
	}
	p := projects[0]
	if p.Status != StatusCreating || p.RepoContainerID != "" || len(e.containers.removed) != 1 {
		t.Fatalf("project = %+v removed=%v", p, e.containers.removed)
	}
	if conv := e.conversion(t, p.ID); conv.State != ConversionImporting || conv.InitSHA != "init-sha" {
		t.Fatalf("conversion = %+v", conv)
	}

	delete(e.workspaces.convertErr, "undo")
	e.reconcile(t)
	if n := e.projectCount(t); n != 0 {
		t.Fatalf("%d projects left", n)
	}
	if !slices.Equal(e.workspaces.undone, []string{"init-sha"}) {
		t.Fatalf("undone = %v", e.workspaces.undone)
	}
}

func TestConvertWorkspaceLeavesAChangedRepoAndRemovesTheProject(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.workspaces.discovery = &agent.RepoDiscovery{}
	e.workspaces.convertErr["bundle"] = errors.New("bundle failed")
	e.workspaces.convertErr["undo"] = &agent.AgentError{StatusCode: http.StatusConflict, Message: "changed"}

	if _, err := e.convert(ws.ID, ConvertInput{Name: "App"}); err == nil {
		t.Fatal("expected the conversion to fail")
	}
	if n := e.projectCount(t); n != 0 {
		t.Fatalf("%d projects left", n)
	}
}

func TestResumedRotationKeepsAKeyAlreadyReplaced(t *testing.T) {
	for _, tc := range []struct {
		name        string
		currentKey  string
		wantRotated int
		wantKey     string
	}{
		// The record of an earlier rotation was lost: the key the workspace
		// held is gone, so it is not replaced again.
		{"already replaced", "ssh-ed25519 REPLACED devpad-owner", 0, "ssh-ed25519 REPLACED devpad-owner"},
		{"still the old key", "ssh-ed25519 OLDKEY", 1, "ssh-ed25519 NEWKEY devpad-owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setupTest(t)
			e.giveUserKey(t)
			p, _ := e.interruptedConversion(t, ConversionAttached, true, "")
			conv := e.conversion(t, p.ID)
			conv.OldKeyFingerprint = sshKeyFingerprint("ssh-ed25519 OLDKEY devpad-owner")
			if err := e.svc.repo.UpdateConversion(context.Background(), conv); err != nil {
				t.Fatalf("update: %v", err)
			}
			if _, err := e.db.Conn().Exec(`UPDATE users SET ssh_public_key = ? WHERE id = ?`, tc.currentKey, e.userID); err != nil {
				t.Fatalf("setting key: %v", err)
			}

			e.reconcile(t)

			if len(e.keys.rotated) != tc.wantRotated {
				t.Fatalf("rotated %v", e.keys.rotated)
			}
			if got := e.conversion(t, p.ID); got.State != ConversionDone || !got.KeyRotated || got.PublicKey != tc.wantKey {
				t.Fatalf("conversion = %+v", got)
			}
		})
	}
}

func TestConvertWorkspaceRecordsTheKeyItHeld(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.giveUserKey(t)
	result, err := e.convert(ws.ID, ConvertInput{RepoPath: ".", Name: "App"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if conv := e.conversion(t, result.Project.ID); conv.OldKeyFingerprint != sshKeyFingerprint("ssh-ed25519 OLDKEY") || conv.OldKeyFingerprint == "" {
		t.Fatalf("fingerprint = %q", conv.OldKeyFingerprint)
	}
}

// failProgressRepo fails recording an attached conversion a number of
// times, as a database write might.
type failProgressRepo struct {
	*tokenCapturingRepo
	mu    sync.Mutex
	fails int
}

func (r *failProgressRepo) UpdateConversion(ctx context.Context, c *Conversion) error {
	r.mu.Lock()
	fail := c.State == ConversionAttached && r.fails > 0
	if fail {
		r.fails--
	}
	r.mu.Unlock()
	if fail {
		return errors.New("database is locked")
	}
	return r.tokenCapturingRepo.UpdateConversion(ctx, c)
}

func TestConvertWorkspaceReportsAProgressWriteThatFailed(t *testing.T) {
	e := setupTest(t)
	ws := e.standalone()
	e.giveUserKey(t)
	e.svc.repo = &failProgressRepo{tokenCapturingRepo: &tokenCapturingRepo{Repository: NewRepository(e.db.Conn()), agent: e.repoAgent}, fails: 1}

	result, err := e.svc.ConvertWorkspace(context.Background(), e.userID, ws.ID, ConvertInput{RepoPath: ".", Name: "App"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(result.ProgressError, "may run again") {
		t.Fatalf("progress error not reported: %q", result.ProgressError)
	}
	// It is kept with the record, for the notice.
	if conv := e.conversion(t, result.Project.ID); conv.ProgressError == "" || conv.State != ConversionDone {
		t.Fatalf("conversion = %+v", conv)
	}
	if got := e.project(t, result.Project.ID); got.Status != StatusReady {
		t.Fatalf("project = %s", got.Status)
	}
}
