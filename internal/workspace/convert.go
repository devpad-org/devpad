package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/agentbin"
)

var (
	// ErrNotStandalone refuses converting a workspace that already belongs
	// to a project.
	ErrNotStandalone = errors.New("workspace already belongs to a project")
	// ErrConverting refuses a second conversion, or deleting the workspace,
	// while it is being converted into a project.
	ErrConverting = errors.New("workspace is being converted into a project")
	// ErrAgentOutdated refuses converting a workspace whose running agent
	// predates the conversion endpoints; restarting it updates the agent.
	ErrAgentOutdated = errors.New("the workspace's agent is out of date; restart the workspace and try again")
)

// standaloneAgent returns a running standalone workspace and its agent
// client, refusing an agent too old to have the conversion endpoints.
func (s *service) standaloneAgent(ctx context.Context, userID, workspaceID int64) (*Workspace, *agent.Client, error) {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !ws.ReceivesUserCredentials() {
		return nil, nil, ErrNotStandalone
	}
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if version, err := c.Version(ctx); err != nil || version != agentbin.Version {
		return nil, nil, ErrAgentOutdated
	}
	return ws, c, nil
}

// DiscoverRepos lists the git repos in a standalone workspace, or previews a
// repo made from its files when it has none.
func (s *service) DiscoverRepos(ctx context.Context, userID, workspaceID int64) (*agent.RepoDiscovery, error) {
	_, c, err := s.standaloneAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RepoDiscover(ctx)
}

// BeginConversion marks a running standalone workspace as being converted,
// so it cannot be converted twice at once or deleted meanwhile. The caller
// must call release when done.
func (s *service) BeginConversion(ctx context.Context, userID, workspaceID int64) (release func(), err error) {
	if _, _, err := s.standaloneAgent(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	s.convertMu.Lock()
	defer s.convertMu.Unlock()
	if s.converting == nil {
		s.converting = make(map[int64]bool)
	}
	if s.converting[workspaceID] {
		return nil, ErrConverting
	}
	s.converting[workspaceID] = true
	return func() {
		s.convertMu.Lock()
		defer s.convertMu.Unlock()
		delete(s.converting, workspaceID)
	}, nil
}

func (s *service) isConverting(workspaceID int64) bool {
	s.convertMu.Lock()
	defer s.convertMu.Unlock()
	return s.converting[workspaceID]
}

// InitRepoFromContents makes a repo at /workspace from the files in a
// standalone workspace, committing as the user.
func (s *service) InitRepoFromContents(ctx context.Context, userID, workspaceID int64, branch string, includeSensitive bool) (*agent.RepoContentsInit, error) {
	ws, c, err := s.standaloneAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	var name, email string
	if user, err := s.users.GetByID(ctx, ws.UserID); err == nil && user != nil {
		name, email = user.Username, user.Email
	}
	return c.RepoInitContents(ctx, branch, includeSensitive, name, email)
}

// UndoInitRepoFromContents removes a repo made by InitRepoFromContents if it
// still holds only the commit sha.
func (s *service) UndoInitRepoFromContents(ctx context.Context, userID, workspaceID int64, sha string) error {
	_, c, err := s.standaloneAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.RepoUndoInitContents(ctx, sha)
}

// BundleAllBranches streams a bundle of every branch and tag of the repo at
// dir in a standalone workspace. The bundle is untrusted; the caller must
// close it.
func (s *service) BundleAllBranches(ctx context.Context, userID, workspaceID int64, dir string) (io.ReadCloser, error) {
	_, c, err := s.standaloneAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.RepoBundleAll(ctx, dir)
}

// AttachToProject makes a standalone workspace the project's workspace, with
// its repo already in place. It first removes the SSH files devpad put in
// the container, since a project workspace never holds the user's
// credentials; files that differ from what devpad wrote are kept and
// reported. If the workspace cannot be attached, the keys are put back.
func (s *service) AttachToProject(ctx context.Context, userID, workspaceID, projectID int64) (*agent.SSHCleanup, error) {
	if projectID == 0 {
		return nil, fmt.Errorf("attaching workspace: no project")
	}
	ws, c, err := s.standaloneAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	user, err := s.users.GetByID(ctx, ws.UserID)
	if err != nil {
		return nil, fmt.Errorf("looking up user: %w", err)
	}
	files, err := managedSSHFiles(user, s.cipher)
	if err != nil {
		return nil, err
	}
	cleanup, err := c.SSHRemoveManaged(ctx, files)
	if err != nil {
		return nil, fmt.Errorf("removing SSH keys: %w", err)
	}

	attached, err := s.repo.AttachToProject(ctx, ws.ID, projectID)
	if err == nil && !attached {
		err = ErrNotStandalone
	}
	if err != nil {
		if len(cleanup.Removed) > 0 {
			if injectErr := s.injectSSHKeys(context.WithoutCancel(ctx), ws); injectErr != nil {
				log.Printf("workspace %d: restoring SSH keys after a failed conversion: %v", ws.ID, injectErr)
			}
		}
		return nil, fmt.Errorf("attaching workspace to project: %w", err)
	}
	return cleanup, nil
}
