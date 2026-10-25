package project

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"path"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Converting a standalone workspace makes a project from one of its repos,
// or from its files when it has none. Devpad pulls a bundle of every branch
// and tag from the workspace and imports it into a new, empty canonical
// repo, the one time a bundle may set the default branch.
//
// A repo at /workspace stays where it is, and the workspace becomes the
// project's workspace: uncommitted work, untracked files and everything else
// in the volume survive, and the SSH keys devpad put in it are removed,
// since a project workspace never holds the user's credentials. A repo in a
// subdirectory cannot become the project workspace's repo in place, because
// devpad works on the repo at /workspace; its branches seed the project, the
// project gets a fresh workspace when first opened, and the old workspace is
// left as it was.

// ConvertInput describes the project to make from a standalone workspace.
type ConvertInput struct {
	// RepoPath picks a repo found in the workspace, relative to /workspace.
	// Empty makes a repo from the workspace's files.
	RepoPath string
	// IncludeSensitive commits files that usually hold secrets, such as
	// .env, when making a repo from the workspace's files.
	IncludeSensitive bool
	Name             string
	Description      string
	DefaultBranch    string
	UpstreamURL      string
	SetupScript      string
	MaxSandboxes     int
}

// ConvertResult is a converted workspace's new project.
type ConvertResult struct {
	Project *Project
	// Workspace is the converted workspace once it is the project's
	// workspace; nil when its repo was in a subdirectory and it was left
	// as it was.
	Workspace *workspace.Workspace
	// SSH reports which of devpad's SSH files were removed from the
	// workspace; nil when the workspace was left as it was.
	SSH *agent.SSHCleanup
}

func (s *service) DiscoverConversion(ctx context.Context, userID, workspaceID int64) (*agent.RepoDiscovery, error) {
	return s.workspaces.DiscoverRepos(ctx, userID, workspaceID)
}

// ConvertWorkspace makes a project from a running standalone workspace. Any
// failure before the workspace is attached removes the half-made project
// and its repo container, and a repo made from the workspace's files, so
// the workspace is left as it was.
func (s *service) ConvertWorkspace(ctx context.Context, userID, workspaceID int64, in ConvertInput) (*ConvertResult, error) {
	// Like opening a project workspace, conversion outlives the request, so
	// a client that goes away cannot leave a half-made project behind.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspaceOpenTimeout)
	defer cancel()

	p, repoPath, err := s.convertInput(userID, in)
	if err != nil {
		return nil, err
	}
	inPlace := repoPath == "" || repoPath == "."

	release, err := s.workspaces.BeginConversion(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer release()

	// Devpad keeps its copies of project branches under the "devpad"
	// remote, so a repo staying in place must not have one of its own.
	if repoPath == "." {
		found, err := s.workspaces.DiscoverRepos(ctx, userID, workspaceID)
		if err != nil {
			return nil, err
		}
		for _, repo := range found.Repos {
			if repo.Path == "." && repo.HasDevpadRemote {
				return nil, invalid("the repo has a remote named %q, which devpad needs; rename it first", "devpad")
			}
		}
	}

	initSHA := ""
	if repoPath == "" {
		made, err := s.workspaces.InitRepoFromContents(ctx, userID, workspaceID, p.DefaultBranch, in.IncludeSensitive)
		if err != nil {
			return nil, fmt.Errorf("making a repo from the workspace's files: %w", err)
		}
		initSHA = made.SHA
		repoPath = "."
	}
	undoInit := func() {
		if initSHA == "" {
			return
		}
		if err := s.workspaces.UndoInitRepoFromContents(ctx, userID, workspaceID, initSHA); err != nil {
			log.Printf("workspace %d: removing the repo made for a failed conversion: %v", workspaceID, err)
		}
	}

	// The project stays "creating" until the workspace is attached, so
	// nothing else can open its workspace or start a sandbox meanwhile, and
	// the workspace lock keeps Delete waiting.
	if err := s.repo.Create(ctx, p); err != nil {
		undoInit()
		return nil, fmt.Errorf("creating project: %w", err)
	}
	unlock := s.lockWorkspace(p.ID)
	defer unlock()
	if err := s.provisionRepoContainer(ctx, p); err != nil {
		if delErr := s.repo.Delete(ctx, p.ID); delErr != nil {
			log.Printf("project %d: removing project after a failed conversion: %v", p.ID, delErr)
		}
		undoInit()
		return nil, err
	}
	fail := func(err error) (*ConvertResult, error) {
		s.discardProject(ctx, p)
		undoInit()
		return nil, err
	}

	if err := s.initRepo(ctx, p, true); err != nil {
		return fail(err)
	}
	if err := s.importWorkspaceRepo(ctx, p, userID, workspaceID, repoPath); err != nil {
		return fail(err)
	}

	result := &ConvertResult{Project: p}
	if inPlace {
		cleanup, err := s.workspaces.AttachToProject(ctx, userID, workspaceID, p.ID)
		if err != nil {
			return fail(err)
		}
		result.SSH = cleanup
	}

	p.Status, p.Error = StatusReady, ""
	if err := s.repo.Update(ctx, p); err != nil {
		return nil, fmt.Errorf("updating project: %w", err)
	}
	if inPlace {
		ws, err := s.workspaces.Get(ctx, userID, workspaceID)
		if err != nil {
			return nil, err
		}
		result.Workspace = ws
	}
	return result, nil
}

// convertInput validates a conversion and returns the project to create and
// the cleaned repo path.
func (s *service) convertInput(userID int64, in ConvertInput) (*Project, string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, "", invalid("name is required")
	}
	branch := strings.TrimSpace(in.DefaultBranch)
	if branch == "" {
		branch = DefaultBranch
	}
	if !validBranchName(branch) {
		return nil, "", invalid("defaultBranch is not a valid branch name")
	}
	upstream := strings.TrimSpace(in.UpstreamURL)
	if strings.HasPrefix(upstream, "-") {
		return nil, "", invalid("upstreamUrl must not start with '-'")
	}
	if hasURLCredentials(upstream) {
		return nil, "", invalid("upstreamUrl must not contain credentials; the project pushes with your SSH key")
	}
	maxSandboxes, err := normalizeMaxSandboxes(in.MaxSandboxes)
	if err != nil {
		return nil, "", err
	}
	repoPath := strings.TrimSpace(in.RepoPath)
	if repoPath != "" {
		repoPath = path.Clean(repoPath)
		if path.IsAbs(repoPath) || repoPath == ".." || strings.HasPrefix(repoPath, "../") || strings.HasPrefix(repoPath, "-") {
			return nil, "", invalid("repoPath must be inside the workspace")
		}
	}
	token, err := randomHex(32)
	if err != nil {
		return nil, "", fmt.Errorf("generating repo agent token: %w", err)
	}
	return &Project{
		UserID:         userID,
		Name:           name,
		Description:    in.Description,
		DefaultBranch:  branch,
		UpstreamURL:    upstream,
		SetupScript:    in.SetupScript,
		MaxSandboxes:   maxSandboxes,
		Status:         StatusCreating,
		RepoAgentToken: token,
	}, repoPath, nil
}

// importWorkspaceRepo moves every branch and tag of the workspace's repo
// into the project's new, empty canonical repo.
func (s *service) importWorkspaceRepo(ctx context.Context, p *Project, userID, workspaceID int64, repoPath string) error {
	c, err := s.repoAgent(ctx, p)
	if err != nil {
		return err
	}
	bundle, err := s.workspaces.BundleAllBranches(ctx, userID, workspaceID, repoPath)
	if err != nil {
		return fmt.Errorf("bundling the workspace's branches: %w", err)
	}
	defer bundle.Close()
	if _, err := c.RepoImportInitial(ctx, p.DefaultBranch, bundle); err != nil {
		return fmt.Errorf("importing the workspace's branches: %w", err)
	}
	return nil
}

// discardProject removes a project whose conversion failed, with its repo
// container. Nothing else can have used it, since it never became ready.
func (s *service) discardProject(ctx context.Context, p *Project) {
	if err := s.removeRepoContainer(ctx, p); err != nil {
		log.Printf("project %d: removing repo container after a failed conversion: %v", p.ID, err)
	}
	if err := s.repo.Delete(ctx, p.ID); err != nil {
		log.Printf("project %d: removing project after a failed conversion: %v", p.ID, err)
	}
}

// hasURLCredentials reports whether an upstream URL embeds a password or
// token. A bare SSH user, as in ssh://git@host/repo, is not one.
func hasURLCredentials(raw string) bool {
	if !strings.Contains(raw, "://") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return false
	}
	_, hasPassword := u.User.Password()
	return hasPassword || strings.HasPrefix(u.Scheme, "http")
}
