package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Repository defines data access for workspaces.
type Repository interface {
	Create(ctx context.Context, ws *Workspace) error
	GetByID(ctx context.Context, id int64) (*Workspace, error)
	ListByUserID(ctx context.Context, userID int64) ([]*Workspace, error)
	ListAll(ctx context.Context) ([]*Workspace, error)
	Update(ctx context.Context, ws *Workspace) error
	SetDefaultAgentID(ctx context.Context, userID, workspaceID int64, agentID string) (bool, error)
	Delete(ctx context.Context, id int64) error
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new workspace Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, ws *Workspace) error {
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO workspaces (user_id, name, description, status, container_id, volume_name, network_name, agent_token, default_agent_id, memory_limit, nano_cpus, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ws.UserID, ws.Name, ws.Description, ws.Status, ws.ContainerID, ws.VolumeName, ws.NetworkName, ws.AgentToken, defaultAgentID(ws.DefaultAgentID), ws.MemoryLimit, ws.NanoCPUs, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting workspace: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}

	ws.ID = id
	ws.DefaultAgentID = defaultAgentID(ws.DefaultAgentID)
	ws.CreatedAt = now
	ws.UpdatedAt = now
	return nil
}

func (r *repository) GetByID(ctx context.Context, id int64) (*Workspace, error) {
	ws := &Workspace{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, description, status, container_id, volume_name, network_name, agent_token, default_agent_id, memory_limit, nano_cpus, created_at, updated_at FROM workspaces WHERE id = ?`,
		id,
	).Scan(&ws.ID, &ws.UserID, &ws.Name, &ws.Description, &ws.Status, &ws.ContainerID, &ws.VolumeName, &ws.NetworkName, &ws.AgentToken, &ws.DefaultAgentID, &ws.MemoryLimit, &ws.NanoCPUs, &ws.CreatedAt, &ws.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying workspace by id: %w", err)
	}
	return ws, nil
}

func (r *repository) ListByUserID(ctx context.Context, userID int64) ([]*Workspace, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, name, description, status, container_id, volume_name, network_name, agent_token, default_agent_id, memory_limit, nano_cpus, created_at, updated_at FROM workspaces WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	defer rows.Close()

	var workspaces []*Workspace
	for rows.Next() {
		ws := &Workspace{}
		if err := rows.Scan(&ws.ID, &ws.UserID, &ws.Name, &ws.Description, &ws.Status, &ws.ContainerID, &ws.VolumeName, &ws.NetworkName, &ws.AgentToken, &ws.DefaultAgentID, &ws.MemoryLimit, &ws.NanoCPUs, &ws.CreatedAt, &ws.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning workspace: %w", err)
		}
		workspaces = append(workspaces, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating workspaces: %w", err)
	}
	return workspaces, nil
}

func (r *repository) ListAll(ctx context.Context) ([]*Workspace, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, name, description, status, container_id, volume_name, network_name, agent_token, default_agent_id, memory_limit, nano_cpus, created_at, updated_at FROM workspaces ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing all workspaces: %w", err)
	}
	defer rows.Close()

	var workspaces []*Workspace
	for rows.Next() {
		ws := &Workspace{}
		if err := rows.Scan(&ws.ID, &ws.UserID, &ws.Name, &ws.Description, &ws.Status, &ws.ContainerID, &ws.VolumeName, &ws.NetworkName, &ws.AgentToken, &ws.DefaultAgentID, &ws.MemoryLimit, &ws.NanoCPUs, &ws.CreatedAt, &ws.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning workspace: %w", err)
		}
		workspaces = append(workspaces, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating workspaces: %w", err)
	}
	return workspaces, nil
}

func (r *repository) Update(ctx context.Context, ws *Workspace) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE workspaces SET name = ?, description = ?, status = ?, container_id = ?, volume_name = ?, network_name = ?, agent_token = ?, default_agent_id = ?, memory_limit = ?, nano_cpus = ?, updated_at = ? WHERE id = ?`,
		ws.Name, ws.Description, ws.Status, ws.ContainerID, ws.VolumeName, ws.NetworkName, ws.AgentToken, defaultAgentID(ws.DefaultAgentID), ws.MemoryLimit, ws.NanoCPUs, now, ws.ID,
	)
	if err != nil {
		return fmt.Errorf("updating workspace: %w", err)
	}
	ws.UpdatedAt = now
	return nil
}

func (r *repository) SetDefaultAgentID(ctx context.Context, userID, workspaceID int64, agentID string) (bool, error) {
	now := time.Now()
	agentID = defaultAgentID(agentID)
	result, err := r.db.ExecContext(ctx,
		`UPDATE workspaces
		    SET default_agent_id = ?, updated_at = ?
		  WHERE id = ?
		    AND user_id = ?
		    AND (
				? = ?
				OR EXISTS (
					SELECT 1
					  FROM ai_agents
					 WHERE id = ?
					   AND user_id = ?
					   AND (is_global = 1 OR workspace_id = ?)
				)
			)`,
		agentID,
		now,
		workspaceID,
		userID,
		agentID,
		DefaultAgentID,
		agentID,
		userID,
		workspaceID,
	)
	if err != nil {
		return false, fmt.Errorf("updating workspace default agent: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking updated workspace default agent rows: %w", err)
	}
	return affected > 0, nil
}

func defaultAgentID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return DefaultAgentID
	}
	id, err := strconv.ParseInt(agentID, 10, 64)
	if err == nil && id > 0 {
		return strconv.FormatInt(id, 10)
	}
	return agentID
}

func (r *repository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting workspace: %w", err)
	}
	return nil
}
