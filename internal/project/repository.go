package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
)

// Repository defines data access for projects.
type Repository interface {
	Create(ctx context.Context, p *Project) error
	GetByID(ctx context.Context, id int64) (*Project, error)
	ListByUserID(ctx context.Context, userID int64) ([]*Project, error)
	Update(ctx context.Context, p *Project) error
	Delete(ctx context.Context, id int64) error
	Overviews(ctx context.Context, userID int64) ([]*Overview, error)
	WorkspaceLinks(ctx context.Context, userID int64) ([]*WorkspaceLink, error)
	// CreateConverting inserts a project being made from a workspace
	// together with the record of its conversion.
	CreateConverting(ctx context.Context, p *Project, c *Conversion) error
	// GetConversion returns a project's conversion record, or nil.
	GetConversion(ctx context.Context, projectID int64) (*Conversion, error)
	UpdateConversion(ctx context.Context, c *Conversion) error
	// UpdateWithConversion updates a project and its conversion record in
	// one transaction, so a project is never ready while its record still
	// says the conversion can be undone.
	UpdateWithConversion(ctx context.Context, p *Project, c *Conversion) error
	// UnfinishedConversions lists the conversions still to be finished.
	UnfinishedConversions(ctx context.Context) ([]*Conversion, error)
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new project Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

const projectColumns = `id, user_id, name, description, default_branch, upstream_url, setup_script, max_sandboxes, status, error, repo_container_id, repo_volume_name, repo_network_name, repo_agent_token, created_at, updated_at`

// execer is a database or a transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (r *repository) Create(ctx context.Context, p *Project) error {
	return insertProject(ctx, r.db, p)
}

func insertProject(ctx context.Context, db execer, p *Project) error {
	now := time.Now()
	result, err := db.ExecContext(ctx,
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
	return updateProject(ctx, r.db, p)
}

func updateProject(ctx context.Context, db execer, p *Project) error {
	now := time.Now()
	_, err := db.ExecContext(ctx,
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

func (r *repository) CreateConverting(ctx context.Context, p *Project, c *Conversion) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	if err := insertProject(ctx, tx, p); err != nil {
		return err
	}
	c.ProjectID, c.UserID = p.ID, p.UserID
	stripped, sshKept, err := conversionLists(c)
	if err != nil {
		return err
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO project_conversions (project_id, user_id, workspace_id, in_place, init_sha, state, stripped, ssh_kept, restart_pending, restart_error, rotation_pending, key_rotated, public_key, rotation_error, dismissed, old_key_fingerprint, progress_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ProjectID, c.UserID, c.WorkspaceID, c.InPlace, c.InitSHA, c.State, stripped, sshKept, c.RestartPending, c.RestartError, c.RotationPending, c.KeyRotated, c.PublicKey, c.RotationError, c.Dismissed, c.OldKeyFingerprint, c.ProgressError, now, now,
	); err != nil {
		return fmt.Errorf("inserting conversion: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing project: %w", err)
	}
	c.CreatedAt, c.UpdatedAt = now, now
	return nil
}

const conversionColumns = `project_id, user_id, workspace_id, in_place, init_sha, state, stripped, ssh_kept, restart_pending, restart_error, rotation_pending, key_rotated, public_key, rotation_error, dismissed, old_key_fingerprint, progress_error, created_at, updated_at`

func (r *repository) GetConversion(ctx context.Context, projectID int64) (*Conversion, error) {
	c, err := scanConversion(r.db.QueryRowContext(ctx, `SELECT `+conversionColumns+` FROM project_conversions WHERE project_id = ?`, projectID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying conversion: %w", err)
	}
	return c, nil
}

func (r *repository) UpdateConversion(ctx context.Context, c *Conversion) error {
	return updateConversion(ctx, r.db, c)
}

func updateConversion(ctx context.Context, db execer, c *Conversion) error {
	stripped, sshKept, err := conversionLists(c)
	if err != nil {
		return err
	}
	now := time.Now()
	if _, err := db.ExecContext(ctx,
		`UPDATE project_conversions SET init_sha = ?, state = ?, stripped = ?, ssh_kept = ?, restart_pending = ?, restart_error = ?, rotation_pending = ?, key_rotated = ?, public_key = ?, rotation_error = ?, dismissed = ?, old_key_fingerprint = ?, progress_error = ?, updated_at = ? WHERE project_id = ?`,
		c.InitSHA, c.State, stripped, sshKept, c.RestartPending, c.RestartError, c.RotationPending, c.KeyRotated, c.PublicKey, c.RotationError, c.Dismissed, c.OldKeyFingerprint, c.ProgressError, now, c.ProjectID,
	); err != nil {
		return fmt.Errorf("updating conversion: %w", err)
	}
	c.UpdatedAt = now
	return nil
}

func (r *repository) UpdateWithConversion(ctx context.Context, p *Project, c *Conversion) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	if err := updateProject(ctx, tx, p); err != nil {
		return err
	}
	if err := updateConversion(ctx, tx, c); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing project and conversion: %w", err)
	}
	return nil
}

func (r *repository) UnfinishedConversions(ctx context.Context) ([]*Conversion, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+conversionColumns+` FROM project_conversions WHERE state != ? ORDER BY project_id`, ConversionDone)
	if err != nil {
		return nil, fmt.Errorf("listing conversions: %w", err)
	}
	defer rows.Close()
	var conversions []*Conversion
	for rows.Next() {
		c, err := scanConversion(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning conversion: %w", err)
		}
		conversions = append(conversions, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating conversions: %w", err)
	}
	return conversions, nil
}

// conversionLists encodes a conversion's lists as JSON. Stripped
// credentials keep their kind and location, never a value.
func conversionLists(c *Conversion) (stripped, sshKept string, err error) {
	items := c.Stripped
	if items == nil {
		items = []agent.CredentialItem{}
	}
	kept := c.SSHKept
	if kept == nil {
		kept = []string{}
	}
	a, err := json.Marshal(items)
	if err != nil {
		return "", "", fmt.Errorf("encoding stripped credentials: %w", err)
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return "", "", fmt.Errorf("encoding kept SSH files: %w", err)
	}
	return string(a), string(b), nil
}

func scanConversion(row rowScanner) (*Conversion, error) {
	c := &Conversion{}
	var stripped, sshKept string
	err := row.Scan(&c.ProjectID, &c.UserID, &c.WorkspaceID, &c.InPlace, &c.InitSHA, &c.State, &stripped, &sshKept,
		&c.RestartPending, &c.RestartError, &c.RotationPending, &c.KeyRotated, &c.PublicKey, &c.RotationError, &c.Dismissed,
		&c.OldKeyFingerprint, &c.ProgressError, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(stripped), &c.Stripped); err != nil {
		return nil, fmt.Errorf("decoding stripped credentials: %w", err)
	}
	if err := json.Unmarshal([]byte(sshKept), &c.SSHKept); err != nil {
		return nil, fmt.Errorf("decoding kept SSH files: %w", err)
	}
	return c, nil
}
