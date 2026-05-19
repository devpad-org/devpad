package workspace

import (
	"bytes"
	"context"
	"io"

	"github.com/devpad-org/devpad/internal/agent"
)

func (s *service) ListFiles(ctx context.Context, userID, workspaceID int64, path string, opts agent.ListFilesOptions) (*agent.FileList, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.ListFiles(ctx, path, opts)
}

func (s *service) ReadFile(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.ReadFile(ctx, path)
}

func (s *service) WriteFile(ctx context.Context, userID, workspaceID int64, path string, content []byte) error {
	return s.UploadFile(ctx, userID, workspaceID, path, bytes.NewReader(content))
}

func (s *service) UploadFile(ctx context.Context, userID, workspaceID int64, path string, content io.Reader) error {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.UploadFile(ctx, path, content)
}

func (s *service) DeleteFile(ctx context.Context, userID, workspaceID int64, path string) error {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.DeleteFile(ctx, path)
}

func (s *service) CreateDirectory(ctx context.Context, userID, workspaceID int64, path string) error {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.CreateDirectory(ctx, path)
}

func (s *service) RenameFile(ctx context.Context, userID, workspaceID int64, oldPath, newPath string) error {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.RenameFile(ctx, oldPath, newPath)
}

func (s *service) SearchFiles(ctx context.Context, userID, workspaceID int64, pattern, pathFilter string, maxResults int) ([]agent.SearchResult, error) {
	c, err := s.getAgent(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.SearchFiles(ctx, pattern, pathFilter, maxResults)
}
