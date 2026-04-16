package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Repository defines data access for workspaces.
type Repository interface {
	Create(ctx context.Context, ws *Workspace) error
	GetByID(ctx context.Context, id int64) (*Workspace, error)
	ListByUserID(ctx context.Context, userID int64) ([]*Workspace, error)
	Update(ctx context.Context, ws *Workspace) error
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
		`INSERT INTO workspaces (user_id, name, description, status, container_id, volume_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ws.UserID, ws.Name, ws.Description, ws.Status, ws.ContainerID, ws.VolumeName, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting workspace: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}

	ws.ID = id
	ws.CreatedAt = now
	ws.UpdatedAt = now
	return nil
}

func (r *repository) GetByID(ctx context.Context, id int64) (*Workspace, error) {
	ws := &Workspace{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, description, status, container_id, volume_name, created_at, updated_at FROM workspaces WHERE id = ?`,
		id,
	).Scan(&ws.ID, &ws.UserID, &ws.Name, &ws.Description, &ws.Status, &ws.ContainerID, &ws.VolumeName, &ws.CreatedAt, &ws.UpdatedAt)
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
		`SELECT id, user_id, name, description, status, container_id, volume_name, created_at, updated_at FROM workspaces WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	defer rows.Close()

	var workspaces []*Workspace
	for rows.Next() {
		ws := &Workspace{}
		if err := rows.Scan(&ws.ID, &ws.UserID, &ws.Name, &ws.Description, &ws.Status, &ws.ContainerID, &ws.VolumeName, &ws.CreatedAt, &ws.UpdatedAt); err != nil {
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
		`UPDATE workspaces SET name = ?, description = ?, status = ?, container_id = ?, volume_name = ?, updated_at = ? WHERE id = ?`,
		ws.Name, ws.Description, ws.Status, ws.ContainerID, ws.VolumeName, now, ws.ID,
	)
	if err != nil {
		return fmt.Errorf("updating workspace: %w", err)
	}
	ws.UpdatedAt = now
	return nil
}

func (r *repository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting workspace: %w", err)
	}
	return nil
}
