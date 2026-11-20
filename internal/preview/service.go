package preview

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/workspace"
)

const tokenTTL = 30 * time.Second

// PreviewCookieName is the cookie a preview's host sets after token exchange.
// The __Host- prefix makes browsers keep it to that one host, so each preview
// has a sign-in of its own that no other preview can replace.
const PreviewCookieName = "__Host-devpad_preview"

// PreviewSessionDuration is how long a preview cookie remains valid.
const PreviewSessionDuration = 24 * time.Hour

var (
	ErrInvalidToken  = errors.New("invalid or expired preview token")
	ErrTokenUsed     = errors.New("preview token already used")
	ErrWorkspaceDown = errors.New("workspace is not running")
)

// Service defines preview business logic.
type Service interface {
	// GenerateURL creates a short-lived preview URL for a workspace port.
	GenerateURL(ctx context.Context, userID, workspaceID int64, port int) (string, error)
	// ValidateToken checks and consumes a single-use preview token, returning its claims.
	ValidateToken(ctx context.Context, tokenStr string) (*Token, error)
	// ResolveContainerAddr gets the agent address and auth token for proxying a preview request.
	ResolveContainerAddr(ctx context.Context, workspaceID int64) (addr, agentToken string, err error)
}

// Config says where preview URLs point.
type Config struct {
	// Domain is the preview domain, under which each preview has its own
	// subdomain.
	Domain string
	// HTTPSPort is the main server's HTTPS port. A port other than 443 is
	// put in preview URLs.
	HTTPSPort int
}

type service struct {
	repo       Repository
	workspaces workspace.Repository
	container  container.Manager
	cfg        Config
}

// NewService creates a new preview Service.
func NewService(repo Repository, wsRepo workspace.Repository, cm container.Manager, cfg Config) Service {
	return &service{repo: repo, workspaces: wsRepo, container: cm, cfg: cfg}
}

func (s *service) GenerateURL(ctx context.Context, userID, workspaceID int64, port int) (string, error) {
	ws, err := s.workspaces.GetByID(ctx, workspaceID)
	if err != nil {
		return "", fmt.Errorf("getting workspace: %w", err)
	}
	if ws == nil || ws.UserID != userID {
		return "", workspace.ErrNotFound
	}
	if ws.Status != workspace.StatusRunning {
		return "", ErrWorkspaceDown
	}

	t := &Token{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Port:        port,
		ExpiresAt:   time.Now().Add(tokenTTL),
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return "", fmt.Errorf("creating preview token: %w", err)
	}

	host := fmt.Sprintf("%d-%d.%s", workspaceID, port, s.cfg.Domain)
	if s.cfg.HTTPSPort != 0 && s.cfg.HTTPSPort != 443 {
		host = fmt.Sprintf("%s:%d", host, s.cfg.HTTPSPort)
	}
	url := fmt.Sprintf("https://%s?token=%s", host, t.Token)
	return url, nil
}

func (s *service) ValidateToken(ctx context.Context, tokenStr string) (*Token, error) {
	t, err := s.repo.GetByToken(ctx, tokenStr)
	if err != nil {
		return nil, fmt.Errorf("looking up preview token: %w", err)
	}
	if t == nil {
		return nil, ErrInvalidToken
	}
	if t.Used {
		return nil, ErrTokenUsed
	}

	if err := s.repo.MarkUsed(ctx, t.ID); err != nil {
		return nil, fmt.Errorf("marking token used: %w", err)
	}

	return t, nil
}

func (s *service) ResolveContainerAddr(ctx context.Context, workspaceID int64) (string, string, error) {
	ws, err := s.workspaces.GetByID(ctx, workspaceID)
	if err != nil {
		return "", "", fmt.Errorf("getting workspace: %w", err)
	}
	if ws == nil {
		return "", "", workspace.ErrNotFound
	}
	if ws.ContainerID == "" || ws.Status != workspace.StatusRunning {
		return "", "", ErrWorkspaceDown
	}

	// Previews are proxied to the container IP, so devpad must be on the
	// workspace network. No-op when devpad runs on the host.
	if err := s.container.EnsureSelfAttached(ctx, ws.NetworkName); err != nil {
		return "", "", fmt.Errorf("joining workspace network: %w", err)
	}

	ip, err := s.container.GetIP(ctx, ws.ContainerID, ws.NetworkName)
	if err != nil {
		return "", "", fmt.Errorf("getting container IP: %w", err)
	}

	return fmt.Sprintf("%s:%d", ip, agent.DefaultPort), ws.AgentToken, nil
}
