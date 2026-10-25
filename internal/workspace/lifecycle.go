package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
)

func (s *service) Create(ctx context.Context, userID int64, name, description string) (*Workspace, error) {
	return s.create(ctx, &Workspace{
		UserID:      userID,
		Name:        name,
		Description: description,
		Kind:        KindInteractive,
	})
}

// CreateSandbox creates a workspace that belongs to a project and owns one of
// its branches. The repo is seeded separately via SeedSandboxRepo.
func (s *service) CreateSandbox(ctx context.Context, userID int64, spec SandboxSpec) (*Workspace, error) {
	if spec.ProjectID == 0 {
		return nil, fmt.Errorf("sandbox requires a project")
	}
	if spec.Kind == KindInteractive || !spec.Kind.Valid() {
		return nil, fmt.Errorf("invalid sandbox kind %q", spec.Kind)
	}
	if spec.Branch == "" {
		return nil, fmt.Errorf("sandbox requires a branch")
	}
	return s.create(ctx, &Workspace{
		UserID:      userID,
		Name:        spec.Name,
		Description: spec.Description,
		ProjectID:   spec.ProjectID,
		Kind:        spec.Kind,
		Branch:      spec.Branch,
	})
}

// CreateProjectWorkspace creates a project's own interactive workspace. It
// owns no branch; its repo is seeded separately via SeedSandboxRepo.
func (s *service) CreateProjectWorkspace(ctx context.Context, userID int64, spec ProjectWorkspaceSpec) (*Workspace, error) {
	if spec.ProjectID == 0 {
		return nil, fmt.Errorf("project workspace requires a project")
	}
	return s.create(ctx, &Workspace{
		UserID:      userID,
		Name:        spec.Name,
		Description: spec.Description,
		ProjectID:   spec.ProjectID,
		Kind:        KindInteractive,
	})
}

func (s *service) create(ctx context.Context, ws *Workspace) (*Workspace, error) {
	// Generate a cryptographically random token for agent authentication.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generating agent token: %w", err)
	}
	agentToken := hex.EncodeToString(tokenBytes)

	// Insert the workspace record first to obtain a stable ID for naming
	// Docker resources. This decouples container/volume names from the
	// user-facing workspace name (which may contain spaces or be renamed).
	ws.Status = StatusCreating
	ws.AgentToken = agentToken
	ws.MemoryLimit = DefaultMemoryLimit
	ws.NanoCPUs = DefaultNanoCPUs
	if err := s.repo.Create(ctx, ws); err != nil {
		return nil, fmt.Errorf("creating workspace: %w", err)
	}

	// Use the workspace ID for Docker resource names — stable and unique.
	networkName := fmt.Sprintf("devpad-net-%d", ws.ID)
	if err := s.container.CreateNetwork(ctx, networkName); err != nil {
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("creating network: %w", err)
	}

	// When devpad runs in a container it must join the workspace network to
	// reach the agent; on the host this is a no-op.
	if err := s.container.EnsureSelfAttached(ctx, networkName); err != nil {
		_ = s.container.RemoveNetwork(ctx, networkName)
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("joining workspace network: %w", err)
	}

	volumeName := fmt.Sprintf("devpad-vol-%d", ws.ID)
	if err := s.container.CreateVolume(ctx, volumeName); err != nil {
		_ = s.container.RemoveNetwork(ctx, networkName)
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("creating volume: %w", err)
	}

	containerEnv := []string{
		fmt.Sprintf("AGENT_AUTH_TOKEN=%s", agentToken),
	}

	// Inject sidecar service env vars (connection info) if any
	// services are already attached.
	if s.sidecar != nil {
		svcEnv, err := s.sidecar.EnvVars(ctx, ws.ID, networkName)
		if err != nil {
			log.Printf("workspace %d: failed to get sidecar env vars: %v", ws.ID, err)
		} else {
			containerEnv = append(containerEnv, svcEnv...)
		}
	}

	containerID, err := s.container.Create(ctx, fmt.Sprintf("%d", ws.ID), volumeName, networkName, containerEnv, ws.MemoryLimit, ws.NanoCPUs)
	if err != nil {
		_ = s.container.RemoveVolume(ctx, volumeName)
		_ = s.container.RemoveNetwork(ctx, networkName)
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("creating container: %w", err)
	}

	if err := s.container.Start(ctx, containerID); err != nil {
		_ = s.container.Remove(ctx, containerID)
		_ = s.container.RemoveVolume(ctx, volumeName)
		_ = s.container.RemoveNetwork(ctx, networkName)
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("starting container: %w", err)
	}

	ws.Status = StatusRunning
	ws.ContainerID = containerID
	ws.VolumeName = volumeName
	ws.NetworkName = networkName
	if err := s.repo.Update(ctx, ws); err != nil {
		_ = s.container.Stop(ctx, containerID)
		_ = s.container.Remove(ctx, containerID)
		_ = s.container.RemoveVolume(ctx, volumeName)
		_ = s.container.RemoveNetwork(ctx, networkName)
		_ = s.repo.Delete(ctx, ws.ID)
		return nil, fmt.Errorf("updating workspace: %w", err)
	}

	// Wait for the agent to be ready before performing post-start actions.
	if err := s.waitForAgent(ctx, ws); err != nil {
		log.Printf("workspace %d: agent not ready after create: %v", ws.ID, err)
	}

	s.injectUserSSHKeys(ctx, ws)

	return ws, nil
}

func (s *service) Delete(ctx context.Context, userID, workspaceID int64) error {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	if s.isConverting(ws.ID) {
		return ErrConverting
	}

	// Remove the Docker container
	if ws.ContainerID != "" {
		_ = s.container.Stop(ctx, ws.ContainerID)
		if err := s.container.Remove(ctx, ws.ContainerID); err != nil {
			return fmt.Errorf("removing container: %w", err)
		}
	}

	// Delete all attached sidecar services and their resources.
	if s.sidecar != nil {
		if err := s.sidecar.DeleteAll(ctx, ws.ID); err != nil {
			log.Printf("workspace %d: sidecar delete failed: %v", ws.ID, err)
		}
	}

	// Remove the associated volume
	if ws.VolumeName != "" {
		if err := s.container.RemoveVolume(ctx, ws.VolumeName); err != nil {
			return fmt.Errorf("removing volume: %w", err)
		}
	}

	// Remove the workspace network. Docker refuses to remove a network that
	// still has endpoints, so drop devpad's own attachment first.
	if ws.NetworkName != "" {
		if err := s.container.DetachSelf(ctx, ws.NetworkName); err != nil {
			log.Printf("workspace %d: detaching devpad from network failed: %v", ws.ID, err)
		}
		if err := s.container.RemoveNetwork(ctx, ws.NetworkName); err != nil {
			return fmt.Errorf("removing network: %w", err)
		}
	}

	if err := s.repo.Delete(ctx, ws.ID); err != nil {
		return fmt.Errorf("deleting workspace: %w", err)
	}
	return nil
}

func (s *service) Start(ctx context.Context, userID, workspaceID int64) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if s.isConverting(ws.ID) {
		return nil, ErrConverting
	}
	if ws.Status == StatusRunning {
		return ws, nil
	}
	if ws.ContainerID == "" {
		return nil, fmt.Errorf("workspace has no container")
	}

	// Detect legacy containers missing the AGENT_AUTH_TOKEN env var.
	// These were created before agent auth was introduced and must be
	// recreated so the token is baked into the container config.
	if err := s.ensureContainerHasToken(ctx, ws); err != nil {
		return nil, fmt.Errorf("migrating container config: %w", err)
	}

	// Backfill network for legacy workspaces created before network isolation.
	if err := s.ensureNetwork(ctx, ws); err != nil {
		return nil, fmt.Errorf("backfilling network: %w", err)
	}

	if err := s.container.Start(ctx, ws.ContainerID); err != nil {
		return nil, fmt.Errorf("starting container: %w", err)
	}
	ws.Status = StatusRunning
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("updating workspace status: %w", err)
	}

	// Wait for the agent to be ready before performing post-start actions.
	if err := s.waitForAgent(ctx, ws); err != nil {
		log.Printf("workspace %d: agent not ready after start: %v", ws.ID, err)
	}

	// Check and update the in-container agent if needed. This is best-effort:
	// a failure to update should not prevent the workspace from starting.
	if err := s.ensureAgentUpdated(ctx, ws); err != nil {
		log.Printf("workspace %d: agent update failed: %v", ws.ID, err)
	}

	s.injectUserSSHKeys(ctx, ws)

	// Start attached sidecar services. Best-effort.
	if s.sidecar != nil {
		if err := s.sidecar.StartAll(ctx, ws.ID, ws.NetworkName); err != nil {
			log.Printf("workspace %d: sidecar start failed: %v", ws.ID, err)
		}
	}

	return ws, nil
}

func (s *service) Stop(ctx context.Context, userID, workspaceID int64) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	// Stopping mid-conversion would leave devpad unable to undo its work.
	if s.isConverting(ws.ID) {
		return nil, ErrConverting
	}
	if ws.Status == StatusStopped {
		return ws, nil
	}
	if ws.ContainerID == "" {
		return nil, fmt.Errorf("workspace has no container")
	}
	if err := s.container.Stop(ctx, ws.ContainerID); err != nil {
		return nil, fmt.Errorf("stopping container: %w", err)
	}

	// Stop attached sidecar services. Best-effort.
	if s.sidecar != nil {
		if err := s.sidecar.StopAll(ctx, ws.ID); err != nil {
			log.Printf("workspace %d: sidecar stop failed: %v", ws.ID, err)
		}
	}

	ws.Status = StatusStopped
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("updating workspace status: %w", err)
	}
	return ws, nil
}

// injectUserSSHKeys injects the user's SSH keys if they have them configured.
// Sandboxes and project workspaces never get them, so an agent cannot push
// anywhere on the user's behalf. Best-effort.
func (s *service) injectUserSSHKeys(ctx context.Context, ws *Workspace) {
	if !ws.ReceivesUserCredentials() {
		return
	}
	if err := s.injectSSHKeys(ctx, ws); err != nil {
		log.Printf("workspace %d: SSH key injection failed: %v", ws.ID, err)
	}
}
