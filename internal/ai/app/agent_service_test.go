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
	if len(agents) != 4 {
		t.Fatalf("expected 2 built-in plus 2 available agents, got %+v", agents)
	}
	if agents[0].ID != domain.DefaultAgentID || !agents[0].IsDefault || !agents[0].IsBuiltIn {
		t.Fatalf("expected first agent to be the default coding agent, got %+v", agents[0])
	}
	if agents[1].ID != domain.ReviewAgentID || agents[1].IsDefault || !agents[1].IsBuiltIn {
		t.Fatalf("expected second agent to be the review agent, got %+v", agents[1])
	}
}

func TestAgentServiceGetsBuiltInAgents(t *testing.T) {
	service := NewAgentService(newMemoryAgentRepo())

	for _, id := range []string{"", domain.CodingAgentID, domain.ReviewAgentID} {
		agent, err := service.GetAgent(context.Background(), 1, 1, id)
		if err != nil {
			t.Fatalf("get agent %q: %v", id, err)
		}
		want := id
		if want == "" {
			want = domain.DefaultAgentID
		}
		if agent.ID != want || !agent.IsBuiltIn || agent.Instructions == "" {
			t.Fatalf("get agent %q: unexpected %+v", id, agent)
		}
	}
}

func TestAgentServiceRejectsEditingBuiltInAgents(t *testing.T) {
	service := NewAgentService(newMemoryAgentRepo())

	for _, agent := range domain.BuiltInAgents() {
		_, err := service.UpdateAgent(context.Background(), SaveAgentRequest{
			ID:           agent.ID,
			UserID:       1,
			WorkspaceID:  1,
			Name:         "Changed",
			Instructions: "new",
		})
		if !errors.Is(err, domain.ErrAgentNotEditable) {
			t.Fatalf("update %q: expected ErrAgentNotEditable, got %v", agent.ID, err)
		}
		if err := service.DeleteAgent(context.Background(), 1, agent.ID); !errors.Is(err, domain.ErrAgentNotEditable) {
			t.Fatalf("delete %q: expected ErrAgentNotEditable, got %v", agent.ID, err)
		}
	}
}
