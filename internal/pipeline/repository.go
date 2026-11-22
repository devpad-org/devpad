package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Repository is the record of a project's pipelines and their jobs.
type Repository interface {
	// Create inserts a pipeline with its jobs, filling in their ids.
	Create(ctx context.Context, p *Pipeline) error
	// Get returns a pipeline with its jobs, scripts included, or nil.
	Get(ctx context.Context, id int64) (*Pipeline, error)
	// List lists a project's pipelines, newest first, with their jobs but
	// not their scripts.
	List(ctx context.Context, projectID int64, f ListFilter) ([]*Pipeline, error)
	// FindSame returns the newest pipeline that tests what p tests, the
	// same subject, commits and jobs, and stands at one of statuses, with
	// its jobs and their scripts, or nil.
	FindSame(ctx context.Context, p *Pipeline, statuses []Status) (*Pipeline, error)

	// MarkStarted records that a queued pipeline started on image,
	// reporting false when it was no longer queued.
	MarkStarted(ctx context.Context, id int64, image string, at time.Time) (bool, error)
	// SetRefPinned records whether the canonical repo may hold the
	// pipeline's ref.
	SetRefPinned(ctx context.Context, id int64, pinned bool) error
	// SetTested records the commit the pipeline's jobs got.
	SetTested(ctx context.Context, id int64, sha string) error
	// Finish records how an unfinished pipeline ended: its status, error,
	// conflicting paths and finish time. Every job still queued is skipped.
	// It reports false when the pipeline had already finished.
	Finish(ctx context.Context, p *Pipeline) (bool, error)

	// StartJob records that a queued job started.
	StartJob(ctx context.Context, jobID int64, at time.Time) error
	// FinishJob records how a job ended, with the end of its output and how
	// much it printed in all.
	FinishJob(ctx context.Context, j *JobRun, output []byte) error
	// SaveJobOutput records the end of a job's output and how much it
	// printed in all, leaving its status as it is.
	SaveJobOutput(ctx context.Context, jobID int64, output []byte, total int64) error
	// JobOutput returns a pipeline's job with what is kept of its output,
	// or nil when the pipeline has no such job.
	JobOutput(ctx context.Context, pipelineID, jobID int64) (*JobRun, []byte, error)
	// PruneOutputs clears the output of every job of the project's
	// pipelines but its keep newest.
	PruneOutputs(ctx context.Context, projectID int64, keep int) error

	// InterruptUnfinished marks every queued or running pipeline
	// interrupted, with reason, its running jobs interrupted and its other
	// unfinished jobs skipped, and returns those pipelines as they now are.
	InterruptUnfinished(ctx context.Context, reason string, at time.Time) ([]*Pipeline, error)
	// StatusesByUID returns the status of each pipeline of uids there is.
	StatusesByUID(ctx context.Context, uids []string) (map[string]Status, error)
	// ListPinnedFinished lists the finished pipelines whose ref the
	// canonical repo may still hold.
	ListPinnedFinished(ctx context.Context) ([]*Pipeline, error)
}

// ListFilter narrows a project's pipelines.
type ListFilter struct {
	// Branch keeps only the pipelines of this branch's commits.
	Branch string
	// MergeRequestID keeps only this merge request's pipelines.
	MergeRequestID int64
	// Before keeps only pipelines older than this one, to page back.
	Before int64
	// Limit is how many to list: DefaultListLimit when zero, at most
	// MaxListLimit.
	Limit int
}

const (
	DefaultListLimit = 50
	MaxListLimit     = 100
)

type repository struct {
	db *sql.DB
}

// NewRepository creates a pipeline Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

const pipelineColumns = `id, project_id, user_id, uid, kind, branch, merge_request_id, source_branch, target_branch, head_sha, target_sha, tested_sha, config_source, config_path, config_hash, image, status, error, conflict_paths, trigger, rerun_of, ref_pinned, created_at, started_at, finished_at`

func (r *repository) Create(ctx context.Context, p *Pipeline) error {
	paths, err := jsonList(p.ConflictPaths)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO pipelines (project_id, user_id, uid, kind, branch, merge_request_id, source_branch, target_branch, head_sha, target_sha, tested_sha, config_source, config_path, config_hash, image, status, error, conflict_paths, trigger, rerun_of, ref_pinned, created_at, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ProjectID, p.UserID, p.UID, p.Kind, p.Branch, nullableID(p.MergeRequestID), p.SourceBranch, p.TargetBranch, p.HeadSHA, p.TargetSHA, p.TestedSHA, p.ConfigSource, p.ConfigPath, p.ConfigHash, p.Image, p.Status, p.Error, paths, p.Trigger, nullableID(p.RerunOf), p.RefPinned, p.CreatedAt, p.StartedAt, p.FinishedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting pipeline: %w", err)
	}
	if p.ID, err = res.LastInsertId(); err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}
	for _, j := range p.Jobs {
		services, err := jsonList(j.Services)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO pipeline_jobs (pipeline_id, position, name, run, timeout_ms, services, status, exit_code, error, started_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, j.Position, j.Name, j.Run, j.Timeout.Milliseconds(), services, j.Status, j.ExitCode, j.Error, j.StartedAt, j.FinishedAt,
		)
		if err != nil {
			return fmt.Errorf("inserting job %s: %w", j.Name, err)
		}
		if j.ID, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("getting inserted id: %w", err)
		}
		j.PipelineID = p.ID
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing pipeline: %w", err)
	}
	return nil
}

func (r *repository) Get(ctx context.Context, id int64) (*Pipeline, error) {
	return r.one(ctx, `SELECT `+pipelineColumns+` FROM pipelines WHERE id = ?`, id)
}

func (r *repository) List(ctx context.Context, projectID int64, f ListFilter) ([]*Pipeline, error) {
	query := `SELECT ` + pipelineColumns + ` FROM pipelines WHERE project_id = ?`
	args := []any{projectID}
	if f.Branch != "" {
		query += ` AND kind = 'branch' AND branch = ?`
		args = append(args, f.Branch)
	}
	if f.MergeRequestID != 0 {
		query += ` AND merge_request_id = ?`
		args = append(args, f.MergeRequestID)
	}
	if f.Before != 0 {
		query += ` AND id < ?`
		args = append(args, f.Before)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, min(limit, MaxListLimit))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing pipelines: %w", err)
	}
	pipelines, err := collect(rows, scanPipeline)
	if err != nil {
		return nil, err
	}
	if err := r.loadJobs(ctx, pipelines, false); err != nil {
		return nil, err
	}
	return pipelines, nil
}

func (r *repository) FindSame(ctx context.Context, p *Pipeline, statuses []Status) (*Pipeline, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	query, args := findSameQuery(p, statuses)
	return r.one(ctx, query, args...)
}

// findSameQuery selects the newest pipeline testing what p tests at one of
// statuses: through the index of a branch's commits, or of a merge
// request's pairs of commits, so it reads only pipelines of those commits
// however many the project keeps.
func findSameQuery(p *Pipeline, statuses []Status) (string, []any) {
	var query string
	var args []any
	if p.Kind == KindMergeRequest {
		query = `SELECT ` + pipelineColumns + ` FROM pipelines
			WHERE merge_request_id = ? AND head_sha = ? AND target_sha = ? AND project_id = ? AND config_hash = ?`
		args = []any{p.MergeRequestID, p.HeadSHA, p.TargetSHA, p.ProjectID, p.ConfigHash}
	} else {
		query = `SELECT ` + pipelineColumns + ` FROM pipelines
			WHERE project_id = ? AND kind = 'branch' AND branch = ? AND head_sha = ? AND config_hash = ?`
		args = []any{p.ProjectID, p.Branch, p.HeadSHA, p.ConfigHash}
	}
	query += ` AND status IN (` + placeholders(len(statuses)) + `) ORDER BY id DESC LIMIT 1`
	for _, s := range statuses {
		args = append(args, s)
	}
	return query, args
}

// one reads the pipeline a query selects, with its jobs and their scripts,
// or nil.
func (r *repository) one(ctx context.Context, query string, args ...any) (*Pipeline, error) {
	p, err := oneOrNil(scanPipeline(r.db.QueryRowContext(ctx, query, args...)))
	if err != nil || p == nil {
		return nil, err
	}
	if err := r.loadJobs(ctx, []*Pipeline{p}, true); err != nil {
		return nil, err
	}
	return p, nil
}

func (r *repository) MarkStarted(ctx context.Context, id int64, image string, at time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE pipelines SET status = ?, image = ?, started_at = ? WHERE id = ? AND status = ?`,
		StatusRunning, image, at, id, StatusQueued)
	if err != nil {
		return false, fmt.Errorf("starting pipeline: %w", err)
	}
	return affected(res)
}

func (r *repository) SetRefPinned(ctx context.Context, id int64, pinned bool) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE pipelines SET ref_pinned = ? WHERE id = ?`, pinned, id); err != nil {
		return fmt.Errorf("recording the pipeline's ref: %w", err)
	}
	return nil
}

func (r *repository) SetTested(ctx context.Context, id int64, sha string) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE pipelines SET tested_sha = ? WHERE id = ?`, sha, id); err != nil {
		return fmt.Errorf("recording the tested commit: %w", err)
	}
	return nil
}

func (r *repository) Finish(ctx context.Context, p *Pipeline) (bool, error) {
	paths, err := jsonList(p.ConflictPaths)
	if err != nil {
		return false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE pipelines SET status = ?, error = ?, conflict_paths = ?, finished_at = ? WHERE id = ? AND status IN (?, ?)`,
		p.Status, p.Error, paths, p.FinishedAt, p.ID, StatusQueued, StatusRunning)
	if err != nil {
		return false, fmt.Errorf("finishing pipeline: %w", err)
	}
	if ok, err := affected(res); err != nil || !ok {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_jobs SET status = ?, finished_at = ? WHERE pipeline_id = ? AND status = ?`,
		JobSkipped, p.FinishedAt, p.ID, JobQueued); err != nil {
		return false, fmt.Errorf("skipping jobs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing pipeline: %w", err)
	}
	return true, nil
}

func (r *repository) StartJob(ctx context.Context, jobID int64, at time.Time) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE pipeline_jobs SET status = ?, started_at = ? WHERE id = ? AND status = ?`,
		JobRunning, at, jobID, JobQueued); err != nil {
		return fmt.Errorf("starting job: %w", err)
	}
	return nil
}

func (r *repository) FinishJob(ctx context.Context, j *JobRun, output []byte) error {
	if output == nil {
		output = []byte{}
	}
	if _, err := r.db.ExecContext(ctx,
		`UPDATE pipeline_jobs SET status = ?, exit_code = ?, error = ?, output = ?, output_bytes = ?, finished_at = ? WHERE id = ?`,
		j.Status, j.ExitCode, j.Error, output, j.OutputBytes, j.FinishedAt, j.ID); err != nil {
		return fmt.Errorf("finishing job: %w", err)
	}
	j.KeptBytes = int64(len(output))
	return nil
}

func (r *repository) SaveJobOutput(ctx context.Context, jobID int64, output []byte, total int64) error {
	if output == nil {
		output = []byte{}
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE pipeline_jobs SET output = ?, output_bytes = ? WHERE id = ?`, output, total, jobID); err != nil {
		return fmt.Errorf("keeping job output: %w", err)
	}
	return nil
}

func (r *repository) JobOutput(ctx context.Context, pipelineID, jobID int64) (*JobRun, []byte, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+jobColumns(false)+`, output FROM pipeline_jobs WHERE id = ? AND pipeline_id = ?`, jobID, pipelineID)
	var output []byte
	j, err := oneOrNil(scanJob(row, false, &output))
	if err != nil || j == nil {
		return nil, nil, err
	}
	return j, output, nil
}

func (r *repository) PruneOutputs(ctx context.Context, projectID int64, keep int) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE pipeline_jobs SET output = x'' WHERE length(output) > 0 AND pipeline_id IN (
			SELECT id FROM pipelines WHERE project_id = ? ORDER BY id DESC LIMIT -1 OFFSET ?)`,
		projectID, keep); err != nil {
		return fmt.Errorf("pruning pipeline output: %w", err)
	}
	return nil
}

func (r *repository) InterruptUnfinished(ctx context.Context, reason string, at time.Time) ([]*Pipeline, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+pipelineColumns+` FROM pipelines WHERE status IN (?, ?) ORDER BY id`, StatusQueued, StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("listing unfinished pipelines: %w", err)
	}
	pipelines, err := collect(rows, scanPipeline)
	if err != nil {
		return nil, err
	}
	for _, p := range pipelines {
		if _, err := tx.ExecContext(ctx,
			`UPDATE pipeline_jobs SET status = CASE status WHEN ? THEN ? ELSE ? END, finished_at = ? WHERE pipeline_id = ? AND status IN (?, ?)`,
			JobRunning, JobInterrupted, JobSkipped, at, p.ID, JobQueued, JobRunning); err != nil {
			return nil, fmt.Errorf("interrupting jobs: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pipelines SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
			StatusInterrupted, reason, at, p.ID); err != nil {
			return nil, fmt.Errorf("interrupting pipeline: %w", err)
		}
		finished := at
		p.Status, p.Error, p.FinishedAt = StatusInterrupted, reason, &finished
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing interruptions: %w", err)
	}
	return pipelines, nil
}

func (r *repository) StatusesByUID(ctx context.Context, uids []string) (map[string]Status, error) {
	statuses := make(map[string]Status, len(uids))
	// SQLite allows many more, but a sweep never needs one query to name
	// them all.
	const batch = 500
	for start := 0; start < len(uids); start += batch {
		part := uids[start:min(start+batch, len(uids))]
		args := make([]any, len(part))
		for i, uid := range part {
			args[i] = uid
		}
		rows, err := r.db.QueryContext(ctx, `SELECT uid, status FROM pipelines WHERE uid IN (`+placeholders(len(part))+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("reading pipeline statuses: %w", err)
		}
		for rows.Next() {
			var uid string
			var status Status
			if err := rows.Scan(&uid, &status); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scanning pipeline status: %w", err)
			}
			statuses[uid] = status
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("reading pipeline statuses: %w", err)
		}
	}
	return statuses, nil
}

func (r *repository) ListPinnedFinished(ctx context.Context) ([]*Pipeline, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+pipelineColumns+` FROM pipelines WHERE ref_pinned = 1 AND status NOT IN (?, ?) ORDER BY id`,
		StatusQueued, StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("listing pinned pipelines: %w", err)
	}
	return collect(rows, scanPipeline)
}

// jobColumns are a job's columns, with its script or without.
func jobColumns(withScript bool) string {
	run := `''`
	if withScript {
		run = `run`
	}
	return `id, pipeline_id, position, name, ` + run + `, timeout_ms, services, status, exit_code, error, output_bytes, length(output), started_at, finished_at`
}

// loadJobs reads the jobs of pipelines, in their file's order.
func (r *repository) loadJobs(ctx context.Context, pipelines []*Pipeline, withScripts bool) error {
	if len(pipelines) == 0 {
		return nil
	}
	byID := make(map[int64]*Pipeline, len(pipelines))
	args := make([]any, 0, len(pipelines))
	for _, p := range pipelines {
		byID[p.ID] = p
		p.Jobs = []*JobRun{}
		args = append(args, p.ID)
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobColumns(withScripts)+` FROM pipeline_jobs WHERE pipeline_id IN (`+placeholders(len(args))+`) ORDER BY pipeline_id, position`, args...)
	if err != nil {
		return fmt.Errorf("listing pipeline jobs: %w", err)
	}
	jobs, err := collect(rows, func(row rowScanner) (*JobRun, error) { return scanJob(row, withScripts) })
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if p := byID[j.PipelineID]; p != nil {
			p.Jobs = append(p.Jobs, j)
		}
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPipeline(row rowScanner) (*Pipeline, error) {
	var p Pipeline
	var mergeRequestID, rerunOf sql.NullInt64
	var paths string
	var started, finished sql.NullTime
	if err := row.Scan(&p.ID, &p.ProjectID, &p.UserID, &p.UID, &p.Kind, &p.Branch, &mergeRequestID, &p.SourceBranch, &p.TargetBranch, &p.HeadSHA, &p.TargetSHA, &p.TestedSHA, &p.ConfigSource, &p.ConfigPath, &p.ConfigHash, &p.Image, &p.Status, &p.Error, &paths, &p.Trigger, &rerunOf, &p.RefPinned, &p.CreatedAt, &started, &finished); err != nil {
		return nil, err
	}
	p.MergeRequestID, p.RerunOf = mergeRequestID.Int64, rerunOf.Int64
	if err := json.Unmarshal([]byte(paths), &p.ConflictPaths); err != nil {
		return nil, fmt.Errorf("decoding conflicting paths: %w", err)
	}
	p.StartedAt, p.FinishedAt = timePtr(started), timePtr(finished)
	return &p, nil
}

// scanJob reads a row of jobColumns, then any extra columns into extra.
func scanJob(row rowScanner, withScript bool, extra ...any) (*JobRun, error) {
	var j JobRun
	var timeoutMS int64
	var services string
	var exitCode sql.NullInt64
	var started, finished sql.NullTime
	dest := []any{&j.ID, &j.PipelineID, &j.Position, &j.Name, &j.Run, &timeoutMS, &services, &j.Status, &exitCode, &j.Error, &j.OutputBytes, &j.KeptBytes, &started, &finished}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	j.Timeout = time.Duration(timeoutMS) * time.Millisecond
	if err := json.Unmarshal([]byte(services), &j.Services); err != nil {
		return nil, fmt.Errorf("decoding job services: %w", err)
	}
	if len(j.Services) == 0 {
		j.Services = nil
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		j.ExitCode = &code
	}
	j.StartedAt, j.FinishedAt = timePtr(started), timePtr(finished)
	return &j, nil
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func oneOrNil[T any](v *T, err error) (*T, error) {
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying: %w", err)
	}
	return v, nil
}

func collect[T any](rows *sql.Rows, scan func(rowScanner) (*T, error)) ([]*T, error) {
	defer rows.Close()
	var out []*T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}
	return out, nil
}

func affected(res sql.Result) (bool, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("counting updated rows: %w", err)
	}
	return n > 0, nil
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// jsonList encodes a list of names for a JSON column, never as null.
func jsonList(items []string) (string, error) {
	if items == nil {
		items = []string{}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("encoding list: %w", err)
	}
	return string(b), nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
