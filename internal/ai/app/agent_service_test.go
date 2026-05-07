package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type memoryAgentRepo struct {
	agents map[string]domain.Agent
	nextID int
}

func newMemoryAgentRepo() *memoryAgentRepo {
	return &memoryAgentRepo{agents: map[string]domain.Agent{}, nextID: 1}
}

func (r *memoryAgentRepo) CreateAgent(_ context.Context, agent *domain.Agent) error {
	agent.ID = string(rune('0' + r.nextID))
	r.nextID++
	agent.CreatedAt = time.Now()
	agent.UpdatedAt = agent.CreatedAt
	r.agents[agent.ID] = *agent
	return nil
}

func (r *memoryAgentRepo) GetAgent(_ context.Context, id string) (*domain.Agent, error) {
	agent, ok := r.agents[id]
	if !ok {
		return nil, nil
	}
	return &agent, nil
}

func (r *memoryAgentRepo) ListAgents(_ context.Context, userID, workspaceID int64) ([]domain.Agent, error) {
	var out []domain.Agent
	for _, agent := range r.agents {
		if agent.UserID == userID && (agent.IsGlobal || agent.WorkspaceID == workspaceID) {
			out = append(out, agent)
		}
	}
	return out, nil
}

func (r *memoryAgentRepo) UpdateAgent(_ context.Context, agent *domain.Agent) error {
	if _, ok := r.agents[agent.ID]; !ok {
		return domain.ErrAgentNotFound
	}
	r.agents[agent.ID] = *agent
	return nil
}

func (r *memoryAgentRepo) DeleteAgent(_ context.Context, id string, userID int64) error {
	agent, ok := r.agents[id]
	if !ok || agent.UserID != userID {
		return domain.ErrAgentNotFound
	}
	delete(r.agents, id)
	return nil
}

func TestAgentServiceListIncludesDefaultAndScopedAgents(t *testing.T) {
	repo := newMemoryAgentRepo()
	repo.agents["1"] = domain.Agent{ID: "1", UserID: 10, WorkspaceID: 42, Name: "Review", Instructions: "review code"}
	repo.agents["2"] = domain.Agent{ID: "2", UserID: 10, IsGlobal: true, Name: "Design", Instructions: "design UI"}
	repo.agents["3"] = domain.Agent{ID: "3", UserID: 10, WorkspaceID: 99, Name: "Other", Instructions: "other"}
	service := NewAgentService(repo)

	agents, err := service.ListAgents(context.Background(), 10, 42)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected default plus 2 available agents, got %+v", agents)
	}
	if agents[0].ID != domain.DefaultAgentID || !agents[0].IsDefault {
		t.Fatalf("expected first agent to be default, got %+v", agents[0])
	}
}

func TestAgentServiceRejectsEditingDefaultAgent(t *testing.T) {
	service := NewAgentService(newMemoryAgentRepo())

	_, err := service.UpdateAgent(context.Background(), SaveAgentRequest{
		ID:           domain.DefaultAgentID,
		UserID:       1,
		WorkspaceID:  1,
		Name:         "Default",
		Instructions: "new",
	})
	if !errors.Is(err, domain.ErrAgentNotEditable) {
		t.Fatalf("expected ErrAgentNotEditable, got %v", err)
	}
}
