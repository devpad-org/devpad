package workspace

import (
	"context"
	"fmt"
)

// ProvisionProjectServices gives a project workspace its own copy of each of
// its project's services it lacks, such as ones added to the project since
// it was made. A running workspace's copies are started and its agent is
// sent their variables; a stopped one's start with it.
//
// It holds the workspace as a start or stop does, so it never runs beside a
// rebuild, which provisions the same services for the container it makes.
func (s *service) ProvisionProjectServices(ctx context.Context, userID, workspaceID int64) error {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	if !ws.IsProjectWorkspace() {
		return ErrNotProjectWorkspace
	}
	if s.sidecar == nil {
		return nil
	}
	done, err := s.beginLifecycle(ws.ID)
	if err != nil {
		return err
	}
	defer done()
	// Read it again now that a rebuild cannot change it.
	if ws, err = s.Get(ctx, userID, workspaceID); err != nil {
		return err
	}
	running := ws.Status == StatusRunning
	if !running && ws.Status != StatusStopped {
		return ErrLifecycleBusy
	}
	if err := s.sidecar.Provision(ctx, ws.ID, ws.ProjectID, ws.NetworkName, running); err != nil {
		return fmt.Errorf("adding the project's services: %w", err)
	}
	if running {
		// The services work without it; terminals opened since lack their
		// variables.
		s.logSyncServiceEnv(ctx, ws)
	}
	return nil
}
