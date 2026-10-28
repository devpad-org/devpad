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
	// GetSetup returns the latest setup run in a workspace, or nil.
	GetSetup(ctx context.Context, workspaceID int64) (*Setup, error)
	// PutSetup records a new setup run in a workspace, replacing the last.
	PutSetup(ctx context.Context, st *Setup) error
	// FinishSetup records how a run ended, unless a newer run replaced it.
	FinishSetup(ctx context.Context, st *Setup) error
	// RunningSetups lists the setup runs not yet recorded as ended.
	RunningSetups(ctx context.Context) ([]*Setup, error)
	// SetEnvironmentImage records the last image a project's environment
	// built. Update never writes it, so a build ending meanwhile is kept.
	SetEnvironmentImage(ctx context.Context, projectID int64, image string) error
	// SetBranches records a project's default branch and the other branches
	// it protects. Update never writes them, so a stale copy of a project
	// cannot undo a change.
	SetBranches(ctx context.Context, projectID int64, defaultBranch string, protected []string) error
	// SetUpstreamURL records a project's upstream; Update never writes it,
	// for the same reason.
	SetUpstreamURL(ctx context.Context, projectID int64, upstreamURL string) error
	// ListWithEnvironment lists the projects that have an environment.
	ListWithEnvironment(ctx context.Context) ([]*Project, error)
	// GetBuild returns the latest build of a project's environment, or nil.
	GetBuild(ctx context.Context, projectID int64) (*Build, error)
	// PutBuild records a new build, replacing the last.
	PutBuild(ctx context.Context, b *Build) error
	// FinishBuild records how a build ended, reporting false when a newer
	// build replaced it.
	FinishBuild(ctx context.Context, b *Build) (bool, error)
	DeleteBuild(ctx context.Context, projectID int64) error
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new project Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

const projectColumns = `id, user_id, name, description, default_branch, protected_branches, upstream_url, setup_script, environment, environment_image, max_sandboxes, auto_review, status, error, repo_container_id, repo_volume_name, repo_network_name, repo_agent_token, created_at, updated_at`

// execer is a database or a transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (r *repository) Create(ctx context.Context, p *Project) error {
	return insertProject(ctx, r.db, p)
}

func insertProject(ctx context.Context, db execer, p *Project) error {
	protected, err := branchList(p.ProtectedBranches)
	if err != nil {
		return err
	}
	now := time.Now()
	result, err := db.ExecContext(ctx,
		`INSERT INTO projects (user_id, name, description, default_branch, protected_branches, upstream_url, setup_script, environment, max_sandboxes, auto_review, status, error, repo_container_id, repo_volume_name, repo_network_name, repo_agent_token, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.UserID, p.Name, p.Description, p.DefaultBranch, protected, p.UpstreamURL, p.SetupScript, p.Environment, p.MaxSandboxes, p.AutoReview, p.Status, p.Error, p.RepoContainerID, p.RepoVolumeName, p.RepoNetworkName, p.RepoAgentToken, now, now,
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
		`UPDATE projects SET name = ?, description = ?, setup_script = ?, environment = ?, max_sandboxes = ?, auto_review = ?, status = ?, error = ?, repo_container_id = ?, repo_volume_name = ?, repo_network_name = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Description, p.SetupScript, p.Environment, p.MaxSandboxes, p.AutoReview, p.Status, p.Error, p.RepoContainerID, p.RepoVolumeName, p.RepoNetworkName, now, p.ID,
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
	var protected string
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.DefaultBranch, &protected, &p.UpstreamURL, &p.SetupScript, &p.Environment, &p.EnvironmentImage, &p.MaxSandboxes, &p.AutoReview, &p.Status, &p.Error, &p.RepoContainerID, &p.RepoVolumeName, &p.RepoNetworkName, &p.RepoAgentToken, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(protected), &p.ProtectedBranches); err != nil {
		return nil, fmt.Errorf("decoding protected branches: %w", err)
	}
	return p, nil
}

// branchList encodes a list of branches for a JSON column.
func branchList(branches []string) (string, error) {
	if branches == nil {
		branches = []string{}
	}
	b, err := json.Marshal(branches)
	if err != nil {
		return "", fmt.Errorf("encoding branches: %w", err)
	}
	return string(b), nil
}

func (r *repository) SetBranches(ctx context.Context, projectID int64, defaultBranch string, protected []string) error {
	list, err := branchList(protected)
	if err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE projects SET default_branch = ?, protected_branches = ?, updated_at = ? WHERE id = ?`, defaultBranch, list, time.Now(), projectID); err != nil {
		return fmt.Errorf("recording branches: %w", err)
	}
	return nil
}

func (r *repository) SetUpstreamURL(ctx context.Context, projectID int64, upstreamURL string) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE projects SET upstream_url = ?, updated_at = ? WHERE id = ?`, upstreamURL, time.Now(), projectID); err != nil {
		return fmt.Errorf("recording upstream: %w", err)
	}
	return nil
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

const setupColumns = `workspace_id, user_id, script, status, command_id, exit_code, output, error, started_at, finished_at`

func (r *repository) GetSetup(ctx context.Context, workspaceID int64) (*Setup, error) {
	st, err := scanSetup(r.db.QueryRowContext(ctx, `SELECT `+setupColumns+` FROM workspace_setups WHERE workspace_id = ?`, workspaceID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying setup run: %w", err)
	}
	return st, nil
}

func (r *repository) PutSetup(ctx context.Context, st *Setup) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO workspace_setups (`+setupColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(workspace_id) DO UPDATE SET user_id = excluded.user_id, script = excluded.script, status = excluded.status,
		   command_id = excluded.command_id, exit_code = excluded.exit_code, output = excluded.output, error = excluded.error,
		   started_at = excluded.started_at, finished_at = excluded.finished_at`,
		st.WorkspaceID, st.UserID, st.Script, st.Status, st.CommandID, nullableInt(st.ExitCode), st.Output, st.Error, st.StartedAt, nullableTime(st.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("recording setup run: %w", err)
	}
	return nil
}

func (r *repository) FinishSetup(ctx context.Context, st *Setup) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE workspace_setups SET status = ?, exit_code = ?, output = ?, error = ?, finished_at = ?
		 WHERE workspace_id = ? AND command_id = ? AND status = ?`,
		st.Status, nullableInt(st.ExitCode), st.Output, st.Error, nullableTime(st.FinishedAt),
		st.WorkspaceID, st.CommandID, SetupRunning,
	)
	if err != nil {
		return fmt.Errorf("recording end of setup run: %w", err)
	}
	return nil
}

func (r *repository) RunningSetups(ctx context.Context) ([]*Setup, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+setupColumns+` FROM workspace_setups WHERE status = ? ORDER BY workspace_id`, SetupRunning)
	if err != nil {
		return nil, fmt.Errorf("listing running setup runs: %w", err)
	}
	defer rows.Close()
	var setups []*Setup
	for rows.Next() {
		st, err := scanSetup(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning setup run: %w", err)
		}
		setups = append(setups, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating setup runs: %w", err)
	}
	return setups, nil
}

func scanSetup(row rowScanner) (*Setup, error) {
	st := &Setup{}
	var exitCode sql.NullInt64
	var finishedAt sql.NullTime
	err := row.Scan(&st.WorkspaceID, &st.UserID, &st.Script, &st.Status, &st.CommandID, &exitCode, &st.Output, &st.Error, &st.StartedAt, &finishedAt)
	if err != nil {
		return nil, err
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		st.ExitCode = &code
	}
	if finishedAt.Valid {
		st.FinishedAt = &finishedAt.Time
	}
	return st, nil
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableTime(v *time.Time) any {
	if v == nil {
		return nil
	}
	return *v
}

func (r *repository) SetEnvironmentImage(ctx context.Context, projectID int64, image string) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE projects SET environment_image = ? WHERE id = ?`, image, projectID); err != nil {
		return fmt.Errorf("recording environment image: %w", err)
	}
	return nil
}

func (r *repository) ListWithEnvironment(ctx context.Context) ([]*Project, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE TRIM(environment) != '' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing projects with an environment: %w", err)
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

const buildColumns = `project_id, build_id, hash, image, status, log, error, started_at, finished_at`

func (r *repository) GetBuild(ctx context.Context, projectID int64) (*Build, error) {
	b := &Build{}
	var finishedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT `+buildColumns+` FROM project_builds WHERE project_id = ?`, projectID).
		Scan(&b.ProjectID, &b.ID, &b.Hash, &b.Image, &b.Status, &b.Log, &b.Error, &b.StartedAt, &finishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying environment build: %w", err)
	}
	if finishedAt.Valid {
		b.FinishedAt = &finishedAt.Time
	}
	return b, nil
}

func (r *repository) PutBuild(ctx context.Context, b *Build) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO project_builds (`+buildColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id) DO UPDATE SET build_id = excluded.build_id, hash = excluded.hash, image = excluded.image, status = excluded.status,
		   log = excluded.log, error = excluded.error, started_at = excluded.started_at, finished_at = excluded.finished_at`,
		b.ProjectID, b.ID, b.Hash, b.Image, b.Status, b.Log, b.Error, b.StartedAt, nullableTime(b.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("recording environment build: %w", err)
	}
	return nil
}

func (r *repository) FinishBuild(ctx context.Context, b *Build) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE project_builds SET status = ?, log = ?, error = ?, finished_at = ?
		 WHERE project_id = ? AND build_id = ? AND status = ?`,
		b.Status, b.Log, b.Error, nullableTime(b.FinishedAt), b.ProjectID, b.ID, BuildRunning,
	)
	if err != nil {
		return false, fmt.Errorf("recording end of environment build: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("recording end of environment build: %w", err)
	}
	return n == 1, nil
}

func (r *repository) DeleteBuild(ctx context.Context, projectID int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM project_builds WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("deleting environment build: %w", err)
	}
	return nil
}
