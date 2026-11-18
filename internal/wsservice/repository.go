package wsservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/database"
)

// Repository defines data access for workspace services.
type Repository interface {
	// Create adds a service to a workspace, returning ErrDuplicateType when
	// the workspace already has one of its type.
	Create(ctx context.Context, svc *WorkspaceService) error
	GetByID(ctx context.Context, id int64) (*WorkspaceService, error)
	ListByWorkspaceID(ctx context.Context, workspaceID int64) ([]*WorkspaceService, error)
	Update(ctx context.Context, svc *WorkspaceService) error
	Delete(ctx context.Context, id int64) error

	ListProjectServices(ctx context.Context, projectID int64) ([]*ProjectService, error)
	// AddProjectService adds a service to a project's list, returning
	// ErrDuplicateType when the project already has one of its type.
	AddProjectService(ctx context.Context, ps *ProjectService) error
	// RemoveProjectService removes a service from a project's list,
	// returning ErrNotFound when it has none of that type.
	RemoveProjectService(ctx context.Context, projectID int64, serviceType ServiceType) error
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
	if database.IsUniqueViolation(err) {
		return ErrDuplicateType
	}
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

func (r *repository) ListProjectServices(ctx context.Context, projectID int64) ([]*ProjectService, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT project_id, service_type, license_acceptance, created_at FROM project_services WHERE project_id = ? ORDER BY created_at ASC, service_type ASC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing project services: %w", err)
	}
	defer rows.Close()

	var services []*ProjectService
	for rows.Next() {
		ps := &ProjectService{}
		var acceptance sql.NullString
		if err := rows.Scan(&ps.ProjectID, &ps.ServiceType, &acceptance, &ps.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning project service: %w", err)
		}
		if acceptance.Valid && acceptance.String != "" {
			ps.LicenseAcceptance = &LicenseAcceptance{}
			if err := json.Unmarshal([]byte(acceptance.String), ps.LicenseAcceptance); err != nil {
				return nil, fmt.Errorf("reading the licence acceptance of project %d's %s: %w", ps.ProjectID, ps.ServiceType, err)
			}
		}
		services = append(services, ps)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating project services: %w", err)
	}
	return services, nil
}

func (r *repository) AddProjectService(ctx context.Context, ps *ProjectService) error {
	var acceptance any
	if ps.LicenseAcceptance != nil {
		b, err := json.Marshal(ps.LicenseAcceptance)
		if err != nil {
			return fmt.Errorf("encoding licence acceptance: %w", err)
		}
		acceptance = string(b)
	}
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO project_services (project_id, service_type, license_acceptance, created_at) VALUES (?, ?, ?, ?)`,
		ps.ProjectID, ps.ServiceType, acceptance, now,
	)
	if database.IsUniqueViolation(err) {
		return ErrDuplicateType
	}
	if err != nil {
		return fmt.Errorf("inserting project service: %w", err)
	}
	ps.CreatedAt = now
	return nil
}

func (r *repository) RemoveProjectService(ctx context.Context, projectID int64, serviceType ServiceType) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM project_services WHERE project_id = ? AND service_type = ?`, projectID, serviceType)
	if err != nil {
		return fmt.Errorf("deleting project service: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("deleting project service: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
