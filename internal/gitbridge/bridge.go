// Package gitbridge answers git-remote-devpad, the git remote helper in a
// project's workspace. The workspace cannot reach devpad, so devpad polls
// each running project workspace's agent for the requests the helper queued,
// does the work through the project service, and posts the results back.
//
// The helper serves two remotes. devpad is the project: fetching it brings
// the canonical branches, and syncs from the upstream first when the last
// sync is stale. origin is the project's upstream, read-only: listing it
// syncs, fetching it brings the upstream's branches as last synced, and
// pushing to it is refused.
//
// A push is only ever a request: devpad pulls the branch by bundle and
// publishes it exactly as the Publish button does, so protected branches,
// branches owned by sandboxes and non-fast-forward updates are refused there.
// The IDE assistant runs in the same workspace and can push the same way;
// that gives it nothing the Publish button does not.
//
// The agent is inside the workspace and so under the workspace's control: it
// can answer polls instantly, return junk or flood requests. Every limit on
// how much work a workspace can make devpad do is therefore enforced here,
// not in the agent.
package gitbridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Workspaces finds project workspaces and their agents.
type Workspaces interface {
	ListAll(ctx context.Context) ([]*workspace.Workspace, error)
	workspace.GitBridgeService
}

// Projects does the work a request asks for, under the project's rules.
type Projects interface {
	Get(ctx context.Context, userID, projectID int64) (*project.Project, error)
	Branches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error)
	FetchWorkspaceBranches(ctx context.Context, userID, projectID int64, branches []string) error
	PublishWorkspaceBranch(ctx context.Context, userID, projectID int64, branch string) (*agent.RepoBranch, error)
	SyncUpstream(ctx context.Context, userID, projectID int64) (*project.UpstreamStatus, error)
	SyncUpstreamIfStale(ctx context.Context, userID, projectID int64, wait time.Duration) string
	UpstreamBranches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error)
	FetchWorkspaceUpstreamBranches(ctx context.Context, userID, projectID int64, branches []string) error
}

const (
	// maxBranches bounds the branches in one fetch; the helper batches.
	maxBranches = 64
	// maxBranchName bounds a branch name from a request.
	maxBranchName = 255
	// maxRequestsPerPoll bounds the requests taken from one poll; an honest
	// agent never holds more than 8 at once.
	maxRequestsPerPoll = 8
	// maxConcurrent bounds the requests answered at once per workspace.
	maxConcurrent = 4

	// A workspace may ask for a burst of requests, then one a second; and
	// fetch a burst of branches, then about eight a second.
	requestBurst      = 20
	requestsPerMinute = 60
	branchBurst       = 512
	branchesPerMinute = 512

	// Bridge log lines per workspace: a burst, then one every six seconds.
	logBurst      = 10
	logsPerMinute = 10
	// maxLogText bounds text from the workspace in a log line or a notice.
	maxLogText = 300

	maxPublishes = 200
	maxRefusals  = 50

	listTimeout     = time.Minute
	transferTimeout = 20 * time.Minute
	// staleSyncWait bounds how long listing the devpad remote waits for a
	// sync from the upstream it started.
	staleSyncWait = 20 * time.Second
	// stoppedTimeout bounds answering requests after the poller stopped.
	stoppedTimeout = 10 * time.Second
	// configureEvery bounds retries of setting up the devpad remote.
	configureEvery = time.Minute
	// revalidateEvery bounds how long a poller trusts its record of the
	// workspace between request batches.
	revalidateEvery = 30 * time.Second

	errStopped     = "devpad stopped while handling this request; check the branch and retry"
	errTooMany     = "too many git requests; try again shortly"
	errTooManyRefs = "fetching too many branches at once; try again in a minute"
	errReadOnly    = "origin is the project's upstream and is read-only here; push your branch to devpad instead"
	errSync        = "could not fetch from the upstream; the project panel says why"
)

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

// Bridge polls every running project workspace.
type Bridge struct {
	workspaces Workspaces
	projects   Projects

	scanEvery  time.Duration
	pollWait   time.Duration
	minPollGap time.Duration
	backoffMin time.Duration
	backoffMax time.Duration
	now        func() time.Time
	// epoch changes on every start, so the IDE can tell its cursor into the
	// activity is from an earlier run.
	epoch string

	mu      sync.Mutex
	pollers map[int64]*poller
	events  map[eventKey]*projectEvents
	logs    map[int64]*rateLog
	wg      sync.WaitGroup
}

type poller struct {
	containerID string
	cancel      context.CancelFunc
}

type eventKey struct{ userID, projectID int64 }

// projectEvents keeps a project's pushes. Publishes and refusals are kept
// apart so that a flood of refusals cannot push a publish out of sight.
type projectEvents struct {
	seq       int64
	published []Event
	refused   []Event
}

// Event records a push made through the devpad remote, published or
// refused, so the IDE can show it even when the assistant made it.
type Event struct {
	Seq    int64     `json:"seq"`
	Branch string    `json:"branch"`
	OK     bool      `json:"ok"`
	SHA    string    `json:"sha,omitempty"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

// New returns a bridge; Run starts it.
func New(workspaces Workspaces, projects Projects) *Bridge {
	return &Bridge{
		workspaces: workspaces,
		projects:   projects,
		scanEvery:  5 * time.Second,
		pollWait:   25 * time.Second,
		minPollGap: 250 * time.Millisecond,
		backoffMin: time.Second,
		backoffMax: time.Minute,
		now:        time.Now,
		epoch:      randomHex(8),
		pollers:    make(map[int64]*poller),
		events:     make(map[eventKey]*projectEvents),
		logs:       make(map[int64]*rateLog),
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("reading random bytes: %v", err))
	}
	return hex.EncodeToString(b)
}

// Run keeps one poller per running project workspace until ctx ends, then
// stops them all.
func (b *Bridge) Run(ctx context.Context) {
	ticker := time.NewTicker(b.scanEvery)
	defer ticker.Stop()
	for {
		b.reconcile(ctx)
		select {
		case <-ctx.Done():
			b.mu.Lock()
			for id, p := range b.pollers {
				p.cancel()
				delete(b.pollers, id)
			}
			b.mu.Unlock()
			b.wg.Wait()
			return
		case <-ticker.C:
		}
	}
}

// reconcile starts pollers for running project workspaces and stops those
// whose workspace stopped, went away or got a new container. Sandboxes and
// standalone workspaces are never polled. It also forgets the pushes and
// log limits of projects and workspaces that are gone.
func (b *Bridge) reconcile(ctx context.Context) {
	all, err := b.workspaces.ListAll(ctx)
	if err != nil {
		log.Printf("git bridge: listing workspaces: %v", err)
		return
	}
	want := make(map[int64]*workspace.Workspace)
	exists := make(map[int64]bool, len(all))
	projects := make(map[eventKey]bool)
	for _, ws := range all {
		exists[ws.ID] = true
		if ws.ProjectID != 0 {
			projects[eventKey{ws.UserID, ws.ProjectID}] = true
		}
		if ws.IsProjectWorkspace() && ws.Status == workspace.StatusRunning && ws.ContainerID != "" && ws.RepoSeeded {
			want[ws.ID] = ws
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for key := range b.events {
		if !projects[key] {
			delete(b.events, key)
		}
	}
	for id := range b.logs {
		if !exists[id] {
			delete(b.logs, id)
		}
	}
	for id, p := range b.pollers {
		if ws, ok := want[id]; !ok || ws.ContainerID != p.containerID {
			p.cancel()
			delete(b.pollers, id)
		}
	}
	if ctx.Err() != nil {
		return
	}
	for id, ws := range want {
		if _, ok := b.pollers[id]; ok {
			continue
		}
		pctx, cancel := context.WithCancel(ctx)
		b.pollers[id] = &poller{containerID: ws.ContainerID, cancel: cancel}
		b.wg.Add(1)
		go func(ws workspace.Workspace) {
			defer b.wg.Done()
			b.poll(pctx, &ws)
		}(*ws)
	}
}

// polled reports which workspaces have pollers, for tests.
func (b *Bridge) polled() map[int64]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make(map[int64]bool, len(b.pollers))
	for id := range b.pollers {
		ids[id] = true
	}
	return ids
}

// pollState is one poller's view of its workspace. Only the poller's own
// goroutine touches it.
type pollState struct {
	ws         *workspace.Workspace
	agent      workspace.GitBridgeAgent
	resolvedAt time.Time
	// session names this poller to the agent, so the agent fails requests
	// an earlier poller picked up and never answered.
	session  string
	requests *bucket
	branches *bucket
	// configured is set once the devpad remote is set up; oldAgent once an
	// agent without the bridge was reported.
	configured   bool
	configuredAt time.Time
	oldAgent     bool
}

// resolve refreshes devpad's record of the workspace and its agent. The
// project and user every request is answered for come from this record.
func (b *Bridge) resolve(ctx context.Context, st *pollState) error {
	ws, a, err := b.workspaces.GitBridgeAgent(ctx, st.ws.UserID, st.ws.ID)
	if err != nil {
		st.agent = nil
		return err
	}
	st.ws, st.agent, st.resolvedAt = ws, a, b.now()
	return nil
}

func (b *Bridge) poll(ctx context.Context, ws *workspace.Workspace) {
	now := b.now()
	st := &pollState{
		ws:       ws,
		session:  randomHex(16),
		requests: newBucket(requestBurst, requestsPerMinute, now),
		branches: newBucket(branchBurst, branchesPerMinute, now),
	}
	slots := make(chan struct{}, maxConcurrent)
	backoff := b.backoffMin
	retry := func() {
		b.sleep(ctx, backoff)
		backoff = min(backoff*2, b.backoffMax)
	}
	var lastPoll time.Time
	for ctx.Err() == nil {
		if st.agent == nil || b.now().Sub(st.resolvedAt) >= revalidateEvery {
			if err := b.resolve(ctx, st); err != nil {
				if ctx.Err() == nil {
					b.logf(ws.ID, "finding the workspace's agent: %v", err)
				}
				retry()
				continue
			}
		}
		b.configure(ctx, st)

		// Pace polls on devpad's side: a gap between them always, and no
		// poll while the workspace is out of requests.
		if gap := b.minPollGap - b.now().Sub(lastPoll); gap > 0 {
			b.sleep(ctx, gap)
		}
		if wait := st.requests.wait(b.now()); wait > 0 {
			b.sleep(ctx, wait)
			continue
		}
		if ctx.Err() != nil {
			return
		}
		lastPoll = b.now()
		reqs, err := st.agent.GitBridgeRequests(ctx, st.session, b.pollWait)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var agentErr *agent.AgentError
			if errors.As(err, &agentErr) && agentErr.StatusCode == 404 {
				// The agent predates the bridge; it is replaced on restart.
				if !st.oldAgent {
					st.oldAgent = true
					b.logf(ws.ID, "the agent has no git bridge; it gets one when the workspace restarts")
				}
				backoff = b.backoffMax
			} else {
				b.logf(ws.ID, "polling for git requests: %v", err)
			}
			// The agent may have restarted or moved; find it and set the
			// remote up again.
			st.agent, st.configured = nil, false
			retry()
			continue
		}
		if len(reqs) == 0 {
			if b.now().Sub(lastPoll) < b.pollWait/2 {
				// An agent holds an empty poll for the whole wait; one that
				// answers early is broken or hostile, so back off.
				retry()
			} else {
				backoff = b.backoffMin
			}
			continue
		}
		backoff = b.backoffMin
		if len(reqs) > maxRequestsPerPoll {
			b.logf(ws.ID, "the agent returned %d requests at once; answering the first %d", len(reqs), maxRequestsPerPoll)
			reqs = reqs[:maxRequestsPerPoll]
		}
		// Answer the batch for devpad's current record of the workspace.
		// A batch that cannot be checked is refused through the agent that
		// sent it.
		sender := st.agent
		if err := b.resolve(ctx, st); err != nil {
			if ctx.Err() == nil {
				b.logf(ws.ID, "checking the workspace before answering: %v", err)
			}
			for _, req := range reqs {
				if requestIDPattern.MatchString(req.ID) {
					b.respondDetached(ctx, ws.ID, sender, st.session, req.ID, agent.GitBridgeResult{Error: "devpad could not complete the request"})
				}
			}
			retry()
			continue
		}
		b.dispatch(ctx, st, slots, reqs)
	}
}

// dispatch answers a batch of requests, each alongside the others and the
// next poll so a long push does not leave a fetch waiting. Requests past the
// workspace's limits are refused without any work.
func (b *Bridge) dispatch(ctx context.Context, st *pollState, slots chan struct{}, reqs []agent.GitBridgeRequest) {
	ws, a, session := st.ws, st.agent, st.session
	for i, req := range reqs {
		if !requestIDPattern.MatchString(req.ID) {
			continue
		}
		now := b.now()
		if !st.requests.take(now, 1) {
			b.respond(ctx, ws.ID, a, session, req.ID, agent.GitBridgeResult{Error: errTooMany})
			continue
		}
		if req.Op == "fetch" && len(req.Branches) > 0 && len(req.Branches) <= maxBranches && !st.branches.take(now, len(req.Branches)) {
			b.respond(ctx, ws.ID, a, session, req.ID, agent.GitBridgeResult{Error: errTooManyRefs})
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			// Stopping: tell the helpers rather than leave them waiting.
			for _, rest := range reqs[i:] {
				if requestIDPattern.MatchString(rest.ID) {
					b.respondDetached(ctx, ws.ID, a, session, rest.ID, agent.GitBridgeResult{Error: errStopped})
				}
			}
			return
		}
		b.wg.Add(1)
		go func(req agent.GitBridgeRequest) {
			defer b.wg.Done()
			defer func() { <-slots }()
			b.answer(ctx, ws, a, session, req)
		}(req)
	}
}

// configure sets up the workspace's remotes: devpad always, and origin when
// the project has an upstream. The workspace is sent a hash of the upstream
// URL, never the URL, so it can replace an origin a converted workspace
// kept.
func (b *Bridge) configure(ctx context.Context, st *pollState) {
	if st.configured || b.now().Sub(st.configuredAt) < configureEvery {
		return
	}
	st.configuredAt = b.now()
	p, err := b.projects.Get(ctx, st.ws.UserID, st.ws.ProjectID)
	if err != nil {
		if ctx.Err() == nil {
			b.logf(st.ws.ID, "reading the project to set up its remotes: %v", err)
		}
		return
	}
	remotes := agent.GitBridgeRemotes{Upstream: p.UpstreamURL != ""}
	if remotes.Upstream {
		sum := sha256.Sum256([]byte(p.UpstreamURL))
		remotes.UpstreamURLHash = hex.EncodeToString(sum[:])
	}
	err = st.agent.GitBridgeConfigureRemote(ctx, remotes)
	var agentErr *agent.AgentError
	switch {
	case err == nil:
		st.configured = true
	case errors.As(err, &agentErr) && agentErr.StatusCode == 409:
		// Another remote has the name, or the repo is missing; the helper
		// is only reached through our remote, so keep polling.
		b.logf(st.ws.ID, "%s", agentErr.Message)
		st.configured = true
	case errors.As(err, &agentErr) && agentErr.StatusCode == 404:
		// An agent without the bridge; the poll reports it once.
		st.configured = true
	case ctx.Err() == nil:
		b.logf(st.ws.ID, "setting up the devpad remote: %v", err)
	}
}

// answer handles a request and posts the result. If the poller stops while
// the work runs, the helper is told devpad stopped: the work may or may not
// have landed.
func (b *Bridge) answer(ctx context.Context, ws *workspace.Workspace, a workspace.GitBridgeAgent, session string, req agent.GitBridgeRequest) {
	result := b.handle(ctx, ws, req)
	if ctx.Err() != nil {
		b.respondDetached(ctx, ws.ID, a, session, req.ID, agent.GitBridgeResult{Error: errStopped})
		return
	}
	b.respond(ctx, ws.ID, a, session, req.ID, result)
}

func (b *Bridge) respond(ctx context.Context, wsID int64, a workspace.GitBridgeAgent, session, id string, result agent.GitBridgeResult) {
	if err := a.GitBridgeRespond(ctx, session, id, result); err != nil && ctx.Err() == nil {
		var agentErr *agent.AgentError
		if !errors.As(err, &agentErr) || agentErr.StatusCode != 410 {
			b.logf(wsID, "answering a request: %v", err)
		}
	}
}

// respondDetached answers on a short context of its own, for when the
// poller's context has ended.
func (b *Bridge) respondDetached(ctx context.Context, wsID int64, a workspace.GitBridgeAgent, session, id string, result agent.GitBridgeResult) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stoppedTimeout)
	defer cancel()
	b.respond(ctx, wsID, a, session, id, result)
}

func (b *Bridge) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// validBranch reports whether a branch name from a request is one devpad
// would act on at all.
func validBranch(name string) bool {
	return len(name) <= maxBranchName && project.ValidBranchName(name)
}

// handle does what a request asks under the project's rules. Everything in
// the request comes from the workspace; the project and user come from
// devpad's record of the workspace.
func (b *Bridge) handle(ctx context.Context, ws *workspace.Workspace, req agent.GitBridgeRequest) agent.GitBridgeResult {
	switch req.Remote {
	case "":
	case agent.GitBridgeUpstream:
		return b.handleUpstream(ctx, ws, req)
	default:
		return agent.GitBridgeResult{Error: "unknown remote"}
	}
	switch req.Op {
	case "list":
		ctx, cancel := context.WithTimeout(ctx, listTimeout)
		defer cancel()
		// A fetch from devpad brings the upstream's latest along when the
		// last sync is stale; a sync that fails or runs long only warns.
		warning := b.projects.SyncUpstreamIfStale(ctx, ws.UserID, ws.ProjectID, staleSyncWait)
		refs, err := b.projects.Branches(ctx, ws.UserID, ws.ProjectID)
		if err != nil {
			return agent.GitBridgeResult{Error: b.message(ws, err)}
		}
		return agent.GitBridgeResult{DefaultBranch: refs.DefaultBranch, Refs: refs.Branches, Warning: warning}

	case "fetch":
		if len(req.Branches) == 0 || len(req.Branches) > maxBranches {
			return agent.GitBridgeResult{Error: fmt.Sprintf("fetch between 1 and %d branches at a time", maxBranches)}
		}
		for _, branch := range req.Branches {
			if !validBranch(branch) {
				return agent.GitBridgeResult{Error: "invalid branch name"}
			}
		}
		ctx, cancel := context.WithTimeout(ctx, transferTimeout)
		defer cancel()
		if err := b.projects.FetchWorkspaceBranches(ctx, ws.UserID, ws.ProjectID, req.Branches); err != nil {
			return agent.GitBridgeResult{Error: b.message(ws, err)}
		}
		return agent.GitBridgeResult{}

	case "push":
		// Malformed and forced pushes are refused before anything is
		// recorded: the helper sends neither, so only something faking the
		// helper does, and its junk must not crowd the IDE's notices.
		if len(req.Branches) != 1 {
			return agent.GitBridgeResult{Error: "push one branch at a time"}
		}
		branch := req.Branches[0]
		if !validBranch(branch) {
			return agent.GitBridgeResult{Error: "invalid branch name"}
		}
		if req.Force {
			return agent.GitBridgeResult{Error: "devpad refuses force pushes; it only fast-forwards"}
		}
		ctx, cancel := context.WithTimeout(ctx, transferTimeout)
		defer cancel()
		published, err := b.projects.PublishWorkspaceBranch(ctx, ws.UserID, ws.ProjectID, branch)
		if err != nil {
			msg := b.message(ws, err)
			b.record(ws, Event{Branch: branch, Error: clean(msg)})
			return agent.GitBridgeResult{Error: msg}
		}
		b.record(ws, Event{Branch: branch, OK: true, SHA: published.SHA})
		return agent.GitBridgeResult{SHA: published.SHA}
	}
	return agent.GitBridgeResult{Error: "unsupported request"}
}

// handleUpstream answers the origin remote, the read-only view of the
// project's upstream: listing it syncs, fetching brings the upstream's
// copies of branches as last synced, and pushing is refused.
func (b *Bridge) handleUpstream(ctx context.Context, ws *workspace.Workspace, req agent.GitBridgeRequest) agent.GitBridgeResult {
	switch req.Op {
	case "list":
		ctx, cancel := context.WithTimeout(ctx, transferTimeout)
		defer cancel()
		if _, err := b.projects.SyncUpstream(ctx, ws.UserID, ws.ProjectID); err != nil {
			if errors.Is(err, project.ErrNoUpstream) || errors.Is(err, project.ErrNotReady) || errors.Is(err, project.ErrDeleting) {
				return agent.GitBridgeResult{Error: err.Error()}
			}
			return agent.GitBridgeResult{Error: errSync}
		}
		refs, err := b.projects.UpstreamBranches(ctx, ws.UserID, ws.ProjectID)
		if err != nil {
			return agent.GitBridgeResult{Error: b.message(ws, err)}
		}
		return agent.GitBridgeResult{DefaultBranch: refs.DefaultBranch, Refs: refs.Branches}

	case "fetch":
		if len(req.Branches) == 0 || len(req.Branches) > maxBranches {
			return agent.GitBridgeResult{Error: fmt.Sprintf("fetch between 1 and %d branches at a time", maxBranches)}
		}
		for _, branch := range req.Branches {
			if !validBranch(branch) {
				return agent.GitBridgeResult{Error: "invalid branch name"}
			}
		}
		ctx, cancel := context.WithTimeout(ctx, transferTimeout)
		defer cancel()
		if err := b.projects.FetchWorkspaceUpstreamBranches(ctx, ws.UserID, ws.ProjectID, req.Branches); err != nil {
			return agent.GitBridgeResult{Error: b.message(ws, err)}
		}
		return agent.GitBridgeResult{}

	case "push":
		return agent.GitBridgeResult{Error: errReadOnly}
	}
	return agent.GitBridgeResult{Error: "unsupported request"}
}

// message turns an error into text for git to show. Errors devpad expects
// are explained; anything else is logged and summarized, so internal detail
// does not reach the workspace.
func (b *Bridge) message(ws *workspace.Workspace, err error) string {
	var agentErr *agent.AgentError
	switch {
	case errors.Is(err, project.ErrPublishProtected):
		return "this branch is protected; it only changes through a merge request"
	case errors.Is(err, project.ErrBranchInUse):
		return "an agent's sandbox owns this branch"
	case errors.Is(err, project.ErrBranchPublishing):
		return "this branch is already being published"
	case errors.Is(err, project.ErrInvalidInput), errors.Is(err, project.ErrNoUpstream):
		return err.Error()
	case errors.Is(err, project.ErrNotReady), errors.Is(err, project.ErrDeleting), errors.Is(err, workspace.ErrNotRunning):
		return err.Error()
	case errors.As(err, &agentErr) && agentErr.StatusCode >= 400 && agentErr.StatusCode < 500:
		return clean(agentErr.Message)
	}
	b.logf(ws.ID, "%v", err)
	return "devpad could not complete the request"
}

func (b *Bridge) record(ws *workspace.Workspace, e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := eventKey{ws.UserID, ws.ProjectID}
	pe := b.events[key]
	if pe == nil {
		pe = &projectEvents{}
		b.events[key] = pe
	}
	pe.seq++
	e.Seq = pe.seq
	e.At = b.now()
	if e.OK {
		pe.published = keepLast(append(pe.published, e), maxPublishes)
	} else {
		pe.refused = keepLast(append(pe.refused, e), maxRefusals)
	}
}

func keepLast(events []Event, n int) []Event {
	if len(events) > n {
		return append([]Event(nil), events[len(events)-n:]...)
	}
	return events
}

// Activity returns a project's pushes after seq, the latest sequence number
// to ask after next time, and the bridge's epoch. Sequence numbers count per
// user and project, and restart with the epoch: a cursor from another epoch
// is from before a restart, so everything since the restart is returned.
func (b *Bridge) Activity(userID, projectID, after int64, epoch string) ([]Event, int64, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if epoch != "" && epoch != b.epoch {
		after = 0
	}
	out := []Event{}
	pe := b.events[eventKey{userID, projectID}]
	if pe == nil {
		return out, 0, b.epoch
	}
	for _, list := range [][]Event{pe.published, pe.refused} {
		for _, e := range list {
			if e.Seq > after {
				out = append(out, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, pe.seq, b.epoch
}

// logf logs a line about a workspace, at most a few a minute per workspace,
// with text from the workspace made safe to log.
func (b *Bridge) logf(wsID int64, format string, args ...any) {
	b.mu.Lock()
	rl := b.logs[wsID]
	if rl == nil {
		rl = &rateLog{bucket: newBucket(logBurst, logsPerMinute, b.now())}
		b.logs[wsID] = rl
	}
	ok, suppressed := rl.allow(b.now())
	b.mu.Unlock()
	if !ok {
		return
	}
	line := clean(fmt.Sprintf(format, args...))
	if suppressed > 0 {
		log.Printf("git bridge: workspace %d: %s (%d earlier lines suppressed)", wsID, line, suppressed)
		return
	}
	log.Printf("git bridge: workspace %d: %s", wsID, line)
}

type rateLog struct {
	bucket     *bucket
	suppressed int
}

func (r *rateLog) allow(now time.Time) (bool, int) {
	if !r.bucket.take(now, 1) {
		r.suppressed++
		return false, 0
	}
	n := r.suppressed
	r.suppressed = 0
	return true, n
}

// clean makes text that may come from a workspace safe for one log line or
// one notice: control, format and separator characters become spaces, and
// it is cut to a bounded length.
func clean(s string) string {
	var sb strings.Builder
	n := 0
	for _, r := range s {
		if n >= maxLogText {
			sb.WriteString("…")
			break
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			r = ' '
		}
		sb.WriteRune(r)
		n++
	}
	return sb.String()
}

// bucket is a token bucket: up to capacity tokens, refilled at a steady
// rate.
type bucket struct {
	capacity  float64
	perSecond float64
	tokens    float64
	last      time.Time
}

func newBucket(capacity, perMinute int, now time.Time) *bucket {
	return &bucket{capacity: float64(capacity), perSecond: float64(perMinute) / 60, tokens: float64(capacity), last: now}
}

func (k *bucket) refill(now time.Time) {
	if now.After(k.last) {
		k.tokens = min(k.capacity, k.tokens+now.Sub(k.last).Seconds()*k.perSecond)
		k.last = now
	}
}

func (k *bucket) take(now time.Time, n int) bool {
	k.refill(now)
	if k.tokens < float64(n) {
		return false
	}
	k.tokens -= float64(n)
	return true
}

// wait is how long until a token is available.
func (k *bucket) wait(now time.Time) time.Duration {
	k.refill(now)
	if k.tokens >= 1 {
		return 0
	}
	return time.Duration((1 - k.tokens) / k.perSecond * float64(time.Second))
}
