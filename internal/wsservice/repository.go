package wsservice

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Repository defines data access for workspace services.
type Repository interface {
	Create(ctx context.Context, svc *WorkspaceService) error
	GetByID(ctx context.Context, id int64) (*WorkspaceService, error)
	ListByWorkspaceID(ctx context.Context, workspaceID int64) ([]*WorkspaceService, error)
	Update(ctx context.Context, svc *WorkspaceService) error
	Delete(ctx context.Context, id int64) error
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new workspace service Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, svc *WorkspaceService) error {
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO workspace_services (workspace_id, service_type, container_id, volume_name, status, config, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		svc.WorkspaceID, svc.ServiceType, svc.ContainerID, svc.VolumeName, svc.Status, svc.Config, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting workspace service: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}

	svc.ID = id
	svc.CreatedAt = now
	svc.UpdatedAt = now
	return nil
}

func (r *repository) GetByID(ctx context.Context, id int64) (*WorkspaceService, error) {
	svc := &WorkspaceService{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, service_type, container_id, volume_name, status, config, created_at, updated_at FROM workspace_services WHERE id = ?`,
		id,
	).Scan(&svc.ID, &svc.WorkspaceID, &svc.ServiceType, &svc.ContainerID, &svc.VolumeName, &svc.Status, &svc.Config, &svc.CreatedAt, &svc.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying workspace service by id: %w", err)
	}
	return svc, nil
}

func (r *repository) ListByWorkspaceID(ctx context.Context, workspaceID int64) ([]*WorkspaceService, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, workspace_id, service_type, container_id, volume_name, status, config, created_at, updated_at FROM workspace_services WHERE workspace_id = ? ORDER BY created_at ASC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing workspace services: %w", err)
	}
	defer rows.Close()

	var services []*WorkspaceService
	for rows.Next() {
		svc := &WorkspaceService{}
		if err := rows.Scan(&svc.ID, &svc.WorkspaceID, &svc.ServiceType, &svc.ContainerID, &svc.VolumeName, &svc.Status, &svc.Config, &svc.CreatedAt, &svc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning workspace service: %w", err)
		}
		services = append(services, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating workspace services: %w", err)
	}
	return services, nil
}

func (r *repository) Update(ctx context.Context, svc *WorkspaceService) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE workspace_services SET container_id = ?, volume_name = ?, status = ?, config = ?, updated_at = ? WHERE id = ?`,
		svc.ContainerID, svc.VolumeName, svc.Status, svc.Config, now, svc.ID,
	)
	if err != nil {
		return fmt.Errorf("updating workspace service: %w", err)
	}
	svc.UpdatedAt = now
	return nil
}

func (r *repository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM workspace_services WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting workspace service: %w", err)
	}
	return nil
}
