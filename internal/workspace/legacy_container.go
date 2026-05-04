package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
)

// ensureContainerHasToken checks whether the container's env includes
// AGENT_AUTH_TOKEN. Legacy containers created before agent auth was
// introduced will be missing it. In that case we stop and remove the old
// container and create a new one with the proper env, reusing the existing
// volume. We also backfill the token in the DB if it was empty.
func (s *service) ensureContainerHasToken(ctx context.Context, ws *Workspace) error {
	envVars, err := s.container.GetEnv(ctx, ws.ContainerID)
	if err != nil {
		return fmt.Errorf("inspecting container env: %w", err)
	}

	hasToken := false
	for _, e := range envVars {
		if len(e) > len("AGENT_AUTH_TOKEN=") && e[:len("AGENT_AUTH_TOKEN=")] == "AGENT_AUTH_TOKEN=" {
			hasToken = true
			break
		}
	}
	if hasToken {
		return nil
	}

	log.Printf("workspace %d: legacy container missing AGENT_AUTH_TOKEN, recreating", ws.ID)

	// Generate a token if the workspace record doesn't have one yet.
	if ws.AgentToken == "" {
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			return fmt.Errorf("generating agent token: %w", err)
		}
		ws.AgentToken = hex.EncodeToString(tokenBytes)
	}

	// Remove the old container (stop first in case it's in a restart loop).
	_ = s.container.Stop(ctx, ws.ContainerID)
	if err := s.container.Remove(ctx, ws.ContainerID); err != nil {
		return fmt.Errorf("removing legacy container: %w", err)
	}

	// Create a new container with the token env var, reusing the volume.
	containerEnv := []string{
		fmt.Sprintf("AGENT_AUTH_TOKEN=%s", ws.AgentToken),
	}
	newID, err := s.container.Create(ctx, fmt.Sprintf("%d", ws.ID), ws.VolumeName, ws.NetworkName, containerEnv, ws.MemoryLimit, ws.NanoCPUs)
	if err != nil {
		return fmt.Errorf("creating replacement container: %w", err)
	}

	ws.ContainerID = newID
	if err := s.repo.Update(ctx, ws); err != nil {
		return fmt.Errorf("updating workspace with new container: %w", err)
	}

	log.Printf("workspace %d: container recreated with auth token", ws.ID)
	return nil
}

// ensureNetwork creates a Docker network for legacy workspaces that were
// created before network isolation was introduced. The existing container is
// connected to the new network in-place via Docker's NetworkConnect API —
// no container recreation is required.
func (s *service) ensureNetwork(ctx context.Context, ws *Workspace) error {
	if ws.NetworkName != "" {
		return nil
	}

	networkName := fmt.Sprintf("devpad-net-%d", ws.ID)
	log.Printf("workspace %d: backfilling network %s", ws.ID, networkName)

	if err := s.container.CreateNetwork(ctx, networkName); err != nil {
		return fmt.Errorf("creating network: %w", err)
	}

	if err := s.container.ConnectToNetwork(ctx, networkName, ws.ContainerID); err != nil {
		_ = s.container.RemoveNetwork(ctx, networkName)
		return fmt.Errorf("connecting container to network: %w", err)
	}

	ws.NetworkName = networkName
	if err := s.repo.Update(ctx, ws); err != nil {
		return fmt.Errorf("updating workspace with network: %w", err)
	}

	log.Printf("workspace %d: network backfilled", ws.ID)
	return nil
}
