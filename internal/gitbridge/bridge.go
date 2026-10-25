// Package gitbridge answers git-remote-devpad, the git remote helper in a
// project's workspace. The workspace cannot reach devpad, so devpad polls
// each running project workspace's agent for the requests the helper queued,
// does the work through the project service, and posts the results back.
//
// A push is only ever a request: devpad pulls the branch by bundle and
// publishes it exactly as the Publish button does, so the default branch,
// branches owned by sandboxes and non-fast-forward updates are refused there.
// The IDE assistant runs in the same workspace and can push the same way;
// that gives it nothing the Publish button does not.
package gitbridge

import (
	"context"
	"errors"
	"log"
	"regexp"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Workspaces finds project workspaces and carries requests to and from
// their agents.
type Workspaces interface {
	ListAll(ctx context.Context) ([]*workspace.Workspace, error)
	workspace.GitBridgeService
}

// Projects does the work a request asks for, under the project's rules.
type Projects interface {
	Branches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error)
	FetchWorkspaceBranches(ctx context.Context, userID, projectID int64, branches []string) error
	PublishWorkspaceBranch(ctx context.Context, userID, projectID int64, branch string) (*agent.RepoBranch, error)
}

const (
	maxBranches = 256
	// maxConcurrent bounds the requests answered at once per workspace.
	maxConcurrent   = 4
	maxEvents       = 50
	listTimeout     = time.Minute
	transferTimeout = 20 * time.Minute
	// configureEvery bounds retries of setting up the devpad remote.
	configureEvery = time.Minute
)

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

// Bridge polls every running project workspace.
type Bridge struct {
	workspaces Workspaces
	projects   Projects

	scanEvery  time.Duration
	pollWait   time.Duration
	backoffMin time.Duration
	backoffMax time.Duration
	now        func() time.Time

	mu      sync.Mutex
	pollers map[int64]*poller
	events  map[eventKey][]Event
	seq     int64
	wg      sync.WaitGroup
}

type poller struct {
	containerID string
	cancel      context.CancelFunc
}

type eventKey struct{ userID, projectID int64 }

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
		backoffMin: time.Second,
		backoffMax: time.Minute,
		now:        time.Now,
		pollers:    make(map[int64]*poller),
		events:     make(map[eventKey][]Event),
	}
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
// standalone workspaces are never polled.
func (b *Bridge) reconcile(ctx context.Context) {
	all, err := b.workspaces.ListAll(ctx)
	if err != nil {
		log.Printf("git bridge: listing workspaces: %v", err)
		return
	}
	want := make(map[int64]*workspace.Workspace)
	for _, ws := range all {
		if ws.IsProjectWorkspace() && ws.Status == workspace.StatusRunning && ws.ContainerID != "" && ws.RepoSeeded {
			want[ws.ID] = ws
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
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

func (b *Bridge) poll(ctx context.Context, ws *workspace.Workspace) {
	slots := make(chan struct{}, maxConcurrent)
	backoff := b.backoffMin
	var configuredAt time.Time
	configured := false
	for ctx.Err() == nil {
		if !configured && b.now().Sub(configuredAt) >= configureEvery {
			configuredAt = b.now()
			err := b.workspaces.ConfigureGitBridge(ctx, ws.UserID, ws.ID)
			var agentErr *agent.AgentError
			switch {
			case err == nil:
				configured = true
			case errors.As(err, &agentErr) && agentErr.StatusCode == 409:
				// Another remote has the name, or the repo is missing; the
				// helper is only reached through our remote, so keep polling.
				log.Printf("git bridge: workspace %d: %s", ws.ID, agentErr.Message)
				configured = true
			case ctx.Err() == nil:
				log.Printf("git bridge: workspace %d: setting up the devpad remote: %v", ws.ID, err)
			}
		}

		reqs, err := b.workspaces.GitBridgeRequests(ctx, ws.UserID, ws.ID, b.pollWait)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var agentErr *agent.AgentError
			if errors.As(err, &agentErr) && agentErr.StatusCode == 404 {
				// The agent predates the bridge; it is replaced on restart.
				backoff = b.backoffMax
			}
			// The agent may have restarted; set the remote up again.
			configured = false
			b.sleep(ctx, backoff)
			backoff = min(backoff*2, b.backoffMax)
			continue
		}
		backoff = b.backoffMin
		for _, req := range reqs {
			if !requestIDPattern.MatchString(req.ID) {
				continue
			}
			// Requests run alongside each other and the next poll, so a long
			// push does not leave a fetch waiting to be picked up.
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			b.wg.Add(1)
			go func(req agent.GitBridgeRequest) {
				defer b.wg.Done()
				defer func() { <-slots }()
				b.answer(ctx, ws, req)
			}(req)
		}
	}
}

func (b *Bridge) answer(ctx context.Context, ws *workspace.Workspace, req agent.GitBridgeRequest) {
	result := b.handle(ctx, ws, req)
	if err := b.workspaces.GitBridgeRespond(ctx, ws.UserID, ws.ID, req.ID, result); err != nil && ctx.Err() == nil {
		var agentErr *agent.AgentError
		if !errors.As(err, &agentErr) || agentErr.StatusCode != 410 {
			log.Printf("git bridge: workspace %d: answering a request: %v", ws.ID, err)
		}
	}
}

func (b *Bridge) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// handle does what a request asks under the project's rules. Everything in
// the request comes from the workspace; the project and user come from
// devpad's record of the workspace.
func (b *Bridge) handle(ctx context.Context, ws *workspace.Workspace, req agent.GitBridgeRequest) agent.GitBridgeResult {
	switch req.Op {
	case "list":
		ctx, cancel := context.WithTimeout(ctx, listTimeout)
		defer cancel()
		refs, err := b.projects.Branches(ctx, ws.UserID, ws.ProjectID)
		if err != nil {
			return agent.GitBridgeResult{Error: message(ws, err)}
		}
		return agent.GitBridgeResult{DefaultBranch: refs.DefaultBranch, Refs: refs.Branches}

	case "fetch":
		if len(req.Branches) == 0 || len(req.Branches) > maxBranches {
			return agent.GitBridgeResult{Error: "invalid branch list"}
		}
		ctx, cancel := context.WithTimeout(ctx, transferTimeout)
		defer cancel()
		if err := b.projects.FetchWorkspaceBranches(ctx, ws.UserID, ws.ProjectID, req.Branches); err != nil {
			return agent.GitBridgeResult{Error: message(ws, err)}
		}
		return agent.GitBridgeResult{}

	case "push":
		if len(req.Branches) != 1 {
			return agent.GitBridgeResult{Error: "push one branch at a time"}
		}
		branch := req.Branches[0]
		if req.Force {
			msg := "devpad refuses force pushes; it only fast-forwards"
			b.record(ws, Event{Branch: branch, Error: msg})
			return agent.GitBridgeResult{Error: msg}
		}
		ctx, cancel := context.WithTimeout(ctx, transferTimeout)
		defer cancel()
		published, err := b.projects.PublishWorkspaceBranch(ctx, ws.UserID, ws.ProjectID, branch)
		if err != nil {
			msg := message(ws, err)
			b.record(ws, Event{Branch: branch, Error: msg})
			return agent.GitBridgeResult{Error: msg}
		}
		b.record(ws, Event{Branch: branch, OK: true, SHA: published.SHA})
		return agent.GitBridgeResult{SHA: published.SHA}
	}
	return agent.GitBridgeResult{Error: "unsupported request"}
}

// message turns an error into text for git to show. Errors devpad expects
// are explained; anything else is logged and summarized, so internal detail
// does not reach the workspace.
func message(ws *workspace.Workspace, err error) string {
	var agentErr *agent.AgentError
	switch {
	case errors.Is(err, project.ErrPublishDefault):
		return "the default branch only changes through a merge request"
	case errors.Is(err, project.ErrBranchInUse):
		return "an agent's sandbox owns this branch"
	case errors.Is(err, project.ErrBranchPublishing):
		return "this branch is already being published"
	case errors.Is(err, project.ErrInvalidInput):
		return err.Error()
	case errors.Is(err, project.ErrNotReady), errors.Is(err, project.ErrDeleting), errors.Is(err, workspace.ErrNotRunning):
		return err.Error()
	case errors.As(err, &agentErr) && agentErr.StatusCode >= 400 && agentErr.StatusCode < 500:
		return agentErr.Message
	}
	log.Printf("git bridge: workspace %d: %v", ws.ID, err)
	return "devpad could not complete the request"
}

func (b *Bridge) record(ws *workspace.Workspace, e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	e.Seq = b.seq
	e.At = b.now()
	key := eventKey{ws.UserID, ws.ProjectID}
	events := append(b.events[key], e)
	if len(events) > maxEvents {
		events = events[len(events)-maxEvents:]
	}
	b.events[key] = events
}

// Activity returns a project's pushes after seq, and the latest sequence
// number to ask after next time.
func (b *Bridge) Activity(userID, projectID, after int64) ([]Event, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Event{}
	for _, e := range b.events[eventKey{userID, projectID}] {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, b.seq
}
