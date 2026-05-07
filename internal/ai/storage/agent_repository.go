package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// AgentRepository stores user-defined AI agents.
type AgentRepository struct {
	db *sql.DB
}

// NewAgentRepository creates a SQLite-backed AI agent repository.
func NewAgentRepository(db *sql.DB) *AgentRepository {
	return &AgentRepository{db: db}
}

func (r *AgentRepository) CreateAgent(ctx context.Context, agent *domain.Agent) error {
	if agent.WorkspaceID != 0 {
		ok, err := r.workspaceOwnedByUser(ctx, agent.WorkspaceID, agent.UserID)
		if err != nil {
			return fmt.Errorf("checking workspace ownership: %w", err)
		}
		if !ok {
			return domain.ErrAgentNotFound
		}
	}
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_agents
		 (user_id, workspace_id, name, purpose, instructions, is_global, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		agent.UserID,
		nullableInt64(agent.WorkspaceID),
		agent.Name,
		agent.Purpose,
		agent.Instructions,
		boolToInt(agent.IsGlobal),
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("inserting agent: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting agent ID: %w", err)
	}
	agent.ID = fmt.Sprintf("%d", id)
	agent.CreatedAt = now
	agent.UpdatedAt = now
	return nil
}

func (r *AgentRepository) GetAgent(ctx context.Context, id string) (*domain.Agent, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, workspace_id, name, purpose, instructions, is_global, created_at, updated_at
		 FROM ai_agents
		 WHERE id = ?`,
		id,
	)
	agent, err := scanAgent(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning agent: %w", err)
	}
	return agent, nil
}

func (r *AgentRepository) ListAgents(ctx context.Context, userID, workspaceID int64) ([]domain.Agent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, workspace_id, name, purpose, instructions, is_global, created_at, updated_at
		 FROM ai_agents
		 WHERE user_id = ? AND (is_global = 1 OR workspace_id = ?)
		 ORDER BY is_global DESC, lower(name) ASC, id ASC`,
		userID,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying agents: %w", err)
	}
	defer rows.Close()

	agents := make([]domain.Agent, 0)
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning listed agent: %w", err)
		}
		agents = append(agents, *agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agents: %w", err)
	}
	return agents, nil
}

func (r *AgentRepository) UpdateAgent(ctx context.Context, agent *domain.Agent) error {
	if agent.WorkspaceID != 0 {
		ok, err := r.workspaceOwnedByUser(ctx, agent.WorkspaceID, agent.UserID)
		if err != nil {
			return fmt.Errorf("checking workspace ownership: %w", err)
		}
		if !ok {
			return domain.ErrAgentNotFound
		}
	}
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx,
		`UPDATE ai_agents
		 SET workspace_id = ?, name = ?, purpose = ?, instructions = ?, is_global = ?, updated_at = ?
		 WHERE id = ? AND user_id = ?`,
		nullableInt64(agent.WorkspaceID),
		agent.Name,
		agent.Purpose,
		agent.Instructions,
		boolToInt(agent.IsGlobal),
		now,
		agent.ID,
		agent.UserID,
	)
	if err != nil {
		return fmt.Errorf("updating agent: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking updated agent count: %w", err)
	}
	if count == 0 {
		return domain.ErrAgentNotFound
	}
	agent.UpdatedAt = now
	return nil
}

func (r *AgentRepository) DeleteAgent(ctx context.Context, id string, userID int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM ai_agents WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("deleting agent: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking deleted agent count: %w", err)
	}
	if count == 0 {
		return domain.ErrAgentNotFound
	}
	return nil
}

type agentScanner interface {
	Scan(dest ...any) error
}

func scanAgent(scanner agentScanner) (*domain.Agent, error) {
	var (
		agent       domain.Agent
		id          int64
		workspaceID sql.NullInt64
		isGlobal    int
	)
	if err := scanner.Scan(
		&id,
		&agent.UserID,
		&workspaceID,
		&agent.Name,
		&agent.Purpose,
		&agent.Instructions,
		&isGlobal,
		&agent.CreatedAt,
		&agent.UpdatedAt,
	); err != nil {
		return nil, err
	}
	agent.ID = fmt.Sprintf("%d", id)
	agent.WorkspaceID = workspaceID.Int64
	agent.IsGlobal = isGlobal == 1
	return &agent, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (r *AgentRepository) workspaceOwnedByUser(ctx context.Context, workspaceID, userID int64) (bool, error) {
	var count int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workspaces WHERE id = ? AND user_id = ?`,
		workspaceID,
		userID,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("querying workspace ownership: %w", err)
	}
	return count > 0, nil
}
