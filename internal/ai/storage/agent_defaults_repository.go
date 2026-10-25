package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// AgentDefaultsRepository stores the model defaults set for built-in agents.
type AgentDefaultsRepository struct {
	db *sql.DB
}

// NewAgentDefaultsRepository creates a SQLite-backed agent defaults repository.
func NewAgentDefaultsRepository(db *sql.DB) *AgentDefaultsRepository {
	return &AgentDefaultsRepository{db: db}
}

func (r *AgentDefaultsRepository) ListAgentDefaults(ctx context.Context) ([]domain.AgentDefaults, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT agent_id, model, effort FROM ai_agent_defaults ORDER BY agent_id`)
	if err != nil {
		return nil, fmt.Errorf("querying agent defaults: %w", err)
	}
	defer rows.Close()

	defaults := make([]domain.AgentDefaults, 0)
	for rows.Next() {
		var d domain.AgentDefaults
		if err := rows.Scan(&d.AgentID, &d.Model, &d.Effort); err != nil {
			return nil, fmt.Errorf("scanning agent defaults: %w", err)
		}
		defaults = append(defaults, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agent defaults: %w", err)
	}
	return defaults, nil
}

func (r *AgentDefaultsRepository) SetAgentDefaults(ctx context.Context, defaults domain.AgentDefaults) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_agent_defaults (agent_id, model, effort, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(agent_id) DO UPDATE SET model = excluded.model, effort = excluded.effort, updated_at = excluded.updated_at`,
		defaults.AgentID,
		defaults.Model,
		defaults.Effort,
		time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("upserting agent defaults: %w", err)
	}
	return nil
}

func (r *AgentDefaultsRepository) DeleteAgentDefaults(ctx context.Context, agentID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM ai_agent_defaults WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("deleting agent defaults: %w", err)
	}
	return nil
}
