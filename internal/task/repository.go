package task

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/database"
)

// Repository defines data access for tasks.
type Repository interface {
	Create(ctx context.Context, t *Task) error
	GetByID(ctx context.Context, id int64) (*Task, error)
	GetByRunID(ctx context.Context, runID int64) (*Task, error)
	ListByProjectID(ctx context.Context, projectID int64) ([]*Task, error)
	ListByStatus(ctx context.Context, status Status) ([]*Task, error)
	// Update saves the task's fields except CostUSD, which only RecordCost
	// changes.
	Update(ctx context.Context, t *Task) error
	// RecordCost saves the total the task's agents have spent. It never
	// lowers the stored total, so a total recorded late cannot undo a later
	// one.
	RecordCost(ctx context.Context, id int64, spentUSD float64) error
	Delete(ctx context.Context, id int64) error

	// CreateLink stores a link between two tasks of the project, or
	// returns ErrAlreadyLinked when the two are linked already.
	CreateLink(ctx context.Context, projectID int64, link Link) error
	// DeleteLink removes the link between two tasks, whichever way it
	// goes, and reports whether there was one.
	DeleteLink(ctx context.Context, a, b int64) (bool, error)
	// ListLinks returns the project's links.
	ListLinks(ctx context.Context, projectID int64) ([]Link, error)
	// LinksOf returns the links to and from one task.
	LinksOf(ctx context.Context, taskID int64) ([]Link, error)

	// GetByWorkspaceID returns the task whose sandbox the workspace is, or
	// nil when there is none.
	GetByWorkspaceID(ctx context.Context, workspaceID int64) (*Task, error)

	// CreateComment stores a new comment, which the database records as its
	// first version. It refuses one past limit comments by the same author
	// on the task with ErrCommentLimit; zero means no limit.
	CreateComment(ctx context.Context, c *Comment, limit int) error
	// GetComment returns a comment, or nil when there is none.
	GetComment(ctx context.Context, id int64) (*Comment, error)
	// ListComments returns a task's comments, oldest first.
	ListComments(ctx context.Context, taskID int64) ([]*Comment, error)
	// EditComment replaces a comment's body, which the database keeps as a
	// new version. It refuses an edit by anyone but the comment's author
	// with ErrNotCommentAuthor, and one past maxCommentRevisions versions
	// with ErrCommentLimit. An edit that changes nothing leaves the comment
	// as it is.
	EditComment(ctx context.Context, id int64, author Author, body string) (*Comment, error)
	// ListCommentRevisions returns every version of a comment, oldest first.
	ListCommentRevisions(ctx context.Context, commentID int64) ([]*CommentRevision, error)
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new task Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

const taskColumns = `id, project_id, user_id, title, body, status, branch, workspace_id, conversation_id, run_id, agent_id, model, effort, pending_message, head_sha, error, base_branch, base_sha, handoff, budget_usd, cost_usd, start_when_unblocked, pipeline_rounds, pipeline_fingerprint, pipeline_decided_id, pipeline_held, created_at, updated_at`

func (r *repository) Create(ctx context.Context, t *Task) error {
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO tasks (project_id, user_id, title, body, status, branch, workspace_id, conversation_id, run_id, agent_id, model, effort, pending_message, head_sha, error, base_branch, base_sha, handoff, budget_usd, start_when_unblocked, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ProjectID, t.UserID, t.Title, t.Body, t.Status, t.Branch, nullableID(t.WorkspaceID), nullableID(t.ConversationID), nullableID(t.RunID), t.AgentID, t.Model, t.Effort, t.PendingMessage, t.HeadSHA, t.Error, t.BaseBranch, t.BaseSHA, t.Handoff, t.BudgetUSD, t.StartWhenUnblocked, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting task: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}
	t.ID = id
	t.CreatedAt = now
	t.UpdatedAt = now
	return nil
}

func (r *repository) GetByID(ctx context.Context, id int64) (*Task, error) {
	return r.getOne(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
}

func (r *repository) GetByRunID(ctx context.Context, runID int64) (*Task, error) {
	return r.getOne(ctx, `SELECT `+taskColumns+` FROM tasks WHERE run_id = ?`, runID)
}

func (r *repository) getOne(ctx context.Context, query string, arg int64) (*Task, error) {
	t, err := scanTask(r.db.QueryRowContext(ctx, query, arg))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying task: %w", err)
	}
	return t, nil
}

func (r *repository) ListByProjectID(ctx context.Context, projectID int64) ([]*Task, error) {
	return r.list(ctx, `SELECT `+taskColumns+` FROM tasks WHERE project_id = ? ORDER BY id`, projectID)
}

func (r *repository) ListByStatus(ctx context.Context, status Status) ([]*Task, error) {
	return r.list(ctx, `SELECT `+taskColumns+` FROM tasks WHERE status = ? ORDER BY id`, status)
}

func (r *repository) list(ctx context.Context, query string, arg any) ([]*Task, error) {
	rows, err := r.db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tasks: %w", err)
	}
	return tasks, nil
}

func (r *repository) Update(ctx context.Context, t *Task) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET title = ?, body = ?, status = ?, branch = ?, workspace_id = ?, conversation_id = ?, run_id = ?, agent_id = ?, model = ?, effort = ?, pending_message = ?, head_sha = ?, error = ?, base_sha = ?, budget_usd = ?, start_when_unblocked = ?, pipeline_rounds = ?, pipeline_fingerprint = ?, pipeline_decided_id = ?, pipeline_held = ?, updated_at = ? WHERE id = ?`,
		t.Title, t.Body, t.Status, t.Branch, nullableID(t.WorkspaceID), nullableID(t.ConversationID), nullableID(t.RunID), t.AgentID, t.Model, t.Effort, t.PendingMessage, t.HeadSHA, t.Error, t.BaseSHA, t.BudgetUSD, t.StartWhenUnblocked,
		t.PipelineRounds.Count, t.PipelineRounds.Fingerprint, t.PipelineRounds.DecidedID, t.PipelineRounds.Held, now, t.ID,
	)
	if err != nil {
		return fmt.Errorf("updating task: %w", err)
	}
	t.UpdatedAt = now
	return nil
}

func (r *repository) RecordCost(ctx context.Context, id int64, spentUSD float64) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE tasks SET cost_usd = MAX(cost_usd, ?) WHERE id = ?`, spentUSD, id); err != nil {
		return fmt.Errorf("recording task cost: %w", err)
	}
	return nil
}

func (r *repository) Delete(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting task: %w", err)
	}
	return nil
}

func (r *repository) CreateLink(ctx context.Context, projectID int64, link Link) error {
	kind := "related"
	if link.Blocks {
		kind = "blocks"
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO task_links (project_id, from_task_id, to_task_id, kind) VALUES (?, ?, ?, ?)`,
		projectID, link.From, link.To, kind,
	)
	if database.IsUniqueViolation(err) {
		return ErrAlreadyLinked
	}
	if err != nil {
		return fmt.Errorf("inserting task link: %w", err)
	}
	return nil
}

func (r *repository) DeleteLink(ctx context.Context, a, b int64) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM task_links WHERE (from_task_id = ? AND to_task_id = ?) OR (from_task_id = ? AND to_task_id = ?)`,
		a, b, b, a,
	)
	if err != nil {
		return false, fmt.Errorf("deleting task link: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("deleting task link: %w", err)
	}
	return n > 0, nil
}

const linkQuery = `SELECT l.from_task_id, l.to_task_id, l.kind, f.status FROM task_links l JOIN tasks f ON f.id = l.from_task_id`

func (r *repository) ListLinks(ctx context.Context, projectID int64) ([]Link, error) {
	return r.links(ctx, linkQuery+` WHERE l.project_id = ? ORDER BY l.id`, projectID)
}

func (r *repository) LinksOf(ctx context.Context, taskID int64) ([]Link, error) {
	return r.links(ctx, linkQuery+` WHERE l.from_task_id = ? OR l.to_task_id = ? ORDER BY l.id`, taskID, taskID)
}

func (r *repository) links(ctx context.Context, query string, args ...any) ([]Link, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing task links: %w", err)
	}
	defer rows.Close()
	var links []Link
	for rows.Next() {
		var l Link
		var kind string
		if err := rows.Scan(&l.From, &l.To, &kind, &l.FromStatus); err != nil {
			return nil, fmt.Errorf("scanning task link: %w", err)
		}
		l.Blocks = kind == "blocks"
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating task links: %w", err)
	}
	return links, nil
}

func (r *repository) GetByWorkspaceID(ctx context.Context, workspaceID int64) (*Task, error) {
	return r.getOne(ctx, `SELECT `+taskColumns+` FROM tasks WHERE workspace_id = ? ORDER BY id DESC LIMIT 1`, workspaceID)
}

const commentQuery = `SELECT c.id, c.task_id, c.author, c.body, c.created_at, c.updated_at,
	(SELECT COUNT(*) FROM task_comment_revisions v WHERE v.comment_id = c.id) FROM task_comments c`

func (r *repository) CreateComment(ctx context.Context, c *Comment, limit int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	if limit > 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_comments WHERE task_id = ? AND author = ?`, c.TaskID, c.Author).Scan(&n); err != nil {
			return fmt.Errorf("counting task comments: %w", err)
		}
		if n >= limit {
			return ErrCommentLimit
		}
	}
	now := time.Now()
	result, err := tx.ExecContext(ctx,
		`INSERT INTO task_comments (task_id, author, body, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		c.TaskID, c.Author, c.Body, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting task comment: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting inserted id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing task comment: %w", err)
	}
	c.ID, c.Revisions, c.CreatedAt, c.UpdatedAt = id, 1, now, now
	return nil
}

func (r *repository) GetComment(ctx context.Context, id int64) (*Comment, error) {
	return getComment(ctx, r.db, id)
}

// queryer is a database or a transaction.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getComment(ctx context.Context, q queryer, id int64) (*Comment, error) {
	c, err := scanComment(q.QueryRowContext(ctx, commentQuery+` WHERE c.id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting task comment: %w", err)
	}
	return c, nil
}

func (r *repository) ListComments(ctx context.Context, taskID int64) ([]*Comment, error) {
	rows, err := r.db.QueryContext(ctx, commentQuery+` WHERE c.task_id = ? ORDER BY c.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("listing task comments: %w", err)
	}
	defer rows.Close()
	var comments []*Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning task comment: %w", err)
		}
		comments = append(comments, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating task comments: %w", err)
	}
	return comments, nil
}

func (r *repository) EditComment(ctx context.Context, id int64, author Author, body string) (*Comment, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	c, err := getComment(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	switch {
	case c == nil:
		return nil, ErrCommentNotFound
	case c.Author != author:
		return nil, ErrNotCommentAuthor
	case c.Body == body:
		return c, nil
	case c.Revisions >= maxCommentRevisions:
		return nil, ErrCommentLimit
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE task_comments SET body = ?, updated_at = ? WHERE id = ?`, body, now, id); err != nil {
		return nil, fmt.Errorf("editing task comment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing task comment edit: %w", err)
	}
	c.Body, c.UpdatedAt = body, now
	c.Revisions++
	return c, nil
}

func (r *repository) ListCommentRevisions(ctx context.Context, commentID int64) ([]*CommentRevision, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, comment_id, body, created_at FROM task_comment_revisions WHERE comment_id = ? ORDER BY id`, commentID)
	if err != nil {
		return nil, fmt.Errorf("listing task comment revisions: %w", err)
	}
	defer rows.Close()
	var revisions []*CommentRevision
	for rows.Next() {
		var v CommentRevision
		if err := rows.Scan(&v.ID, &v.CommentID, &v.Body, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning task comment revision: %w", err)
		}
		revisions = append(revisions, &v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating task comment revisions: %w", err)
	}
	return revisions, nil
}

func scanComment(row rowScanner) (*Comment, error) {
	var c Comment
	if err := row.Scan(&c.ID, &c.TaskID, &c.Author, &c.Body, &c.CreatedAt, &c.UpdatedAt, &c.Revisions); err != nil {
		return nil, err
	}
	return &c, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (*Task, error) {
	var t Task
	var workspaceID, conversationID, runID sql.NullInt64
	if err := row.Scan(&t.ID, &t.ProjectID, &t.UserID, &t.Title, &t.Body, &t.Status, &t.Branch, &workspaceID, &conversationID, &runID, &t.AgentID, &t.Model, &t.Effort, &t.PendingMessage, &t.HeadSHA, &t.Error, &t.BaseBranch, &t.BaseSHA, &t.Handoff, &t.BudgetUSD, &t.CostUSD, &t.StartWhenUnblocked,
		&t.PipelineRounds.Count, &t.PipelineRounds.Fingerprint, &t.PipelineRounds.DecidedID, &t.PipelineRounds.Held, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.WorkspaceID = workspaceID.Int64
	t.ConversationID = conversationID.Int64
	t.RunID = runID.Int64
	return &t, nil
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
