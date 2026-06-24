package workspace

import (
	"errors"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/encrypt"
)

var (
	ErrNotFound             = errors.New("workspace not found")
	ErrForbidden            = errors.New("access denied")
	ErrNotRunning           = errors.New("workspace is not running")
	ErrDefaultAgentNotFound = errors.New("default agent not found")
)

type service struct {
	repo      Repository
	container container.Manager
	users     auth.UserRepository
	cipher    *encrypt.Cipher
	agentPort int
	sidecar   SidecarService
}

// NewService creates a new workspace Service.
func NewService(repo Repository, cm container.Manager, users auth.UserRepository, cipher *encrypt.Cipher) Service {
	return &service{repo: repo, container: cm, users: users, cipher: cipher, agentPort: agent.DefaultPort}
}

func (s *service) SetSidecarService(sidecar SidecarService) {
	s.sidecar = sidecar
}
