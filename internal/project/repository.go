package project

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Repository defines data access for projects.
type Repository interface {
	Create(ctx context.Context, p *Project) error
	GetByID(ctx context.Context, id int64) (*Project, error)
	ListByUserID(ctx context.Context, userID int64) ([]*Project, error)
	Update(ctx context.Context, p *Project) error
	Delete(ctx context.Context, id int64) error
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new project Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

const projectColumns = `id, user_id, name, description, default_branch, upstream_url, setup_script, max_sandboxes, status, error, repo_container_id, repo_volume_name, repo_network_name, repo_agent_token, created_at, updated_at`

func (r *repository) Create(ctx context.Context, p *Project) error {
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO projects (user_id, name, description, default_branch, upstream_url, setup_script, max_sandboxes, status, error, repo_container_id, repo_volume_name, repo_network_name, repo_agent_token, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.UserID, p.Name, p.Description, p.DefaultBranch, p.UpstreamURL, p.SetupScript, p.MaxSandboxes, p.Status, p.Error, p.RepoContainerID, p.RepoVolumeName, p.RepoNetworkName, p.RepoAgentToken, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting project: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}
	p.ID = id
	p.CreatedAt = now
	p.UpdatedAt = now
	return nil
}

func (r *repository) GetByID(ctx context.Context, id int64) (*Project, error) {
	p, err := scanProject(r.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying project by id: %w", err)
	}
	return p, nil
}

func (r *repository) ListByUserID(ctx context.Context, userID int64) ([]*Project, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning project: %w", err)
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating projects: %w", err)
	}
	return projects, nil
}

func (r *repository) Update(ctx context.Context, p *Project) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE projects SET name = ?, description = ?, setup_script = ?, max_sandboxes = ?, status = ?, error = ?, repo_container_id = ?, repo_volume_name = ?, repo_network_name = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Description, p.SetupScript, p.MaxSandboxes, p.Status, p.Error, p.RepoContainerID, p.RepoVolumeName, p.RepoNetworkName, now, p.ID,
	)
	if err != nil {
		return fmt.Errorf("updating project: %w", err)
	}
	p.UpdatedAt = now
	return nil
}

func (r *repository) Delete(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting project: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProject(row rowScanner) (*Project, error) {
	p := &Project{}
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.DefaultBranch, &p.UpstreamURL, &p.SetupScript, &p.MaxSandboxes, &p.Status, &p.Error, &p.RepoContainerID, &p.RepoVolumeName, &p.RepoNetworkName, &p.RepoAgentToken, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}
