package task

import (
	"context"
	"database/sql"
	"fmt"
	"time"
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
}

type repository struct {
	db *sql.DB
}

// NewRepository creates a new task Repository backed by SQLite.
func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

const taskColumns = `id, project_id, user_id, title, body, status, branch, workspace_id, conversation_id, run_id, agent_id, model, effort, pending_message, head_sha, error, base_branch, base_sha, handoff, budget_usd, cost_usd, created_at, updated_at`

func (r *repository) Create(ctx context.Context, t *Task) error {
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO tasks (project_id, user_id, title, body, status, branch, workspace_id, conversation_id, run_id, agent_id, model, effort, pending_message, head_sha, error, base_branch, base_sha, handoff, budget_usd, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ProjectID, t.UserID, t.Title, t.Body, t.Status, t.Branch, nullableID(t.WorkspaceID), nullableID(t.ConversationID), nullableID(t.RunID), t.AgentID, t.Model, t.Effort, t.PendingMessage, t.HeadSHA, t.Error, t.BaseBranch, t.BaseSHA, t.Handoff, t.BudgetUSD, now, now,
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
		`UPDATE tasks SET title = ?, body = ?, status = ?, branch = ?, workspace_id = ?, conversation_id = ?, run_id = ?, agent_id = ?, model = ?, effort = ?, pending_message = ?, head_sha = ?, error = ?, base_sha = ?, budget_usd = ?, updated_at = ? WHERE id = ?`,
		t.Title, t.Body, t.Status, t.Branch, nullableID(t.WorkspaceID), nullableID(t.ConversationID), nullableID(t.RunID), t.AgentID, t.Model, t.Effort, t.PendingMessage, t.HeadSHA, t.Error, t.BaseSHA, t.BudgetUSD, now, t.ID,
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (*Task, error) {
	var t Task
	var workspaceID, conversationID, runID sql.NullInt64
	if err := row.Scan(&t.ID, &t.ProjectID, &t.UserID, &t.Title, &t.Body, &t.Status, &t.Branch, &workspaceID, &conversationID, &runID, &t.AgentID, &t.Model, &t.Effort, &t.PendingMessage, &t.HeadSHA, &t.Error, &t.BaseBranch, &t.BaseSHA, &t.Handoff, &t.BudgetUSD, &t.CostUSD, &t.CreatedAt, &t.UpdatedAt); err != nil {
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
