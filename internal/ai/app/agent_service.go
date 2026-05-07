package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	maxAgentNameLength         = 80
	maxAgentPurposeLength      = 200
	maxAgentInstructionsLength = 8000
)

// AgentRepository persists custom AI agents.
type AgentRepository interface {
	CreateAgent(ctx context.Context, agent *domain.Agent) error
	GetAgent(ctx context.Context, id string) (*domain.Agent, error)
	ListAgents(ctx context.Context, userID, workspaceID int64) ([]domain.Agent, error)
	UpdateAgent(ctx context.Context, agent *domain.Agent) error
	DeleteAgent(ctx context.Context, id string, userID int64) error
}

// AgentService manages user and workspace-scoped agents.
type AgentService interface {
	ListAgents(ctx context.Context, userID, workspaceID int64) ([]domain.Agent, error)
	GetAgent(ctx context.Context, userID, workspaceID int64, id string) (*domain.Agent, error)
	CreateAgent(ctx context.Context, req SaveAgentRequest) (*domain.Agent, error)
	UpdateAgent(ctx context.Context, req SaveAgentRequest) (*domain.Agent, error)
	DeleteAgent(ctx context.Context, userID int64, id string) error
}

// SaveAgentRequest is used by create/update operations.
type SaveAgentRequest struct {
	ID           string
	UserID       int64
	WorkspaceID  int64
	Name         string
	Purpose      string
	Instructions string
	IsGlobal     bool
}

type agentService struct {
	repo AgentRepository
}

// NewAgentService creates the app service for AI agents.
func NewAgentService(repo AgentRepository) AgentService {
	return &agentService{repo: repo}
}

func (s *agentService) ListAgents(ctx context.Context, userID, workspaceID int64) ([]domain.Agent, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("user ID is required")
	}
	if workspaceID <= 0 {
		return nil, fmt.Errorf("workspace ID is required")
	}
	agents, err := s.repo.ListAgents(ctx, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing custom agents: %w", err)
	}
	return append([]domain.Agent{domain.DefaultAgent()}, agents...), nil
}

func (s *agentService) GetAgent(ctx context.Context, userID, workspaceID int64, id string) (*domain.Agent, error) {
	if id == "" || id == domain.DefaultAgentID {
		agent := domain.DefaultAgent()
		return &agent, nil
	}
	agent, err := s.repo.GetAgent(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting agent: %w", err)
	}
	if agent == nil || agent.UserID != userID || (!agent.IsGlobal && agent.WorkspaceID != workspaceID) {
		return nil, domain.ErrAgentNotFound
	}
	return agent, nil
}

func (s *agentService) CreateAgent(ctx context.Context, req SaveAgentRequest) (*domain.Agent, error) {
	agent, err := agentFromRequest(req)
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreateAgent(ctx, agent); err != nil {
		return nil, fmt.Errorf("creating agent: %w", err)
	}
	return agent, nil
}

func (s *agentService) UpdateAgent(ctx context.Context, req SaveAgentRequest) (*domain.Agent, error) {
	if req.ID == "" || req.ID == domain.DefaultAgentID {
		return nil, domain.ErrAgentNotEditable
	}
	existing, err := s.repo.GetAgent(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("getting existing agent: %w", err)
	}
	if existing == nil || existing.UserID != req.UserID {
		return nil, domain.ErrAgentNotFound
	}
	agent, err := agentFromRequest(req)
	if err != nil {
		return nil, err
	}
	agent.ID = req.ID
	if err := s.repo.UpdateAgent(ctx, agent); err != nil {
		return nil, fmt.Errorf("updating agent: %w", err)
	}
	return s.repo.GetAgent(ctx, req.ID)
}

func (s *agentService) DeleteAgent(ctx context.Context, userID int64, id string) error {
	if id == "" || id == domain.DefaultAgentID {
		return domain.ErrAgentNotEditable
	}
	if err := s.repo.DeleteAgent(ctx, id, userID); err != nil {
		return fmt.Errorf("deleting agent: %w", err)
	}
	return nil
}

func agentFromRequest(req SaveAgentRequest) (*domain.Agent, error) {
	name := strings.TrimSpace(req.Name)
	purpose := strings.TrimSpace(req.Purpose)
	instructions := strings.TrimSpace(req.Instructions)
	if req.UserID <= 0 {
		return nil, fmt.Errorf("user ID is required")
	}
	if !req.IsGlobal && req.WorkspaceID <= 0 {
		return nil, fmt.Errorf("workspace ID is required")
	}
	if name == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if instructions == "" {
		return nil, fmt.Errorf("agent instructions are required")
	}
	if len(name) > maxAgentNameLength {
		return nil, fmt.Errorf("agent name must be %d characters or fewer", maxAgentNameLength)
	}
	if len(purpose) > maxAgentPurposeLength {
		return nil, fmt.Errorf("agent purpose must be %d characters or fewer", maxAgentPurposeLength)
	}
	if len(instructions) > maxAgentInstructionsLength {
		return nil, fmt.Errorf("agent instructions must be %d characters or fewer", maxAgentInstructionsLength)
	}
	workspaceID := req.WorkspaceID
	if req.IsGlobal {
		workspaceID = 0
	}
	return &domain.Agent{
		ID:           req.ID,
		UserID:       req.UserID,
		WorkspaceID:  workspaceID,
		Name:         name,
		Purpose:      purpose,
		Instructions: instructions,
		IsGlobal:     req.IsGlobal,
	}, nil
}
