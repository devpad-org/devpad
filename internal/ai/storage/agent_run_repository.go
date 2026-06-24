package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// AgentRunRepository stores background agent runs and their ordered events.
type AgentRunRepository struct {
	db *sql.DB
}

// NewAgentRunRepository creates a SQLite-backed agent run repository.
func NewAgentRunRepository(db *sql.DB) *AgentRunRepository {
	return &AgentRunRepository{db: db}
}

func (r *AgentRunRepository) CreateRun(ctx context.Context, run *domain.AgentRun) error {
	inputTurns, err := json.Marshal(run.InputTurns)
	if err != nil {
		return fmt.Errorf("marshalling input turns: %w", err)
	}
	thinking, err := marshalOptionalJSON(run.Thinking)
	if err != nil {
		return fmt.Errorf("marshalling thinking config: %w", err)
	}

	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_agent_runs
		 (parent_run_id, user_id, workspace_id, conversation_id, agent_id, model, status, error, input_turns, thinking, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableInt64(run.ParentRunID),
		run.UserID,
		run.WorkspaceID,
		nullableInt64(run.ConversationID),
		agentIDOrDefault(run.AgentID),
		run.Model,
		string(run.Status),
		run.Error,
		string(inputTurns),
		thinking,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("inserting agent run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting agent run ID: %w", err)
	}

	run.ID = id
	run.CreatedAt = now
	run.UpdatedAt = now
	return nil
}

func (r *AgentRunRepository) GetRun(ctx context.Context, id int64) (*domain.AgentRun, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, parent_run_id, user_id, workspace_id, conversation_id, agent_id, model, status, error,
		        input_turns, thinking, created_at, updated_at, started_at, completed_at
		 FROM ai_agent_runs
		 WHERE id = ?`,
		id,
	)

	run, err := scanAgentRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning agent run: %w", err)
	}
	return run, nil
}

func (r *AgentRunRepository) ListRuns(ctx context.Context, userID, workspaceID int64) ([]domain.AgentRun, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, parent_run_id, user_id, workspace_id, conversation_id, agent_id, model, status, error,
		        input_turns, thinking, created_at, updated_at, started_at, completed_at
		 FROM ai_agent_runs
		 WHERE user_id = ? AND workspace_id = ?
		 ORDER BY created_at DESC, id DESC`,
		userID,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying agent runs: %w", err)
	}
	defer rows.Close()

	runs := make([]domain.AgentRun, 0)
	for rows.Next() {
		run, err := scanAgentRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning agent run list item: %w", err)
		}
		runs = append(runs, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agent runs: %w", err)
	}
	return runs, nil
}

func (r *AgentRunRepository) ListEvents(ctx context.Context, runID, afterSequence int64) ([]domain.AgentRunEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, run_id, sequence, event_json, created_at
		 FROM ai_agent_run_events
		 WHERE run_id = ? AND sequence > ?
		 ORDER BY sequence ASC`,
		runID,
		afterSequence,
	)
	if err != nil {
		return nil, fmt.Errorf("querying agent run events: %w", err)
	}
	defer rows.Close()

	events := make([]domain.AgentRunEvent, 0)
	for rows.Next() {
		event, err := scanAgentRunEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agent run events: %w", err)
	}

	return events, nil
}

func (r *AgentRunRepository) AppendEvent(ctx context.Context, runID int64, event domain.ClientEvent) (domain.AgentRunEvent, error) {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return domain.AgentRunEvent{}, fmt.Errorf("marshalling agent run event: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AgentRunEvent{}, fmt.Errorf("beginning event append transaction: %w", err)
	}
	defer tx.Rollback()

	var sequence int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sequence), 0) + 1
		 FROM ai_agent_run_events
		 WHERE run_id = ?`,
		runID,
	).Scan(&sequence); err != nil {
		return domain.AgentRunEvent{}, fmt.Errorf("getting next event sequence: %w", err)
	}

	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx,
		`INSERT INTO ai_agent_run_events (run_id, sequence, event_json, created_at)
		 VALUES (?, ?, ?, ?)`,
		runID,
		sequence,
		string(eventJSON),
		now,
	)
	if err != nil {
		return domain.AgentRunEvent{}, fmt.Errorf("inserting agent run event: %w", err)
	}
	eventID, err := result.LastInsertId()
	if err != nil {
		return domain.AgentRunEvent{}, fmt.Errorf("getting agent run event ID: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return domain.AgentRunEvent{}, fmt.Errorf("committing event append transaction: %w", err)
	}

	return domain.AgentRunEvent{
		ID:        eventID,
		RunID:     runID,
		Sequence:  sequence,
		Event:     event,
		CreatedAt: now,
	}, nil
}

func (r *AgentRunRepository) UpdateStatus(ctx context.Context, runID int64, status domain.AgentRunStatus, errorMessage string) error {
	now := time.Now().UTC()
	var startedAt any
	var completedAt any
	if status == domain.AgentRunRunning || status == domain.AgentRunWaitingApproval || status == domain.AgentRunWaitingUser {
		startedAt = now
	}
	if domain.AgentRunStatusTerminal(status) {
		completedAt = now
	}

	result, err := r.db.ExecContext(ctx,
		`UPDATE ai_agent_runs
		 SET status = ?,
		     error = ?,
		     updated_at = ?,
		     started_at = CASE WHEN started_at IS NULL AND ? IS NOT NULL THEN ? ELSE started_at END,
		     completed_at = CASE WHEN ? IS NOT NULL THEN ? ELSE completed_at END
		 WHERE id = ?`,
		string(status),
		errorMessage,
		now,
		startedAt,
		startedAt,
		completedAt,
		completedAt,
		runID,
	)
	if err != nil {
		return fmt.Errorf("updating agent run status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking updated agent run count: %w", err)
	}
	if count == 0 {
		return domain.ErrAgentRunNotFound
	}
	return nil
}

func (r *AgentRunRepository) MarkActiveRunsFailed(ctx context.Context, message string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`UPDATE ai_agent_runs
		 SET status = ?, error = ?, updated_at = ?, completed_at = ?
		 WHERE status IN (?, ?, ?, ?)`,
		string(domain.AgentRunFailed),
		message,
		now,
		now,
		string(domain.AgentRunQueued),
		string(domain.AgentRunRunning),
		string(domain.AgentRunWaitingApproval),
		string(domain.AgentRunWaitingUser),
	)
	if err != nil {
		return fmt.Errorf("marking active agent runs failed: %w", err)
	}
	return nil
}

type agentRunScanner interface {
	Scan(dest ...any) error
}

func scanAgentRun(scanner agentRunScanner) (*domain.AgentRun, error) {
	var (
		run            domain.AgentRun
		parentRunID    sql.NullInt64
		conversationID sql.NullInt64
		status         string
		inputTurns     string
		thinking       string
		startedAt      sql.NullTime
		completedAt    sql.NullTime
	)

	err := scanner.Scan(
		&run.ID,
		&parentRunID,
		&run.UserID,
		&run.WorkspaceID,
		&conversationID,
		&run.AgentID,
		&run.Model,
		&status,
		&run.Error,
		&inputTurns,
		&thinking,
		&run.CreatedAt,
		&run.UpdatedAt,
		&startedAt,
		&completedAt,
	)
	if err != nil {
		return nil, err
	}

	run.ParentRunID = parentRunID.Int64
	run.ConversationID = conversationID.Int64
	if run.AgentID == "" {
		run.AgentID = domain.DefaultAgentID
	}
	run.Status = domain.AgentRunStatus(status)
	if inputTurns != "" {
		if err := json.Unmarshal([]byte(inputTurns), &run.InputTurns); err != nil {
			return nil, fmt.Errorf("unmarshalling input turns: %w", err)
		}
	}
	if thinking != "" {
		var cfg domain.ThinkingConfig
		if err := json.Unmarshal([]byte(thinking), &cfg); err != nil {
			return nil, fmt.Errorf("unmarshalling thinking config: %w", err)
		}
		run.Thinking = &cfg
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}

	return &run, nil
}

type agentRunEventScanner interface {
	Scan(dest ...any) error
}

func scanAgentRunEvent(scanner agentRunEventScanner) (domain.AgentRunEvent, error) {
	var (
		event     domain.AgentRunEvent
		eventJSON string
	)
	if err := scanner.Scan(&event.ID, &event.RunID, &event.Sequence, &eventJSON, &event.CreatedAt); err != nil {
		return domain.AgentRunEvent{}, fmt.Errorf("scanning agent run event: %w", err)
	}
	if err := json.Unmarshal([]byte(eventJSON), &event.Event); err != nil {
		return domain.AgentRunEvent{}, fmt.Errorf("unmarshalling agent run event: %w", err)
	}
	return event, nil
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func marshalOptionalJSON(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func agentIDOrDefault(value string) string {
	if value == "" {
		return domain.DefaultAgentID
	}
	return value
}
