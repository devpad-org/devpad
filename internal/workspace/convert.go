package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/agentbin"
)

var (
	// ErrNotStandalone refuses converting a workspace that already belongs
	// to a project.
	ErrNotStandalone = errors.New("workspace already belongs to a project")
	// ErrConverting refuses a second conversion, or deleting, stopping or
	// starting the workspace, while it is being converted into a project.
	ErrConverting = errors.New("workspace is being converted into a project")
	// ErrAgentOutdated refuses converting a workspace whose running agent
	// predates the conversion endpoints; restarting it updates the agent.
	ErrAgentOutdated = errors.New("the workspace's agent is out of date; restart the workspace and try again")
	// ErrCredentialsPresent refuses making a workspace a project's workspace
	// while it holds credentials the user has not agreed to strip.
	ErrCredentialsPresent = errors.New("the workspace holds credentials")
)

// CredentialsError lists the credentials that stop a workspace becoming a
// project's workspace: found without the user's consent to strip them, or
// that could not be stripped.
type CredentialsError struct {
	Items []agent.CredentialItem
}

func (e *CredentialsError) Error() string {
	where := make([]string, 0, len(e.Items))
	for _, item := range e.Items {
		where = append(where, item.Location)
	}
	return fmt.Sprintf("%s that must be stripped first: %s", ErrCredentialsPresent, strings.Join(where, "; "))
}

func (e *CredentialsError) Unwrap() error { return ErrCredentialsPresent }

// AttachResult reports what was taken out of a workspace that became a
// project's workspace.
type AttachResult struct {
	SSH      *agent.SSHCleanup
	Stripped []agent.CredentialItem
}

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
// its repo already in place. A project workspace holds no credentials, so it
// first strips the ones the user agreed to (strip names them by ID), and
// refuses if any credential is left; then it removes the SSH files devpad put
// in the container: always devpad's key pair, and known_hosts and config
// where they still hold what devpad wrote. If the workspace cannot be
// attached, the keys are put back.
//
// The agent strips and removes from inside the workspace, which an AI may
// control, so devpad cannot verify any of it. That is why a conversion also
// rotates the user's key and tells them to revoke what was stripped: those
// stay safe however this went.
func (s *service) AttachToProject(ctx context.Context, userID, workspaceID, projectID int64, strip []string) (*AttachResult, error) {
	if projectID == 0 {
		return nil, fmt.Errorf("attaching workspace: no project")
	}
	ws, c, err := s.standaloneAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	stripped, err := c.StripCredentials(ctx, strip)
	if err != nil {
		return nil, fmt.Errorf("stripping credentials: %w", err)
	}
	left := append([]agent.CredentialItem(nil), stripped.Remaining...)
	for _, f := range stripped.Failed {
		log.Printf("workspace %d: stripping %s: %s", ws.ID, f.Location, f.Error)
		left = append(left, f.CredentialItem)
	}
	if len(left) > 0 {
		return nil, &CredentialsError{Items: left}
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
	return &AttachResult{SSH: cleanup, Stripped: stripped.Stripped}, nil
}

// RestartConverted restarts the container of a workspace that has just
// become a project's workspace, ending every process started while it held
// credentials, such as an ssh-agent with the key loaded. Terminals and
// running commands restart with it; the volume is untouched.
func (s *service) RestartConverted(ctx context.Context, userID, workspaceID int64) error {
	ws, err := s.Get(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	if ws.ReceivesUserCredentials() {
		return fmt.Errorf("restarting workspace %d: it is not a project workspace", ws.ID)
	}
	if ws.ContainerID == "" {
		return fmt.Errorf("workspace has no container")
	}
	if err := s.container.Restart(ctx, ws.ContainerID); err != nil {
		return fmt.Errorf("restarting container: %w", err)
	}
	if err := s.waitForAgent(ctx, ws); err != nil {
		return fmt.Errorf("agent not ready after restart: %w", err)
	}
	return nil
}
