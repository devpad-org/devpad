package workspace

import (
	"context"

	"github.com/devpad-org/devpad/internal/agent"
)

// GitActionRequest aliases the agent request model so handlers and services
// share the same wire shape without duplicating fields.
type GitActionRequest = agent.GitActionRequest

func (s *service) GitStatus(ctx context.Context, userID, workspaceID int64) (*agent.GitStatus, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitStatus(ctx)
}

func (s *service) GitLog(ctx context.Context, userID, workspaceID int64, count int, opts agent.GitLogOptions) ([]agent.GitCommit, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitLog(ctx, count, opts)
}

func (s *service) GitCommitFiles(ctx context.Context, userID, workspaceID int64, commit string) ([]agent.GitCommitFile, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitCommitFiles(ctx, commit)
}

func (s *service) GitCommitFileDiff(ctx context.Context, userID, workspaceID int64, commit, path, oldPath string) (*agent.GitFileDiff, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitCommitFileDiff(ctx, commit, path, oldPath)
}

func (s *service) GitFileDiff(ctx context.Context, userID, workspaceID int64, path string, staged bool) (*agent.GitFileDiff, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitFileDiff(ctx, path, staged)
}

func (s *service) GitBranches(ctx context.Context, userID, workspaceID int64) (*agent.GitBranches, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitBranches(ctx)
}

func (s *service) GitDiff(ctx context.Context, userID, workspaceID int64, path string, staged bool) (string, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return "", err
	}
	return c.GitDiff(ctx, path, staged)
}

func (s *service) GitRemotes(ctx context.Context, userID, workspaceID int64) ([]agent.GitRemote, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitRemotes(ctx)
}

func (s *service) GitAction(ctx context.Context, userID, workspaceID int64, req GitActionRequest) (*agent.GitActionResult, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GitAction(ctx, req)
}
