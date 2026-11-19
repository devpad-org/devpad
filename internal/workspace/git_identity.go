package workspace

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
)

const (
	// gitIdentitySyncTimeout bounds each workspace's update, so an agent
	// that doesn't answer delays neither a start nor a save for long.
	gitIdentitySyncTimeout = 5 * time.Second
	// gitIdentitySyncParallel is how many workspaces a save updates at once.
	gitIdentitySyncParallel = 8
)

// holdsDevpadRepo reports whether the repo at /workspace is one devpad seeded,
// and so one whose commit identity devpad keeps. A standalone workspace's repo
// is the user's own, identity included.
func (ws *Workspace) holdsDevpadRepo() bool {
	return ws.ProjectID != 0 && ws.RepoSeeded
}

// SyncGitIdentity applies the user's git identity to each of their running
// project workspaces and sandboxes.
func (s *service) SyncGitIdentity(ctx context.Context, userID int64) (updated int, complete bool) {
	workspaces, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		log.Printf("user %d: listing workspaces to update the git identity: %v", userID, err)
		return 0, false
	}
	var done, failed atomic.Int64
	var wg sync.WaitGroup
	slots := make(chan struct{}, gitIdentitySyncParallel)
	for _, ws := range workspaces {
		if !ws.holdsDevpadRepo() || ws.Status != StatusRunning || ws.ContainerID == "" {
			continue
		}
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			if err := s.syncGitIdentity(ctx, ws); err != nil {
				failed.Add(1)
				log.Printf("workspace %d: %v", ws.ID, err)
				return
			}
			done.Add(1)
		})
	}
	wg.Wait()
	return int(done.Load()), failed.Load() == 0
}

// syncGitIdentity writes the owner's git identity into the workspace's repo,
// replacing what is there, once the owner has saved one. Until then the repo
// keeps what it was seeded with and anything set in it since. The identity is
// read under the lock, so the last write is always the newest identity.
func (s *service) syncGitIdentity(ctx context.Context, ws *Workspace) error {
	if !ws.holdsDevpadRepo() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gitIdentitySyncTimeout)
	defer cancel()

	unlock, err := s.lockAgentSync(ctx, ws.ID)
	if err != nil {
		return err
	}
	defer unlock()

	user, err := s.users.GetByID(ctx, ws.UserID)
	if err != nil {
		return fmt.Errorf("looking up the git identity: %w", err)
	}
	if user == nil || !user.GitIdentitySaved {
		return nil
	}
	name, email := user.GitIdentity()
	ip, err := s.containerIP(ctx, ws)
	if err != nil {
		return err
	}
	result, err := agent.NewClient(ip, s.agentPort, ws.AgentToken).GitAction(ctx, agent.GitActionRequest{
		Action:    "set-config",
		UserName:  name,
		UserEmail: email,
	})
	// What the agent answers is untrusted and these errors are logged, so
	// its text is capped and quoted to keep it to one short line.
	if err != nil {
		return fmt.Errorf("setting the git identity: %q", cleanReason(err.Error()))
	}
	if !result.Success {
		return fmt.Errorf("setting the git identity: %q", cleanReason(result.Error))
	}
	return nil
}

// logSyncGitIdentity syncs a workspace's git identity, logging a failure: the
// workspace still works, committing as it did before.
func (s *service) logSyncGitIdentity(ctx context.Context, ws *Workspace) {
	if err := s.syncGitIdentity(ctx, ws); err != nil {
		log.Printf("workspace %d: %v", ws.ID, err)
	}
}
