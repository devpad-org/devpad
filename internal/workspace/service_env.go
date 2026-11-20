package workspace

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
)

// A container's environment is fixed when it is made, so a service added
// to a running workspace reaches its processes through the agent instead:
// devpad sends it the variables of every service the workspace has, and it
// gives them to each terminal and command it starts. The agent keeps them
// across restarts of the container; a rebuilt container has every service
// in its own environment.

// serviceVarsEnv names, comma-separated, the variables of services in a
// container's environment, so the agent can tell them from the image's
// own: once devpad has sent it the services' variables, it drops those of
// services since removed (cmd/agent/service_env.go).
const serviceVarsEnv = "DEVPAD_SERVICE_VARS"

// envNames returns the names of NAME=value variables.
func envNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	return names
}

// lockAgentSync takes the lock that serialises what devpad syncs to a
// workspace's agent (its services' variables and the git identity), so an
// older value never lands after a newer one. An agent can stall whoever holds
// it, so waiting for it ends with ctx.
func (s *service) lockAgentSync(ctx context.Context, workspaceID int64) (unlock func(), err error) {
	s.agentSyncMu.Lock()
	if s.agentSyncLocks == nil {
		s.agentSyncLocks = make(map[int64]chan struct{})
	}
	lock, ok := s.agentSyncLocks[workspaceID]
	if !ok {
		lock = make(chan struct{}, 1)
		s.agentSyncLocks[workspaceID] = lock
	}
	s.agentSyncMu.Unlock()

	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for another sync to workspace %d: %w", workspaceID, ctx.Err())
	}
}

// forgetAgentSyncLock drops a deleted workspace's sync lock.
func (s *service) forgetAgentSyncLock(workspaceID int64) {
	s.agentSyncMu.Lock()
	defer s.agentSyncMu.Unlock()
	delete(s.agentSyncLocks, workspaceID)
}

// SyncServiceEnv sends a running workspace's agent the variables of the
// services it has now. A stopped workspace is left alone: it gets them
// when it starts.
func (s *service) SyncServiceEnv(ctx context.Context, userID, workspaceID int64) error {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	if ws.Status != StatusRunning || ws.ContainerID == "" {
		return nil
	}
	return s.syncServiceEnv(ctx, ws)
}

func (s *service) syncServiceEnv(ctx context.Context, ws *Workspace) error {
	if s.sidecar == nil {
		return nil
	}
	unlock, err := s.lockAgentSync(ctx, ws.ID)
	if err != nil {
		return err
	}
	defer unlock()

	env, err := s.sidecar.EnvVars(ctx, ws.ID, ws.NetworkName)
	if err != nil {
		return fmt.Errorf("listing service variables: %w", err)
	}
	ip, err := s.containerIP(ctx, ws)
	if err != nil {
		return err
	}
	if err := agent.NewClient(ip, s.agentPort, ws.AgentToken).SetServiceEnv(ctx, env); err != nil {
		return fmt.Errorf("sending service variables to workspace %d: %w", ws.ID, err)
	}
	return nil
}

// logSyncServiceEnv syncs a workspace's service variables, logging a
// failure: the workspace still works, without the services' variables.
func (s *service) logSyncServiceEnv(ctx context.Context, ws *Workspace) {
	if err := s.syncServiceEnv(ctx, ws); err != nil {
		log.Printf("workspace %d: %v", ws.ID, err)
	}
}
