package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"

	"github.com/devpad-org/devpad/internal/container"
)

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
		Image:       spec.Image,
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
		Image:       spec.Image,
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
	// Undoing a failed create goes on when ctx is cancelled, which is what
	// often failed it, so nothing is left behind.
	cleanupCtx := context.WithoutCancel(ctx)

	// Use the workspace ID for Docker resource names — stable and unique.
	networkName := fmt.Sprintf("devpad-net-%d", ws.ID)
	if err := s.container.CreateNetwork(ctx, networkName); err != nil {
		_ = s.repo.Delete(cleanupCtx, ws.ID)
		return nil, fmt.Errorf("creating network: %w", err)
	}

	// When devpad runs in a container it must join the workspace network to
	// reach the agent; on the host this is a no-op.
	if err := s.container.EnsureSelfAttached(ctx, networkName); err != nil {
		_ = s.container.RemoveNetwork(cleanupCtx, networkName)
		_ = s.repo.Delete(cleanupCtx, ws.ID)
		return nil, fmt.Errorf("joining workspace network: %w", err)
	}

	volumeName := fmt.Sprintf("devpad-vol-%d", ws.ID)
	if err := s.container.CreateVolume(ctx, volumeName); err != nil {
		_ = s.container.RemoveNetwork(cleanupCtx, networkName)
		_ = s.repo.Delete(cleanupCtx, ws.ID)
		return nil, fmt.Errorf("creating volume: %w", err)
	}

	// undo removes what was made so far: the services, then the volume,
	// network and record.
	undo := func() {
		if s.sidecar != nil {
			if err := s.sidecar.DeleteAll(cleanupCtx, ws.ID); err != nil {
				log.Printf("workspace %d: removing services after a failed create: %v", ws.ID, err)
			}
		}
		_ = s.container.RemoveVolume(cleanupCtx, volumeName)
		_ = s.container.RemoveNetwork(cleanupCtx, networkName)
		_ = s.repo.Delete(cleanupCtx, ws.ID)
	}

	// The project's services come first, so the container is made knowing
	// how to reach them. A sandbox without them would leave its agent
	// working against services that are not there, so it is not made; the
	// project workspace is, and can get them by being rebuilt. They are
	// started, not waited for: the project waits for them to be ready
	// before the sandbox's setup script, outside the locks its callers
	// hold while it is made.
	if ws.ProjectID != 0 && s.sidecar != nil {
		if err := s.sidecar.Provision(ctx, ws.ID, ws.ProjectID, networkName, true); err != nil {
			if ws.IsSandbox() {
				undo()
				return nil, fmt.Errorf("starting the project's services: %w", err)
			}
			log.Printf("workspace %d: starting the project's services: %v", ws.ID, err)
		}
	}

	containerEnv := s.containerEnv(ctx, ws.ID, agentToken, networkName)
	containerID, err := s.container.Create(ctx, container.CreateOptions{
		Name:        fmt.Sprintf("%d", ws.ID),
		Image:       ws.Image,
		Volume:      volumeName,
		Network:     networkName,
		Env:         containerEnv,
		MemoryLimit: ws.MemoryLimit,
		NanoCPUs:    ws.NanoCPUs,
	})
	if err != nil {
		undo()
		return nil, fmt.Errorf("creating container: %w", err)
	}

	if err := s.container.Start(ctx, containerID); err != nil {
		_ = s.container.Remove(cleanupCtx, containerID)
		undo()
		return nil, fmt.Errorf("starting container: %w", err)
	}

	ws.Status = StatusRunning
	ws.ContainerID = containerID
	ws.VolumeName = volumeName
	ws.NetworkName = networkName
	if err := s.repo.Update(ctx, ws); err != nil {
		_ = s.container.Stop(cleanupCtx, containerID)
		_ = s.container.Remove(cleanupCtx, containerID)
		undo()
		return nil, fmt.Errorf("updating workspace: %w", err)
	}

	// Wait for the agent to be ready before performing post-start actions.
	if err := s.waitForAgent(ctx, ws); err != nil {
		log.Printf("workspace %d: agent not ready after create: %v", ws.ID, err)
	}

	s.injectUserSSHKeys(ctx, ws)

	return ws, nil
}

// containerEnv is the environment a workspace's container starts with: the
// agent's token and, if any services are already attached, how to reach
// them.
func (s *service) containerEnv(ctx context.Context, workspaceID int64, agentToken, networkName string) []string {
	env := []string{fmt.Sprintf("AGENT_AUTH_TOKEN=%s", agentToken)}
	if s.sidecar != nil {
		svcEnv, err := s.sidecar.EnvVars(ctx, workspaceID, networkName)
		if err != nil {
			log.Printf("workspace %d: failed to get sidecar env vars: %v", workspaceID, err)
		} else if len(svcEnv) > 0 {
			env = append(env, svcEnv...)
			env = append(env, serviceVarsEnv+"="+strings.Join(envNames(svcEnv), ","))
		}
	}
	return env
}

func (s *service) Delete(ctx context.Context, userID, workspaceID int64) error {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	done, err := s.beginLifecycle(ws.ID)
	if err != nil {
		return err
	}
	defer done()

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
	s.forgetAgentSyncLock(ws.ID)
	return nil
}

// Start starts a project or standalone workspace. Sandboxes start through
// StartSandbox, so their project can count them.
func (s *service) Start(ctx context.Context, userID, workspaceID int64) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.IsSandbox() {
		return nil, ErrSandboxStart
	}
	done, err := s.beginLifecycle(ws.ID)
	if err != nil {
		return nil, err
	}
	defer done()
	return s.start(ctx, ws)
}

// StartSandbox starts a sandbox. The caller has counted it against its
// project's sandbox limit.
func (s *service) StartSandbox(ctx context.Context, userID, workspaceID int64) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if !ws.IsSandbox() {
		return nil, fmt.Errorf("starting workspace %d: it is not a sandbox", ws.ID)
	}
	done, err := s.beginLifecycle(ws.ID)
	if err != nil {
		return nil, err
	}
	defer done()
	return s.start(ctx, ws)
}

// start starts a workspace's container under the caller's lifecycle hold.
func (s *service) start(ctx context.Context, ws *Workspace) (*Workspace, error) {
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

	// The workspace has started whether or not its caller is still there to
	// see it, and the start hook may resume an agent in it, so neither
	// waiting for the agent nor giving it the git identity stops with the
	// caller.
	started := context.WithoutCancel(ctx)

	// Wait for the agent to be ready before performing post-start actions.
	if err := s.waitForAgent(started, ws); err != nil {
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
		// Services added while it was stopped, or since its container was
		// made, are not in the container's environment.
		s.logSyncServiceEnv(ctx, ws)
	}

	// Before the start hook, so an agent resumed by it commits as the user
	// now wants.
	s.logSyncGitIdentity(started, ws)

	if s.startHook != nil {
		s.startHook.WorkspaceStarted(started, ws)
	}
	return ws, nil
}

func (s *service) Stop(ctx context.Context, userID, workspaceID int64) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	// Stopping mid-conversion would leave devpad unable to undo its work.
	done, err := s.beginLifecycle(ws.ID)
	if err != nil {
		return nil, err
	}
	defer done()
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

// Restart restarts a running workspace's container, installing the current
// agent first if it is outdated, so one restart brings it up to date.
// Terminals and running processes restart with it; the volume and attached
// services are untouched. A stopped workspace is refused rather than
// started, so a sandbox cannot start this way.
func (s *service) Restart(ctx context.Context, userID, workspaceID int64) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	done, err := s.beginLifecycle(ws.ID)
	if err != nil {
		return nil, err
	}
	defer done()
	if ws.Status != StatusRunning {
		return nil, ErrNotRunning
	}
	if ws.ContainerID == "" {
		return nil, fmt.Errorf("workspace has no container")
	}

	// Best-effort: an agent that can't be replaced now still restarts, and
	// the check after the restart tries again.
	if _, err := s.installAgentIfOutdated(ctx, ws); err != nil {
		log.Printf("workspace %d: agent update before restart failed: %v", ws.ID, err)
	}
	if err := s.container.Restart(ctx, ws.ContainerID); err != nil {
		return nil, fmt.Errorf("restarting container: %w", err)
	}
	if err := s.waitForAgent(ctx, ws); err != nil {
		log.Printf("workspace %d: agent not ready after restart: %v", ws.ID, err)
	}
	if err := s.ensureAgentUpdated(ctx, ws); err != nil {
		log.Printf("workspace %d: agent update failed: %v", ws.ID, err)
	}
	// An agent that was just updated has none of the services' variables
	// an older one may have been unable to keep.
	s.logSyncServiceEnv(ctx, ws)
	s.logSyncGitIdentity(ctx, ws)

	s.injectUserSSHKeys(ctx, ws)
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
