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
	// Latest returns the newest pipeline of f's purpose that counts
	// (Status.Counts) of each branch and merge request of the project f
	// names that has one, with their jobs but not their scripts: a branch's
	// first, in f's order, then a merge request's.
	Latest(ctx context.Context, projectID int64, f LatestFilter) ([]*Pipeline, error)
	// FindSame returns the newest pipeline that tests what p tests, the
	// same subject and purpose, commits and jobs, and stands at one of
	// statuses, with its jobs and their scripts, or nil.
	FindSame(ctx context.Context, p *Pipeline, statuses []Status) (*Pipeline, error)
	// CountSame counts the pipelines that test what p tests, with p's
	// purpose, and stand at one of statuses.
	CountSame(ctx context.Context, p *Pipeline, statuses []Status) (int, error)

	// MarkStarted records that a queued pipeline started on image, and so
	// may keep its jobs' output, reporting false when it was no longer
	// queued.
	MarkStarted(ctx context.Context, id int64, image string, at time.Time) (bool, error)
	// SetRefPinned records whether the canonical repo may hold the
	// pipeline's ref.
	SetRefPinned(ctx context.Context, id int64, pinned bool) error
	// SetTested records the commit the pipeline's jobs got, and its tree.
	SetTested(ctx context.Context, id int64, sha, tree string) error
	// Finish records how an unfinished pipeline ended: its status, error,
	// conflicting paths and finish time. Every job still queued is skipped,
	// and every one still running ends with it: cancelled when the pipeline
	// was cancelled or superseded, with its error, and otherwise an error
	// (lostJob), so a finished pipeline has no unfinished job. It reports
	// false when the pipeline had already finished.
	Finish(ctx context.Context, p *Pipeline) (bool, error)
	// MarkAnnounced records that every listener was told a pipeline
	// finished.
	MarkAnnounced(ctx context.Context, id int64, at time.Time) error

	// StartJob records that a queued job started.
	StartJob(ctx context.Context, jobID int64, at time.Time) error
	// FinishJob records how a job ended, with what it told, setting its
	// OutputBytes and KeptBytes.
	FinishJob(ctx context.Context, j *JobRun, told JobLog) error
	// SaveJobLog records what a job told, leaving its status as it is.
	SaveJobLog(ctx context.Context, jobID int64, told JobLog) error
	// JobLog returns a pipeline's job with what is kept of what it told,
	// or nil when the pipeline has no such job.
	JobLog(ctx context.Context, pipelineID, jobID int64) (*JobRun, *JobLog, error)
	// JobTail is JobLog with only the last n bytes of what is kept of the
	// job's output, read so from the database.
	JobTail(ctx context.Context, pipelineID, jobID int64, n int) (*JobRun, *JobLog, error)
	// PruneOutputs clears the output of every job of the project's
	// finished pipelines but its keep newest that started.
	PruneOutputs(ctx context.Context, projectID int64, keep int) error

	// ListUnfinished lists the queued and running pipelines, without their
	// jobs.
	ListUnfinished(ctx context.Context) ([]*Pipeline, error)
	// Interrupt marks interrupted, with reason, each of pipelines that is
	// still unfinished, its running jobs interrupted and its other
	// unfinished jobs skipped, and returns those it marked as they now are.
	Interrupt(ctx context.Context, pipelines []*Pipeline, reason string, at time.Time) ([]*Pipeline, error)
	// ListUnannounced lists the finished pipelines not every listener has
	// been told of, oldest first.
	ListUnannounced(ctx context.Context) ([]int64, error)
	// RecordsByUID returns what the database has of each pipeline of uids
	// it has.
	RecordsByUID(ctx context.Context, uids []string) (map[string]UIDRecord, error)
	// ListPinnedFinished lists the finished pipelines whose ref the
	// canonical repo may still hold.
	ListPinnedFinished(ctx context.Context) ([]*Pipeline, error)
}

// UIDRecord is what the database has of a pipeline found by its UID.
type UIDRecord struct {
	ProjectID int64
	Status    Status
}

// lostJob is the error of a job whose pipeline finished while the job's
// record still had it running: devpad lost track of it, such as when it
// could not record how the job ended.
const lostJob = "devpad could not record how the job ended"

// ListFilter narrows a project's pipelines.
type ListFilter struct {
	// Branch keeps only the pipelines of this branch's commits.
	Branch string
	// MergeRequestID keeps only this merge request's pipelines.
	MergeRequestID int64
	// Purpose keeps only pipelines of this purpose; empty keeps all.
	Purpose Purpose
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

// LatestFilter names the branches and merge requests of a project whose
// newest pipeline of Purpose to read. Purpose is always named: left empty,
// it matches no pipeline.
type LatestFilter struct {
	Branches        []string
	MergeRequestIDs []int64
	Purpose         Purpose
}

// MaxLatestSubjects caps the branches and merge requests one LatestFilter
// names.
const MaxLatestSubjects = 100

type repository struct {
	db *sql.DB
}

// NewRepository creates a pipeline Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

const pipelineColumns = `id, project_id, user_id, uid, kind, purpose, branch, merge_request_id, source_branch, target_branch, head_sha, target_sha, tested_sha, tested_tree, config_source, config_path, config_hash, config_sha, image, status, error, conflict_paths, trigger, rerun_of, ref_pinned, created_at, started_at, finished_at, announced_at`

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
		`INSERT INTO pipelines (project_id, user_id, uid, kind, purpose, branch, merge_request_id, source_branch, target_branch, head_sha, target_sha, tested_sha, config_source, config_path, config_hash, config_sha, image, status, error, conflict_paths, trigger, rerun_of, ref_pinned, created_at, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ProjectID, p.UserID, p.UID, p.Kind, p.Purpose, p.Branch, nullableID(p.MergeRequestID), p.SourceBranch, p.TargetBranch, p.HeadSHA, p.TargetSHA, p.TestedSHA, p.ConfigSource, p.ConfigPath, p.ConfigHash, p.ConfigSHA, p.Image, p.Status, p.Error, paths, p.Trigger, nullableID(p.RerunOf), p.RefPinned, p.CreatedAt, p.StartedAt, p.FinishedAt,
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
	query, args := listQuery(projectID, f)
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

// listQuery selects a page of the project's pipelines f picks, newest first,
// through the index of the project's, the branch's or the merge request's
// pipelines.
func listQuery(projectID int64, f ListFilter) (string, []any) {
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
	if f.Purpose != "" {
		query += ` AND purpose = ?`
		args = append(args, f.Purpose)
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
	return query, append(args, min(limit, MaxListLimit))
}

func (r *repository) Latest(ctx context.Context, projectID int64, f LatestFilter) ([]*Pipeline, error) {
	query, args := latestQuery(projectID, f)
	if query == "" {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the latest pipelines: %w", err)
	}
	found, err := collect(rows, scanPipeline)
	if err != nil {
		return nil, err
	}
	if err := r.loadJobs(ctx, found, false); err != nil {
		return nil, err
	}
	branches := make(map[string]*Pipeline)
	mrs := make(map[int64]*Pipeline)
	for _, p := range found {
		if p.Kind == KindBranch {
			branches[p.Branch] = p
		} else {
			mrs[p.MergeRequestID] = p
		}
	}
	out := make([]*Pipeline, 0, len(found))
	for _, b := range f.Branches {
		if p := branches[b]; p != nil {
			out = append(out, p)
			delete(branches, b)
		}
	}
	for _, id := range f.MergeRequestIDs {
		if p := mrs[id]; p != nil {
			out = append(out, p)
			delete(mrs, id)
		}
	}
	return out, nil
}

// latestQuery selects, for each branch and merge request f names, its
// newest pipeline of f's purpose that counts (Status.Counts: unfinished, or
// finished with a result), so a cancelled, superseded, interrupted or
// erroring newest one never hides the last result, and a pipeline of
// another purpose is never taken for it. Each subject's is read backwards
// through the index of a branch's or a merge request's pipelines, stopping
// at the first that counts. A merge request's pipelines are all of its
// project, so the project is checked on the one found. It is empty when f
// names none.
func latestQuery(projectID int64, f LatestFilter) (string, []any) {
	counting := `p.purpose = ? AND p.status IN (` + placeholders(len(countingStatuses)) + `)`
	var parts []string
	args := []any{projectID}
	addStatuses := func() {
		args = append(args, f.Purpose)
		for _, st := range countingStatuses {
			args = append(args, st)
		}
	}
	if len(f.Branches) > 0 {
		parts = append(parts, `SELECT (SELECT p.id FROM pipelines p WHERE p.project_id = ? AND p.kind = 'branch' AND p.branch = s.column1 AND `+counting+` ORDER BY p.id DESC LIMIT 1) FROM (VALUES `+valueRows(len(f.Branches))+`) s`)
		args = append(args, projectID)
		addStatuses()
		for _, b := range f.Branches {
			args = append(args, b)
		}
	}
	if len(f.MergeRequestIDs) > 0 {
		parts = append(parts, `SELECT (SELECT p.id FROM pipelines p WHERE p.merge_request_id = s.column1 AND `+counting+` ORDER BY p.id DESC LIMIT 1) FROM (VALUES `+valueRows(len(f.MergeRequestIDs))+`) s`)
		addStatuses()
		for _, id := range f.MergeRequestIDs {
			args = append(args, id)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return `SELECT ` + pipelineColumns + ` FROM pipelines WHERE project_id = ? AND id IN (` + strings.Join(parts, ` UNION ALL `) + `)`, args
}

// valueRows is n one-column rows of placeholders for a VALUES list.
func valueRows(n int) string {
	return strings.TrimSuffix(strings.Repeat("(?), ", n), ", ")
}

func (r *repository) FindSame(ctx context.Context, p *Pipeline, statuses []Status) (*Pipeline, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	query, args := findSameQuery(p, statuses)
	return r.one(ctx, query, args...)
}

func (r *repository) CountSame(ctx context.Context, p *Pipeline, statuses []Status) (int, error) {
	if len(statuses) == 0 {
		return 0, nil
	}
	query, args := countSameQuery(p, statuses)
	var n int
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting pipelines: %w", err)
	}
	return n, nil
}

// findSameQuery selects the newest pipeline testing what p tests at one of
// statuses.
func findSameQuery(p *Pipeline, statuses []Status) (string, []any) {
	where, args := sameWhere(p, statuses)
	return `SELECT ` + pipelineColumns + ` FROM pipelines WHERE ` + where + ` ORDER BY id DESC LIMIT 1`, args
}

// countSameQuery counts the pipelines testing what p tests at one of
// statuses.
func countSameQuery(p *Pipeline, statuses []Status) (string, []any) {
	where, args := sameWhere(p, statuses)
	return `SELECT COUNT(*) FROM pipelines WHERE ` + where, args
}

// sameWhere picks the pipelines testing what p tests at one of statuses,
// of p's subject, purpose included, so that reusing a result, the merge
// gate's current pipeline, a settled one and the count of those that could
// not run all see only pipelines of one purpose: through the index of a
// branch's commits, or of a merge request's pairs of commits, so it reads
// only pipelines of those commits however many the project keeps.
func sameWhere(p *Pipeline, statuses []Status) (string, []any) {
	sub := p.subject()
	var where string
	var args []any
	if sub.kind == KindMergeRequest {
		where = `merge_request_id = ? AND head_sha = ? AND target_sha = ? AND project_id = ? AND config_hash = ? AND purpose = ?`
		args = []any{sub.mergeRequestID, p.HeadSHA, p.TargetSHA, sub.projectID, p.ConfigHash, sub.purpose}
	} else {
		where = `project_id = ? AND kind = 'branch' AND branch = ? AND head_sha = ? AND config_hash = ? AND purpose = ?`
		args = []any{sub.projectID, sub.branch, p.HeadSHA, p.ConfigHash, sub.purpose}
	}
	where += ` AND status IN (` + placeholders(len(statuses)) + `)`
	for _, s := range statuses {
		args = append(args, s)
	}
	return where, args
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
	res, err := r.db.ExecContext(ctx, `UPDATE pipelines SET status = ?, image = ?, started_at = ?, keeps_output = 1 WHERE id = ? AND status = ?`,
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

func (r *repository) SetTested(ctx context.Context, id int64, sha, tree string) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE pipelines SET tested_sha = ?, tested_tree = ? WHERE id = ?`, sha, tree, id); err != nil {
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
	running, why := JobError, lostJob
	if p.Status == StatusCancelled || p.Status == StatusSuperseded {
		running, why = JobCancelled, p.Error
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_jobs SET status = ?, error = ?, finished_at = ? WHERE pipeline_id = ? AND status = ?`,
		running, why, p.FinishedAt, p.ID, JobRunning); err != nil {
		return false, fmt.Errorf("ending running jobs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing pipeline: %w", err)
	}
	return true, nil
}

func (r *repository) MarkAnnounced(ctx context.Context, id int64, at time.Time) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE pipelines SET announced_at = ? WHERE id = ?`, at, id); err != nil {
		return fmt.Errorf("recording the pipeline announced: %w", err)
	}
	return nil
}

func (r *repository) StartJob(ctx context.Context, jobID int64, at time.Time) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE pipeline_jobs SET status = ?, started_at = ? WHERE id = ? AND status = ?`,
		JobRunning, at, jobID, JobQueued); err != nil {
		return fmt.Errorf("starting job: %w", err)
	}
	return nil
}

func (r *repository) FinishJob(ctx context.Context, j *JobRun, told JobLog) error {
	output, notes, err := encodeLog(told)
	if err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx,
		`UPDATE pipeline_jobs SET status = ?, exit_code = ?, error = ?, output = ?, output_bytes = ?, notes = ?, finished_at = ? WHERE id = ?`,
		j.Status, j.ExitCode, j.Error, output, told.Total, notes, j.FinishedAt, j.ID); err != nil {
		return fmt.Errorf("finishing job: %w", err)
	}
	j.OutputBytes, j.KeptBytes = told.Total, int64(len(output))
	return nil
}

func (r *repository) SaveJobLog(ctx context.Context, jobID int64, told JobLog) error {
	output, notes, err := encodeLog(told)
	if err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE pipeline_jobs SET output = ?, output_bytes = ?, notes = ? WHERE id = ?`, output, told.Total, notes, jobID); err != nil {
		return fmt.Errorf("keeping what the job told: %w", err)
	}
	return nil
}

// encodeLog is a job's output and notes as their columns hold them.
func encodeLog(told JobLog) ([]byte, string, error) {
	output := told.Output
	if output == nil {
		output = []byte{}
	}
	notes := told.Notes
	if notes == nil {
		notes = []Note{}
	}
	b, err := json.Marshal(notes)
	if err != nil {
		return nil, "", fmt.Errorf("encoding the job's notes: %w", err)
	}
	return output, string(b), nil
}

func (r *repository) JobLog(ctx context.Context, pipelineID, jobID int64) (*JobRun, *JobLog, error) {
	return r.jobLog(ctx, `SELECT `+jobColumns(false)+`, output, notes FROM pipeline_jobs WHERE id = ? AND pipeline_id = ?`, jobID, pipelineID)
}

// JobTail reads the end of the output with substr, which counts a blob's
// bytes, so a job's megabyte of output is never read for its last lines.
func (r *repository) JobTail(ctx context.Context, pipelineID, jobID int64, n int) (*JobRun, *JobLog, error) {
	if n <= 0 {
		return nil, nil, fmt.Errorf("reading the end of a job's output: %d bytes", n)
	}
	return r.jobLog(ctx, `SELECT `+jobColumns(false)+`, substr(output, ?), notes FROM pipeline_jobs WHERE id = ? AND pipeline_id = ?`, -n, jobID, pipelineID)
}

// jobLog reads the job, its output and its notes that query selects.
func (r *repository) jobLog(ctx context.Context, query string, args ...any) (*JobRun, *JobLog, error) {
	row := r.db.QueryRowContext(ctx, query, args...)
	var told JobLog
	var notes string
	j, err := oneOrNil(scanJob(row, false, &told.Output, &notes))
	if err != nil || j == nil {
		return nil, nil, err
	}
	if err := json.Unmarshal([]byte(notes), &told.Notes); err != nil {
		return nil, nil, fmt.Errorf("decoding the job's notes: %w", err)
	}
	told.Total = j.OutputBytes
	return j, &told, nil
}

// keptOutputsQuery lists the project's pipelines that still keep output
// past its newest keep, through the index of those alone: one prune after
// each finish leaves it a few to read, however many the project has had.
const keptOutputsQuery = `SELECT id, status FROM pipelines WHERE project_id = ? AND keeps_output = 1 ORDER BY id DESC LIMIT -1 OFFSET ?`

func (r *repository) PruneOutputs(ctx context.Context, projectID int64, keep int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, keptOutputsQuery, projectID, keep)
	if err != nil {
		return fmt.Errorf("listing pipelines to prune: %w", err)
	}
	var old []int64
	for rows.Next() {
		var id int64
		var status Status
		if err := rows.Scan(&id, &status); err != nil {
			rows.Close()
			return fmt.Errorf("scanning pipeline: %w", err)
		}
		// One still running prunes itself once it finishes.
		if status.Finished() {
			old = append(old, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("listing pipelines to prune: %w", err)
	}
	for _, id := range old {
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_jobs SET output = x'' WHERE pipeline_id = ?`, id); err != nil {
			return fmt.Errorf("pruning pipeline output: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pipelines SET keeps_output = 0 WHERE id = ?`, id); err != nil {
			return fmt.Errorf("pruning pipeline output: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing the prune: %w", err)
	}
	return nil
}

// listUnfinishedQuery names the statuses as the partial index of unfinished
// pipelines does, so it reads only that index.
const listUnfinishedQuery = `SELECT ` + pipelineColumns + ` FROM pipelines WHERE status IN ('queued', 'running') ORDER BY id`

func (r *repository) ListUnfinished(ctx context.Context) ([]*Pipeline, error) {
	rows, err := r.db.QueryContext(ctx, listUnfinishedQuery)
	if err != nil {
		return nil, fmt.Errorf("listing unfinished pipelines: %w", err)
	}
	return collect(rows, scanPipeline)
}

func (r *repository) Interrupt(ctx context.Context, pipelines []*Pipeline, reason string, at time.Time) ([]*Pipeline, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	var interrupted []*Pipeline
	for _, p := range pipelines {
		res, err := tx.ExecContext(ctx, `UPDATE pipelines SET status = ?, error = ?, finished_at = ? WHERE id = ? AND status IN (?, ?)`,
			StatusInterrupted, reason, at, p.ID, StatusQueued, StatusRunning)
		if err != nil {
			return nil, fmt.Errorf("interrupting pipeline: %w", err)
		}
		if ok, err := affected(res); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE pipeline_jobs SET status = CASE status WHEN ? THEN ? ELSE ? END, finished_at = ? WHERE pipeline_id = ? AND status IN (?, ?)`,
			JobRunning, JobInterrupted, JobSkipped, at, p.ID, JobQueued, JobRunning); err != nil {
			return nil, fmt.Errorf("interrupting jobs: %w", err)
		}
		marked, finished := *p, at
		marked.Status, marked.Error, marked.FinishedAt = StatusInterrupted, reason, &finished
		interrupted = append(interrupted, &marked)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing interruptions: %w", err)
	}
	return interrupted, nil
}

// listUnannouncedQuery names the statuses as the partial index of
// unannounced pipelines does, so it reads only that index.
const listUnannouncedQuery = `SELECT id FROM pipelines WHERE announced_at IS NULL AND status NOT IN ('queued', 'running') ORDER BY id`

func (r *repository) ListUnannounced(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, listUnannouncedQuery)
	if err != nil {
		return nil, fmt.Errorf("listing unannounced pipelines: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning pipeline id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}
	return ids, nil
}

func (r *repository) RecordsByUID(ctx context.Context, uids []string) (map[string]UIDRecord, error) {
	records := make(map[string]UIDRecord, len(uids))
	// SQLite allows many more, but a sweep never needs one query to name
	// them all.
	const batch = 500
	for start := 0; start < len(uids); start += batch {
		part := uids[start:min(start+batch, len(uids))]
		args := make([]any, len(part))
		for i, uid := range part {
			args[i] = uid
		}
		rows, err := r.db.QueryContext(ctx, `SELECT uid, project_id, status FROM pipelines WHERE uid IN (`+placeholders(len(part))+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("reading pipelines by uid: %w", err)
		}
		for rows.Next() {
			var uid string
			var rec UIDRecord
			if err := rows.Scan(&uid, &rec.ProjectID, &rec.Status); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scanning pipeline: %w", err)
			}
			records[uid] = rec
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("reading pipelines by uid: %w", err)
		}
	}
	return records, nil
}

// listPinnedFinishedQuery reads only the index of pinned pipelines.
const listPinnedFinishedQuery = `SELECT ` + pipelineColumns + ` FROM pipelines WHERE ref_pinned = 1 AND status NOT IN ('queued', 'running') ORDER BY id`

func (r *repository) ListPinnedFinished(ctx context.Context) ([]*Pipeline, error) {
	rows, err := r.db.QueryContext(ctx, listPinnedFinishedQuery)
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

// jobsQuery selects the jobs of n pipelines, in their files' order.
func jobsQuery(withScripts bool, n int) string {
	return `SELECT ` + jobColumns(withScripts) + ` FROM pipeline_jobs WHERE pipeline_id IN (` + placeholders(n) + `) ORDER BY pipeline_id, position`
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
	rows, err := r.db.QueryContext(ctx, jobsQuery(withScripts, len(args)), args...)
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
	var started, finished, announced sql.NullTime
	if err := row.Scan(&p.ID, &p.ProjectID, &p.UserID, &p.UID, &p.Kind, &p.Purpose, &p.Branch, &mergeRequestID, &p.SourceBranch, &p.TargetBranch, &p.HeadSHA, &p.TargetSHA, &p.TestedSHA, &p.TestedTree, &p.ConfigSource, &p.ConfigPath, &p.ConfigHash, &p.ConfigSHA, &p.Image, &p.Status, &p.Error, &paths, &p.Trigger, &rerunOf, &p.RefPinned, &p.CreatedAt, &started, &finished, &announced); err != nil {
		return nil, err
	}
	p.MergeRequestID, p.RerunOf = mergeRequestID.Int64, rerunOf.Int64
	if err := json.Unmarshal([]byte(paths), &p.ConflictPaths); err != nil {
		return nil, fmt.Errorf("decoding conflicting paths: %w", err)
	}
	p.StartedAt, p.FinishedAt, p.AnnouncedAt = timePtr(started), timePtr(finished), timePtr(announced)
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
