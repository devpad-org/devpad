package workspace

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/agentbin"
	"github.com/devpad-org/devpad/internal/container"
)

// containerIP resolves a workspace container's IP on its private network,
// making sure devpad can actually route to that network first. Attaching is a
// no-op when devpad runs on the host and cached when it runs in a container.
func (s *service) containerIP(ctx context.Context, ws *Workspace) (string, error) {
	if err := s.container.EnsureSelfAttached(ctx, ws.NetworkName); err != nil {
		return "", fmt.Errorf("joining workspace network: %w", err)
	}
	ip, err := s.container.GetIP(ctx, ws.ContainerID, ws.NetworkName)
	if err != nil {
		return "", fmt.Errorf("getting container IP: %w", err)
	}
	return ip, nil
}

func (s *service) getAgent(ctx context.Context, userID, workspaceID int64) (*agent.Client, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.ContainerID == "" || ws.Status != StatusRunning {
		return nil, ErrNotRunning
	}
	ip, err := s.containerIP(ctx, ws)
	if err != nil {
		return nil, err
	}
	return agent.NewClient(ip, s.agentPort, ws.AgentToken), nil
}

// waitForAgent waits until the agent inside the workspace container is ready
// to accept requests. It uses a 30-second timeout to avoid hanging forever if
// the container is broken.
func (s *service) waitForAgent(ctx context.Context, ws *Workspace) error {
	ip, err := s.containerIP(ctx, ws)
	if err != nil {
		return err
	}
	c := agent.NewClient(ip, s.agentPort, ws.AgentToken)

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := c.WaitReady(waitCtx); err != nil {
		return fmt.Errorf("waiting for agent in workspace %d: %w", ws.ID, err)
	}
	return nil
}

// ensureAgentUpdated checks whether the in-container agent is running the
// expected version and, if not, copies the embedded binary into the container
// and restarts it. An empty or unreachable version response is treated as
// outdated (the agent predates the /api/version endpoint).
func (s *service) ensureAgentUpdated(ctx context.Context, ws *Workspace) error {
	if ws.ContainerID == "" {
		return nil
	}

	ip, err := s.containerIP(ctx, ws)
	if err != nil {
		return fmt.Errorf("resolving container address for update check: %w", err)
	}

	c := agent.NewClient(ip, s.agentPort, ws.AgentToken)
	remoteVersion, err := c.Version(ctx)
	if err != nil {
		// Agent is unreachable or too old to respond — treat as needing update.
		log.Printf("workspace %d: agent version check failed (%v), updating agent", ws.ID, err)
		remoteVersion = ""
	}

	if remoteVersion == agentbin.Version {
		return nil
	}

	log.Printf("workspace %d: updating agent from %q to %q", ws.ID, remoteVersion, agentbin.Version)

	if err := s.container.CopyFileToContainer(ctx, ws.ContainerID, "usr/local/bin/devpad-agent", agentbin.Binary, 0755); err != nil {
		return fmt.Errorf("copying agent binary to container: %w", err)
	}

	if err := s.container.Restart(ctx, ws.ContainerID); err != nil {
		return fmt.Errorf("restarting container after agent update: %w", err)
	}

	// Wait for the new agent to become ready after the restart.
	if err := s.waitForAgent(ctx, ws); err != nil {
		return fmt.Errorf("agent not ready after update: %w", err)
	}

	log.Printf("workspace %d: agent updated and container restarted", ws.ID)
	return nil
}

func (s *service) AgentAddr(ctx context.Context, userID, workspaceID int64) (string, string, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return "", "", err
	}
	if ws.ContainerID == "" || ws.Status != StatusRunning {
		return "", "", ErrNotRunning
	}
	ip, err := s.containerIP(ctx, ws)
	if err != nil {
		return "", "", err
	}
	return fmt.Sprintf("%s:%d", ip, s.agentPort), ws.AgentToken, nil
}

// WorkspaceInfo holds combined info about a workspace's agent and container.
type WorkspaceInfo struct {
	AgentVersion         string                    `json:"agentVersion"`
	ExpectedAgentVersion string                    `json:"expectedAgentVersion"`
	Stats                *container.ContainerStats `json:"stats"`
}

func (s *service) Info(ctx context.Context, userID, workspaceID int64) (*WorkspaceInfo, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.ContainerID == "" || ws.Status != StatusRunning {
		return nil, ErrNotRunning
	}

	info := &WorkspaceInfo{
		ExpectedAgentVersion: agentbin.Version,
	}

	// Fetch agent version
	ip, err := s.containerIP(ctx, ws)
	if err == nil {
		c := agent.NewClient(ip, s.agentPort, ws.AgentToken)
		if v, verr := c.Version(ctx); verr == nil {
			info.AgentVersion = v
		}
	}

	// Fetch container stats
	stats, err := s.container.Stats(ctx, ws.ContainerID)
	if err == nil {
		info.Stats = stats
	}

	return info, nil
}
