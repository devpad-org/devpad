package workspace

import (
	"context"
	"fmt"
	"log"
)

// RebuildProjectContainer replaces a project workspace's container with one
// made from image, keeping its volume, network and agent token. Only
// /workspace survives; anything else the old container held is lost. The new
// container is made before the old one goes, so a failure leaves the
// workspace as it was. A running workspace is started again.
func (s *service) RebuildProjectContainer(ctx context.Context, userID, workspaceID int64, image string) (*Workspace, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	if !ws.IsProjectWorkspace() {
		return nil, ErrNotProjectWorkspace
	}
	done, err := s.beginLifecycle(ws.ID)
	if err != nil {
		return nil, err
	}
	defer done()
	if ws.ContainerID == "" {
		return nil, fmt.Errorf("workspace has no container")
	}

	name := fmt.Sprintf("%d", ws.ID)
	env := s.containerEnv(ctx, ws.ID, ws.AgentToken, ws.NetworkName)
	newID, err := s.container.Create(ctx, name+"-next", image, ws.VolumeName, ws.NetworkName, env, ws.MemoryLimit, ws.NanoCPUs)
	if err != nil {
		return nil, fmt.Errorf("creating the new container: %w", err)
	}

	oldID := ws.ContainerID
	_ = s.container.Stop(ctx, oldID)
	if err := s.container.Remove(ctx, oldID); err != nil {
		_ = s.container.Remove(ctx, newID)
		// Stopping it may have worked; start it again so the workspace is
		// as it was.
		if ws.Status == StatusRunning {
			_ = s.container.Start(ctx, oldID)
		}
		return nil, fmt.Errorf("removing the old container: %w", err)
	}
	// The old container is gone, so from here the workspace holds the new
	// one whatever else fails.
	ws.ContainerID, ws.Image = newID, image
	if err := s.container.Rename(ctx, newID, "devpad-ws-"+name); err != nil {
		log.Printf("workspace %d: naming the rebuilt container: %v", ws.ID, err)
	}
	wasRunning := ws.Status == StatusRunning
	if err := s.repo.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("recording the new container: %w", err)
	}
	if !wasRunning {
		return ws, nil
	}

	if err := s.container.Start(ctx, newID); err != nil {
		ws.Status = StatusStopped
		if updErr := s.repo.Update(ctx, ws); updErr != nil {
			log.Printf("workspace %d: recording it stopped: %v", ws.ID, updErr)
		}
		return nil, fmt.Errorf("starting the new container: %w", err)
	}
	if err := s.waitForAgent(ctx, ws); err != nil {
		log.Printf("workspace %d: agent not ready after rebuild: %v", ws.ID, err)
	}
	// An image built before this server's agent carries an older one.
	if err := s.ensureAgentUpdated(ctx, ws); err != nil {
		log.Printf("workspace %d: agent update failed: %v", ws.ID, err)
	}
	return ws, nil
}
