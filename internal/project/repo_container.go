package project

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/agentbin"
)

// Repo containers only hold a bare repo and serve git bundles, so they get
// much smaller limits than workspaces.
const (
	repoMemoryLimit int64 = 1024 * 1024 * 1024 // 1 GB
	repoNanoCPUs    int64 = 1_000_000_000      // 1 core

	repoAgentReadyTimeout = 30 * time.Second
)

// The repo container uses its own network, which only devpad joins, so no
// sandbox can route to it. The container manager prefixes the name with
// "devpad-ws-"; the "repo-" part keeps it apart from numeric workspace IDs.
func repoContainerName(projectID int64) string { return fmt.Sprintf("repo-%d", projectID) }
func repoNetworkName(projectID int64) string   { return fmt.Sprintf("devpad-repo-net-%d", projectID) }
func repoVolumeName(projectID int64) string    { return fmt.Sprintf("devpad-repo-vol-%d", projectID) }

// provisionRepoContainer creates and starts the project's repo container,
// rolling back whatever it created if a step fails.
func (s *service) provisionRepoContainer(ctx context.Context, p *Project) error {
	network := repoNetworkName(p.ID)
	if err := s.container.CreateNetwork(ctx, network); err != nil {
		return fmt.Errorf("creating repo network: %w", err)
	}
	if err := s.container.EnsureSelfAttached(ctx, network); err != nil {
		_ = s.container.RemoveNetwork(ctx, network)
		return fmt.Errorf("joining repo network: %w", err)
	}
	volume := repoVolumeName(p.ID)
	if err := s.container.CreateVolume(ctx, volume); err != nil {
		s.removeRepoNetwork(ctx, network)
		return fmt.Errorf("creating repo volume: %w", err)
	}

	env := []string{"AGENT_AUTH_TOKEN=" + p.RepoAgentToken}
	containerID, err := s.container.Create(ctx, repoContainerName(p.ID), volume, network, env, repoMemoryLimit, repoNanoCPUs)
	if err != nil {
		_ = s.container.RemoveVolume(ctx, volume)
		s.removeRepoNetwork(ctx, network)
		return fmt.Errorf("creating repo container: %w", err)
	}
	if err := s.container.Start(ctx, containerID); err != nil {
		_ = s.container.Remove(ctx, containerID)
		_ = s.container.RemoveVolume(ctx, volume)
		s.removeRepoNetwork(ctx, network)
		return fmt.Errorf("starting repo container: %w", err)
	}

	p.RepoContainerID = containerID
	p.RepoVolumeName = volume
	p.RepoNetworkName = network
	return nil
}

// removeRepoContainer removes the repo container, its volume, and its
// network. Missing resources are skipped.
func (s *service) removeRepoContainer(ctx context.Context, p *Project) error {
	if p.RepoContainerID != "" {
		_ = s.container.Stop(ctx, p.RepoContainerID)
		if err := s.container.Remove(ctx, p.RepoContainerID); err != nil {
			return fmt.Errorf("removing repo container: %w", err)
		}
	}
	if p.RepoVolumeName != "" {
		if err := s.container.RemoveVolume(ctx, p.RepoVolumeName); err != nil {
			return fmt.Errorf("removing repo volume: %w", err)
		}
	}
	if p.RepoNetworkName != "" {
		s.removeRepoNetwork(ctx, p.RepoNetworkName)
	}
	return nil
}

func (s *service) removeRepoNetwork(ctx context.Context, network string) {
	if err := s.container.DetachSelf(ctx, network); err != nil {
		log.Printf("detaching devpad from repo network %s failed: %v", network, err)
	}
	if err := s.container.RemoveNetwork(ctx, network); err != nil {
		log.Printf("removing repo network %s failed: %v", network, err)
	}
}

// repoAgent returns a client for the project's repo container, starting the
// container if it is down and updating its agent if it is outdated.
func (s *service) repoAgent(ctx context.Context, p *Project) (*agent.Client, error) {
	if p.RepoContainerID == "" {
		return nil, ErrNotReady
	}
	c, err := s.repoClient(ctx, p)
	if err != nil {
		return nil, err
	}
	if c.Healthz(ctx) != nil {
		if err := s.container.Start(ctx, p.RepoContainerID); err != nil {
			log.Printf("project %d: starting repo container: %v", p.ID, err)
		}
		if c, err = s.waitForRepoAgent(ctx, p); err != nil {
			return nil, err
		}
	}

	version, err := c.Version(ctx)
	if err != nil || version != agentbin.Version {
		log.Printf("project %d: updating repo agent from %q to %q", p.ID, version, agentbin.Version)
		if err := s.container.CopyFileToContainer(ctx, p.RepoContainerID, "usr/local/bin/devpad-agent", agentbin.Binary, 0755); err != nil {
			return nil, fmt.Errorf("copying agent binary to repo container: %w", err)
		}
		if err := s.container.Restart(ctx, p.RepoContainerID); err != nil {
			return nil, fmt.Errorf("restarting repo container: %w", err)
		}
		if c, err = s.waitForRepoAgent(ctx, p); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// repoClient resolves the repo container's address; the IP can change when
// the container restarts.
func (s *service) repoClient(ctx context.Context, p *Project) (*agent.Client, error) {
	if err := s.container.EnsureSelfAttached(ctx, p.RepoNetworkName); err != nil {
		return nil, fmt.Errorf("joining repo network: %w", err)
	}
	ip, err := s.container.GetIP(ctx, p.RepoContainerID, p.RepoNetworkName)
	if err != nil {
		return nil, fmt.Errorf("getting repo container IP: %w", err)
	}
	return agent.NewClient(ip, s.agentPort, p.RepoAgentToken), nil
}

func (s *service) waitForRepoAgent(ctx context.Context, p *Project) (*agent.Client, error) {
	c, err := s.repoClient(ctx, p)
	if err != nil {
		return nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, repoAgentReadyTimeout)
	defer cancel()
	if err := c.WaitReady(waitCtx); err != nil {
		return nil, fmt.Errorf("waiting for repo agent in project %d: %w", p.ID, err)
	}
	return c, nil
}
