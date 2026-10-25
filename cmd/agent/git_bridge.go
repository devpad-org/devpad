package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The git bridge lets git in a project workspace use the "devpad" remote.
// The workspace has no route to devpad or to the project's repo, so a push or
// fetch is only a request: git-remote-devpad hands it to this agent over a
// unix socket, devpad picks it up by long-polling the agent, does the work
// itself under its own rules (it pulls a pushed branch by bundle and
// publishes it exactly like the Publish button), and posts the result back.
// Nothing here can move a canonical branch.

const (
	// devpadRemoteURL is the URL of the devpad remote; git runs
	// git-remote-devpad for any "devpad::" URL.
	devpadRemoteURL = "devpad::project"

	gitBridgeMaxOutstanding = 8
	gitBridgeMaxPerMinute   = 60
	// gitBridgeMaxBranches is devpad's limit on branches per request; the
	// helper fetches more in batches.
	gitBridgeMaxBranches = 64
	gitBridgeMaxPollWait = 30 * time.Second
	// gitBridgePickupTimeout bounds how long a request waits for devpad to
	// pick it up, and gitBridgeServingWindow how recently devpad must have
	// polled for the bridge to accept requests at all.
	gitBridgePickupTimeout = 45 * time.Second
	gitBridgeServingWindow = 15 * time.Second

	defaultGitBridgeSocket = "/tmp/devpad/git.sock"
)

var errBridgeNotServing = errors.New("devpad is not serving git requests for this workspace; only a project's own workspace can push to or fetch from devpad")

// errDevpadRestarted answers requests a previous devpad poller picked up but
// never answered: devpad restarted or stopped polling while handling them.
const errDevpadRestarted = "devpad restarted while handling this request; check the branch and retry"

var sessionPattern = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

type gitBridgeRequest struct {
	ID       string   `json:"id"`
	Op       string   `json:"op"` // list, fetch or push
	Branches []string `json:"branches,omitempty"`
	Force    bool     `json:"force,omitempty"`
}

// gitBridgeResult is devpad's answer. Error is a refusal or failure to show
// the user; the rest depends on the op.
type gitBridgeResult struct {
	Error         string    `json:"error,omitempty"`
	DefaultBranch string    `json:"defaultBranch,omitempty"`
	Refs          []repoRef `json:"refs,omitempty"`
	SHA           string    `json:"sha,omitempty"`
}

type bridgeCall struct {
	req  gitBridgeRequest
	done chan gitBridgeResult
	// session is the devpad poller that picked the request up; only it may
	// answer.
	session string
}

type gitBridge struct {
	mu       sync.Mutex
	pending  []*bridgeCall
	inflight map[string]*bridgeCall
	wake     chan struct{}
	polling  int
	lastSeen time.Time
	recent   []time.Time
	now      func() time.Time
}

func newGitBridge() *gitBridge {
	return &gitBridge{inflight: make(map[string]*bridgeCall), wake: make(chan struct{}), now: time.Now}
}

var bridge = newGitBridge()

// serving reports whether devpad is polling this agent. Only devpad's
// poller for a project's own workspace polls, so in a sandbox or a legacy
// workspace requests fail at once instead of hanging.
func (b *gitBridge) serving() bool {
	return b.polling > 0 || len(b.inflight) > 0 || (!b.lastSeen.IsZero() && b.now().Sub(b.lastSeen) < gitBridgeServingWindow)
}

// opTimeout is how long a helper waits for devpad. Transfers wait longer
// than devpad's own 20-minute limit, so a slow push devpad completes is not
// reported to git as a failure.
func opTimeout(op string) time.Duration {
	if op == "list" {
		return 2 * time.Minute
	}
	return 25 * time.Minute
}

// submit queues a request for devpad and waits for its result, until ctx is
// done (the helper went away) or the op's timeout.
func (b *gitBridge) submit(ctx context.Context, req gitBridgeRequest) (gitBridgeResult, error) {
	id, err := randomHex(16)
	if err != nil {
		return gitBridgeResult{}, err
	}
	req.ID = id
	call := &bridgeCall{req: req, done: make(chan gitBridgeResult, 1)}

	b.mu.Lock()
	if !b.serving() {
		b.mu.Unlock()
		return gitBridgeResult{}, errBridgeNotServing
	}
	if len(b.pending)+len(b.inflight) >= gitBridgeMaxOutstanding {
		b.mu.Unlock()
		return gitBridgeResult{}, errors.New("too many git requests in flight; try again shortly")
	}
	now := b.now()
	kept := b.recent[:0]
	for _, at := range b.recent {
		if now.Sub(at) < time.Minute {
			kept = append(kept, at)
		}
	}
	b.recent = kept
	if len(b.recent) >= gitBridgeMaxPerMinute {
		b.mu.Unlock()
		return gitBridgeResult{}, errors.New("too many git requests; try again in a minute")
	}
	b.recent = append(b.recent, now)
	b.pending = append(b.pending, call)
	close(b.wake)
	b.wake = make(chan struct{})
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, opTimeout(req.Op))
	defer cancel()
	pickup := time.NewTimer(gitBridgePickupTimeout)
	defer pickup.Stop()
	for {
		select {
		case res := <-call.done:
			return res, nil
		case <-pickup.C:
			if b.dropPending(call) {
				return gitBridgeResult{}, errors.New("devpad did not pick up the request")
			}
		case <-ctx.Done():
			b.drop(call)
			return gitBridgeResult{}, fmt.Errorf("waiting for devpad: %w", ctx.Err())
		}
	}
}

// dropPending removes a call devpad has not picked up yet, reporting whether
// it was still pending.
func (b *gitBridge) dropPending(call *bridgeCall) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, c := range b.pending {
		if c == call {
			b.pending = append(b.pending[:i], b.pending[i+1:]...)
			return true
		}
	}
	return false
}

// drop forgets a call whose helper went away; a result posted later is
// discarded.
func (b *gitBridge) drop(call *bridgeCall) {
	if b.dropPending(call) {
		return
	}
	b.mu.Lock()
	delete(b.inflight, call.req.ID)
	b.mu.Unlock()
}

// poll hands devpad's poller session the pending requests, waiting up to
// wait for one. A new session means devpad restarted or replaced its poller,
// so requests an earlier session picked up will never be answered; they fail
// now instead of when their helper gives up.
func (b *gitBridge) poll(ctx context.Context, session string, wait time.Duration) []gitBridgeRequest {
	timer := time.NewTimer(wait)
	defer timer.Stop()

	b.mu.Lock()
	b.polling++
	b.lastSeen = b.now()
	defer func() {
		b.polling--
		b.lastSeen = b.now()
		b.mu.Unlock()
	}()
	for id, c := range b.inflight {
		if c.session != session {
			delete(b.inflight, id)
			c.done <- gitBridgeResult{Error: errDevpadRestarted}
		}
	}
	for {
		if len(b.pending) > 0 {
			reqs := make([]gitBridgeRequest, 0, len(b.pending))
			for _, c := range b.pending {
				c.session = session
				b.inflight[c.req.ID] = c
				reqs = append(reqs, c.req)
			}
			b.pending = nil
			return reqs
		}
		wake := b.wake
		b.mu.Unlock()
		select {
		case <-wake:
		case <-timer.C:
			b.mu.Lock()
			return []gitBridgeRequest{}
		case <-ctx.Done():
			b.mu.Lock()
			return []gitBridgeRequest{}
		}
		b.mu.Lock()
	}
}

// complete delivers devpad's result from the session that picked the
// request up, reporting whether a helper still waits for it.
func (b *gitBridge) complete(session, id string, res gitBridgeResult) bool {
	b.mu.Lock()
	call := b.inflight[id]
	if call == nil || call.session != session {
		b.mu.Unlock()
		return false
	}
	delete(b.inflight, id)
	b.lastSeen = b.now()
	b.mu.Unlock()
	call.done <- res
	return true
}

// handlePoll is devpad's long poll for requests.
func (b *gitBridge) handlePoll(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if !sessionPattern.MatchString(session) {
		writeErr(w, http.StatusBadRequest, "invalid session")
		return
	}
	wait := gitBridgeMaxPollWait
	if s := r.URL.Query().Get("wait"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			writeErr(w, http.StatusBadRequest, "invalid wait")
			return
		}
		wait = min(d, gitBridgeMaxPollWait)
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": b.poll(r.Context(), session, wait)})
}

// handleResult is devpad posting a request's result.
func (b *gitBridge) handleResult(w http.ResponseWriter, r *http.Request) {
	var res gitBridgeResult
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&res); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid result")
		return
	}
	if !b.complete(r.URL.Query().Get("session"), r.PathValue("id"), res) {
		writeErr(w, http.StatusGone, "nobody is waiting for that request")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleHelperRequest serves git-remote-devpad over the unix socket.
func (b *gitBridge) handleHelperRequest(w http.ResponseWriter, r *http.Request) {
	var req gitBridgeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, gitBridgeResult{Error: "invalid request"})
		return
	}
	if req.Force {
		// The helper refuses force pushes itself; anything else on this
		// socket asking for one is refused here too, and by devpad.
		writeJSON(w, http.StatusBadRequest, gitBridgeResult{Error: "devpad refuses force pushes; it only fast-forwards"})
		return
	}
	switch req.Op {
	case "list":
		req.Branches = nil
	case "fetch", "push":
		if len(req.Branches) == 0 || len(req.Branches) > gitBridgeMaxBranches || (req.Op == "push" && len(req.Branches) != 1) {
			writeJSON(w, http.StatusBadRequest, gitBridgeResult{Error: "invalid branch list"})
			return
		}
		for _, branch := range req.Branches {
			if err := validateBranchName(r.Context(), branch, "branch"); err != nil {
				writeJSON(w, http.StatusBadRequest, gitBridgeResult{Error: err.Error()})
				return
			}
		}
	default:
		writeJSON(w, http.StatusBadRequest, gitBridgeResult{Error: "unknown op"})
		return
	}
	res, err := b.submit(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, gitBridgeResult{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func gitBridgeSocketPath() string {
	if p := os.Getenv("DEVPAD_GIT_SOCKET"); p != "" {
		return p
	}
	return defaultGitBridgeSocket
}

// serveGitBridgeSocket listens for git-remote-devpad on a unix socket only
// this user can open. A socket, unlike a port, cannot be reached through the
// agent's port proxy or a preview.
func serveGitBridgeSocket(b *gitBridge, path string) (*http.Server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating socket directory: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("removing stale socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("restricting socket: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /request", b.handleHelperRequest)
	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("git bridge socket: %v", err)
		}
	}()
	return srv, nil
}

// remoteHelperDir is where the git-remote-devpad link goes; empty means next
// to the agent binary, which is on PATH.
var remoteHelperDir = ""

// handleGitBridgeRemote is devpad setting up the devpad remote in a
// project's workspace: it links git-remote-devpad to this binary and points
// the remote at it. An existing remote of that name with another URL is left
// alone.
func handleGitBridgeRemote(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	if err := installRemoteHelper(ctx); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if gitExec(ctx, "rev-parse", "--git-dir") != nil {
		writeErr(w, http.StatusConflict, "workspace has no repository")
		return
	}
	if out, err := gitOutput(ctx, "config", "--get", "remote."+sandboxBaseRemote+".url"); err == nil {
		if url := strings.TrimSpace(out); url != devpadRemoteURL {
			writeErr(w, http.StatusConflict, "a remote named "+sandboxBaseRemote+" already exists")
			return
		}
	}
	steps := [][]string{
		{"config", "remote." + sandboxBaseRemote + ".url", devpadRemoteURL},
		{"config", "--replace-all", "remote." + sandboxBaseRemote + ".fetch", "+refs/heads/*:refs/remotes/" + sandboxBaseRemote + "/*"},
		// A bare `git push devpad` pushes the current branch.
		{"config", "--replace-all", "remote." + sandboxBaseRemote + ".push", "HEAD"},
	}
	for _, args := range steps {
		if out, err := gitOutput(ctx, args...); err != nil {
			writeErr(w, http.StatusInternalServerError, "configuring the devpad remote: "+strings.TrimSpace(out))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// installRemoteHelper links git-remote-devpad to the running agent binary.
// The binary's directory belongs to root, so it falls back to sudo, which
// the workspace user has.
func installRemoteHelper(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating the agent binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := remoteHelperDir
	if dir == "" {
		dir = filepath.Dir(exe)
	}
	link := filepath.Join(dir, remoteHelperName)
	if target, err := os.Readlink(link); err == nil && target == exe {
		return nil
	}
	if err := os.Remove(link); err == nil || errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink(exe, link); err == nil {
			return nil
		}
	}
	if out, err := exec.CommandContext(ctx, "sudo", "-n", "ln", "-sfn", exe, link).CombinedOutput(); err != nil {
		return fmt.Errorf("installing %s: %s", remoteHelperName, strings.TrimSpace(string(out)))
	}
	return nil
}
