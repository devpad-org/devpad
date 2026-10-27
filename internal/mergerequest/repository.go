package mergerequest

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Repository defines data access for merge requests, reviews and comments.
type Repository interface {
	Create(ctx context.Context, mr *MergeRequest) error
	GetByID(ctx context.Context, id int64) (*MergeRequest, error)
	GetOpenBySource(ctx context.Context, projectID int64, sourceBranch string) (*MergeRequest, error)
	ListByProjectID(ctx context.Context, projectID int64) ([]*MergeRequest, error)
	Update(ctx context.Context, mr *MergeRequest) error
	// UpdateHead moves mr to head and records the push in the same
	// transaction.
	UpdateHead(ctx context.Context, mr *MergeRequest, head string, source PushSource) error
	ListPushes(ctx context.Context, mergeRequestID int64) ([]*Push, error)

	CreateReview(ctx context.Context, r *Review) error
	GetReview(ctx context.Context, id int64) (*Review, error)
	GetReviewByRunID(ctx context.Context, runID int64) (*Review, error)
	GetPendingReviewByWorkspace(ctx context.Context, workspaceID int64) (*Review, error)
	ListReviews(ctx context.Context, mergeRequestID int64) ([]*Review, error)
	ListPendingReviews(ctx context.Context) ([]*Review, error)
	UpdateReview(ctx context.Context, r *Review) error

	CreateComment(ctx context.Context, c *Comment) error
	GetComment(ctx context.Context, id int64) (*Comment, error)
	ListComments(ctx context.Context, mergeRequestID int64) ([]*Comment, error)
	CountReviewComments(ctx context.Context, reviewID int64) (int, error)
	CountAuthorComments(ctx context.Context, mergeRequestID int64, author Author) (int, error)
	// SetResolved resolves the thread a comment starts, or opens it again
	// when by is empty. It does nothing to a reply.
	SetResolved(ctx context.Context, commentID int64, by Author) error
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new merge request Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

const mrColumns = `id, project_id, user_id, task_id, title, description, source_branch, target_branch, status, head_sha, base_sha, merge_sha, merged_at, created_at, updated_at`

func (r *repository) Create(ctx context.Context, mr *MergeRequest) error {
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO merge_requests (project_id, user_id, task_id, title, description, source_branch, target_branch, status, head_sha, base_sha, merge_sha, merged_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mr.ProjectID, mr.UserID, nullableID(mr.TaskID), mr.Title, mr.Description, mr.SourceBranch, mr.TargetBranch, mr.Status, mr.HeadSHA, mr.BaseSHA, mr.MergeSHA, mr.MergedAt, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting merge request: %w", err)
	}
	if mr.ID, err = result.LastInsertId(); err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}
	mr.CreatedAt, mr.UpdatedAt = now, now
	return nil
}

func (r *repository) GetByID(ctx context.Context, id int64) (*MergeRequest, error) {
	return oneOrNil(scanMergeRequest(r.db.QueryRowContext(ctx, `SELECT `+mrColumns+` FROM merge_requests WHERE id = ?`, id)))
}

func (r *repository) GetOpenBySource(ctx context.Context, projectID int64, sourceBranch string) (*MergeRequest, error) {
	return oneOrNil(scanMergeRequest(r.db.QueryRowContext(ctx,
		`SELECT `+mrColumns+` FROM merge_requests WHERE project_id = ? AND source_branch = ? AND status = 'open'`, projectID, sourceBranch)))
}

func (r *repository) ListByProjectID(ctx context.Context, projectID int64) ([]*MergeRequest, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+mrColumns+` FROM merge_requests WHERE project_id = ? ORDER BY id DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing merge requests: %w", err)
	}
	return collect(rows, scanMergeRequest)
}

func (r *repository) Update(ctx context.Context, mr *MergeRequest) error {
	now := time.Now()
	if _, err := r.db.ExecContext(ctx,
		`UPDATE merge_requests SET title = ?, description = ?, status = ?, head_sha = ?, base_sha = ?, merge_sha = ?, merged_at = ?, updated_at = ? WHERE id = ?`,
		mr.Title, mr.Description, mr.Status, mr.HeadSHA, mr.BaseSHA, mr.MergeSHA, mr.MergedAt, now, mr.ID,
	); err != nil {
		return fmt.Errorf("updating merge request: %w", err)
	}
	mr.UpdatedAt = now
	return nil
}

func (r *repository) UpdateHead(ctx context.Context, mr *MergeRequest, head string, source PushSource) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	now := time.Now()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO merge_request_pushes (merge_request_id, source, before_sha, head_sha, created_at) VALUES (?, ?, ?, ?, ?)`,
		mr.ID, source, mr.HeadSHA, head, now,
	); err != nil {
		return fmt.Errorf("inserting push: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE merge_requests SET title = ?, description = ?, head_sha = ?, updated_at = ? WHERE id = ?`,
		mr.Title, mr.Description, head, now, mr.ID,
	); err != nil {
		return fmt.Errorf("updating merge request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing push: %w", err)
	}
	mr.HeadSHA, mr.UpdatedAt = head, now
	return nil
}

func (r *repository) ListPushes(ctx context.Context, mergeRequestID int64) ([]*Push, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, merge_request_id, source, before_sha, head_sha, created_at FROM merge_request_pushes WHERE merge_request_id = ? ORDER BY id`, mergeRequestID)
	if err != nil {
		return nil, fmt.Errorf("listing pushes: %w", err)
	}
	return collect(rows, func(row rowScanner) (*Push, error) {
		var p Push
		err := row.Scan(&p.ID, &p.MergeRequestID, &p.Source, &p.BeforeSHA, &p.HeadSHA, &p.CreatedAt)
		return &p, err
	})
}

const reviewColumns = `id, merge_request_id, author, status, verdict, body, head_sha, workspace_id, run_id, agent_id, model, effort, auto_fix, error, created_at, updated_at`

func (r *repository) CreateReview(ctx context.Context, rv *Review) error {
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO merge_request_reviews (merge_request_id, author, status, verdict, body, head_sha, workspace_id, run_id, agent_id, model, effort, auto_fix, error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rv.MergeRequestID, rv.Author, rv.Status, rv.Verdict, rv.Body, rv.HeadSHA, nullableID(rv.WorkspaceID), nullableID(rv.RunID), rv.AgentID, rv.Model, rv.Effort, rv.AutoFix, rv.Error, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting review: %w", err)
	}
	if rv.ID, err = result.LastInsertId(); err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}
	rv.CreatedAt, rv.UpdatedAt = now, now
	return nil
}

func (r *repository) GetReview(ctx context.Context, id int64) (*Review, error) {
	return oneOrNil(scanReview(r.db.QueryRowContext(ctx, `SELECT `+reviewColumns+` FROM merge_request_reviews WHERE id = ?`, id)))
}

func (r *repository) GetReviewByRunID(ctx context.Context, runID int64) (*Review, error) {
	return oneOrNil(scanReview(r.db.QueryRowContext(ctx, `SELECT `+reviewColumns+` FROM merge_request_reviews WHERE run_id = ?`, runID)))
}

func (r *repository) GetPendingReviewByWorkspace(ctx context.Context, workspaceID int64) (*Review, error) {
	return oneOrNil(scanReview(r.db.QueryRowContext(ctx,
		`SELECT `+reviewColumns+` FROM merge_request_reviews WHERE workspace_id = ? AND status = 'pending' ORDER BY id DESC LIMIT 1`, workspaceID)))
}

func (r *repository) ListReviews(ctx context.Context, mergeRequestID int64) ([]*Review, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+reviewColumns+` FROM merge_request_reviews WHERE merge_request_id = ? ORDER BY id`, mergeRequestID)
	if err != nil {
		return nil, fmt.Errorf("listing reviews: %w", err)
	}
	return collect(rows, scanReview)
}

func (r *repository) ListPendingReviews(ctx context.Context) ([]*Review, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+reviewColumns+` FROM merge_request_reviews WHERE status = 'pending' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing pending reviews: %w", err)
	}
	return collect(rows, scanReview)
}

func (r *repository) UpdateReview(ctx context.Context, rv *Review) error {
	now := time.Now()
	if _, err := r.db.ExecContext(ctx,
		`UPDATE merge_request_reviews SET status = ?, verdict = ?, body = ?, workspace_id = ?, run_id = ?, error = ?, updated_at = ? WHERE id = ?`,
		rv.Status, rv.Verdict, rv.Body, nullableID(rv.WorkspaceID), nullableID(rv.RunID), rv.Error, now, rv.ID,
	); err != nil {
		return fmt.Errorf("updating review: %w", err)
	}
	rv.UpdatedAt = now
	return nil
}

func (r *repository) CreateComment(ctx context.Context, c *Comment) error {
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO merge_request_comments (merge_request_id, review_id, reply_to, author, path, line, body, head_sha, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.MergeRequestID, nullableID(c.ReviewID), nullableID(c.ReplyTo), c.Author, c.Path, c.Line, c.Body, c.HeadSHA, now,
	)
	if err != nil {
		return fmt.Errorf("inserting comment: %w", err)
	}
	if c.ID, err = result.LastInsertId(); err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}
	c.CreatedAt = now
	return nil
}

const commentColumns = `id, merge_request_id, review_id, reply_to, author, path, line, body, head_sha, created_at, resolved_by, resolved_at`

func (r *repository) GetComment(ctx context.Context, id int64) (*Comment, error) {
	return oneOrNil(scanComment(r.db.QueryRowContext(ctx, `SELECT `+commentColumns+` FROM merge_request_comments WHERE id = ?`, id)))
}

func (r *repository) ListComments(ctx context.Context, mergeRequestID int64) ([]*Comment, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+commentColumns+` FROM merge_request_comments WHERE merge_request_id = ? ORDER BY id`, mergeRequestID)
	if err != nil {
		return nil, fmt.Errorf("listing comments: %w", err)
	}
	return collect(rows, scanComment)
}

func (r *repository) CountReviewComments(ctx context.Context, reviewID int64) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM merge_request_comments WHERE review_id = ?`, reviewID).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting comments: %w", err)
	}
	return n, nil
}

func (r *repository) CountAuthorComments(ctx context.Context, mergeRequestID int64, author Author) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM merge_request_comments WHERE merge_request_id = ? AND author = ?`, mergeRequestID, author).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting comments: %w", err)
	}
	return n, nil
}

func (r *repository) SetResolved(ctx context.Context, commentID int64, by Author) error {
	var at any
	if by != "" {
		at = time.Now()
	}
	if _, err := r.db.ExecContext(ctx,
		`UPDATE merge_request_comments SET resolved_by = ?, resolved_at = ? WHERE id = ? AND reply_to IS NULL`, by, at, commentID,
	); err != nil {
		return fmt.Errorf("resolving comment: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMergeRequest(row rowScanner) (*MergeRequest, error) {
	var mr MergeRequest
	var taskID sql.NullInt64
	var mergedAt sql.NullTime
	if err := row.Scan(&mr.ID, &mr.ProjectID, &mr.UserID, &taskID, &mr.Title, &mr.Description, &mr.SourceBranch, &mr.TargetBranch, &mr.Status, &mr.HeadSHA, &mr.BaseSHA, &mr.MergeSHA, &mergedAt, &mr.CreatedAt, &mr.UpdatedAt); err != nil {
		return nil, err
	}
	mr.TaskID = taskID.Int64
	if mergedAt.Valid {
		mr.MergedAt = &mergedAt.Time
	}
	return &mr, nil
}

func scanReview(row rowScanner) (*Review, error) {
	var rv Review
	var workspaceID, runID sql.NullInt64
	if err := row.Scan(&rv.ID, &rv.MergeRequestID, &rv.Author, &rv.Status, &rv.Verdict, &rv.Body, &rv.HeadSHA, &workspaceID, &runID, &rv.AgentID, &rv.Model, &rv.Effort, &rv.AutoFix, &rv.Error, &rv.CreatedAt, &rv.UpdatedAt); err != nil {
		return nil, err
	}
	rv.WorkspaceID, rv.RunID = workspaceID.Int64, runID.Int64
	return &rv, nil
}

func scanComment(row rowScanner) (*Comment, error) {
	var c Comment
	var reviewID, replyTo sql.NullInt64
	var resolvedAt sql.NullTime
	if err := row.Scan(&c.ID, &c.MergeRequestID, &reviewID, &replyTo, &c.Author, &c.Path, &c.Line, &c.Body, &c.HeadSHA, &c.CreatedAt, &c.ResolvedBy, &resolvedAt); err != nil {
		return nil, err
	}
	c.ReviewID, c.ReplyTo = reviewID.Int64, replyTo.Int64
	if resolvedAt.Valid {
		c.ResolvedAt = &resolvedAt.Time
	}
	return &c, nil
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

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
