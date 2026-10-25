package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Repository defines data access for workspaces.
type Repository interface {
	Create(ctx context.Context, ws *Workspace) error
	GetByID(ctx context.Context, id int64) (*Workspace, error)
	ListByUserID(ctx context.Context, userID int64) ([]*Workspace, error)
	ListByProjectID(ctx context.Context, projectID int64) ([]*Workspace, error)
	ListAll(ctx context.Context) ([]*Workspace, error)
	Update(ctx context.Context, ws *Workspace) error
	SetDefaultAgentID(ctx context.Context, userID, workspaceID int64, agentID string) (bool, error)
	MarkRepoSeeded(ctx context.Context, id int64) error
	// AttachToProject makes a standalone workspace a project's workspace,
	// reporting false if it was not standalone.
	AttachToProject(ctx context.Context, id, projectID int64) (bool, error)
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
		`INSERT INTO workspaces (user_id, name, description, status, container_id, volume_name, network_name, agent_token, default_agent_id, memory_limit, nano_cpus, project_id, kind, branch, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ws.UserID, ws.Name, ws.Description, ws.Status, ws.ContainerID, ws.VolumeName, ws.NetworkName, ws.AgentToken, defaultAgentID(ws.DefaultAgentID), ws.MemoryLimit, ws.NanoCPUs, nullableID(ws.ProjectID), workspaceKind(ws.Kind), ws.Branch, now, now,
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
	ws.Kind = workspaceKind(ws.Kind)
	ws.CreatedAt = now
	ws.UpdatedAt = now
	return nil
}

func (r *repository) GetByID(ctx context.Context, id int64) (*Workspace, error) {
	ws, err := scanWorkspace(r.db.QueryRowContext(ctx,
		`SELECT `+workspaceColumns+` FROM workspaces WHERE id = ?`,
		id,
	))
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
		`SELECT `+workspaceColumns+` FROM workspaces WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	return scanWorkspaces(rows)
}

func (r *repository) ListAll(ctx context.Context) ([]*Workspace, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+workspaceColumns+` FROM workspaces ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing all workspaces: %w", err)
	}
	return scanWorkspaces(rows)
}

func (r *repository) ListByProjectID(ctx context.Context, projectID int64) ([]*Workspace, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+workspaceColumns+` FROM workspaces WHERE project_id = ? ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing project workspaces: %w", err)
	}
	return scanWorkspaces(rows)
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

func (r *repository) MarkRepoSeeded(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE workspaces SET repo_seeded = 1, updated_at = ? WHERE id = ?`, time.Now(), id); err != nil {
		return fmt.Errorf("marking workspace repo seeded: %w", err)
	}
	return nil
}

func (r *repository) AttachToProject(ctx context.Context, id, projectID int64) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE workspaces SET project_id = ?, kind = ?, branch = '', repo_seeded = 1, updated_at = ?
		 WHERE id = ? AND project_id IS NULL AND kind = ?`,
		projectID, KindInteractive, time.Now(), id, KindInteractive,
	)
	if err != nil {
		return false, fmt.Errorf("attaching workspace to project: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("attaching workspace to project: %w", err)
	}
	return n == 1, nil
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
				?
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
		slices.Contains(builtInAgentIDs, agentID),
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

const workspaceColumns = `id, user_id, name, description, status, container_id, volume_name, network_name, agent_token, default_agent_id, memory_limit, nano_cpus, project_id, kind, branch, repo_seeded, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWorkspace(row rowScanner) (*Workspace, error) {
	ws := &Workspace{}
	var projectID sql.NullInt64
	err := row.Scan(&ws.ID, &ws.UserID, &ws.Name, &ws.Description, &ws.Status, &ws.ContainerID, &ws.VolumeName, &ws.NetworkName, &ws.AgentToken, &ws.DefaultAgentID, &ws.MemoryLimit, &ws.NanoCPUs, &projectID, &ws.Kind, &ws.Branch, &ws.RepoSeeded, &ws.CreatedAt, &ws.UpdatedAt)
	if err != nil {
		return nil, err
	}
	ws.ProjectID = projectID.Int64
	return ws, nil
}

func scanWorkspaces(rows *sql.Rows) ([]*Workspace, error) {
	defer rows.Close()

	var workspaces []*Workspace
	for rows.Next() {
		ws, err := scanWorkspace(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning workspace: %w", err)
		}
		workspaces = append(workspaces, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating workspaces: %w", err)
	}
	return workspaces, nil
}

func nullableID(id int64) sql.NullInt64 {
	return sql.NullInt64{Int64: id, Valid: id != 0}
}

func workspaceKind(kind Kind) Kind {
	if kind == "" {
		return KindInteractive
	}
	return kind
}
