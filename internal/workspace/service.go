package workspace

import (
	"errors"
	"sync"

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

	// converting holds the workspaces being converted into a project, and
	// busy counts the starts, stops and deletes in flight for each; one
	// excludes the other.
	convertMu  sync.Mutex
	converting map[int64]bool
	busy       map[int64]int
}

// NewService creates a new workspace Service.
func NewService(repo Repository, cm container.Manager, users auth.UserRepository, cipher *encrypt.Cipher) Service {
	return &service{repo: repo, container: cm, users: users, cipher: cipher, agentPort: agent.DefaultPort}
}

func (s *service) SetSidecarService(sidecar SidecarService) {
	s.sidecar = sidecar
}
