package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// usageSlotSeconds is the length of a UsageSlot.
const usageSlotSeconds = 15 * 60

// UsageRepository keeps the ledger of provider requests.
type UsageRepository struct {
	db *sql.DB
}

// NewUsageRepository creates a SQLite-backed usage ledger.
func NewUsageRepository(db *sql.DB) *UsageRepository {
	return &UsageRepository{db: db}
}

// RecordUsage adds one request to the ledger, with the project and kind of
// the workspace it was made in as they are now, so the row keeps them once
// the workspace is gone. A workspace that is not the user's is left out: the
// ID can come from a request, and the row would show the user another
// user's project and count the user's spend under it.
func (r *UsageRepository) RecordUsage(ctx context.Context, rec domain.UsageRecord) error {
	var cost any
	if rec.Usage.CostUSD != nil {
		cost = *rec.Usage.CostUSD
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_usage (user_id, project_id, project_name, workspace_id, workspace_kind, run_id, model,
		                       input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, created_at)
		 SELECT ?, w.project_id, COALESCE(p.name, ''), w.id, COALESCE(w.kind, ''), ?, ?, ?, ?, ?, ?, ?, ?
		 FROM (SELECT 1)
		 LEFT JOIN workspaces w ON w.id = ? AND w.user_id = ?
		 LEFT JOIN projects p ON p.id = w.project_id`,
		rec.UserID,
		nullableInt64(rec.RunID),
		rec.Model,
		rec.Usage.InputTokens,
		rec.Usage.OutputTokens,
		rec.Usage.CacheReadTokens,
		rec.Usage.CacheWriteTokens,
		cost,
		rec.At.Unix(),
		rec.WorkspaceID,
		rec.UserID,
	); err != nil {
		return fmt.Errorf("inserting usage: %w", err)
	}
	return nil
}

// ListUsageSlots totals the ledger by quarter hour, project and model from
// from up to but not including to. A zero userID covers every user. A
// project is named as it is now, or, once deleted, as it was when last used.
func (r *UsageRepository) ListUsageSlots(ctx context.Context, userID int64, from, to time.Time) ([]domain.UsageSlot, error) {
	query := `SELECT u.created_at / ? AS slot, COALESCE(u.project_id, 0), COALESCE(p.name, MAX(u.project_name)),
	                 u.project_id IS NOT NULL AND p.id IS NULL, u.model,
	                 COUNT(*), SUM(u.cost_usd IS NULL), SUM(u.input_tokens), SUM(u.output_tokens),
	                 SUM(u.cache_read_tokens), SUM(u.cache_write_tokens), COALESCE(SUM(u.cost_usd), 0)
	          FROM ai_usage u
	          LEFT JOIN projects p ON p.id = u.project_id
	          WHERE u.created_at >= ? AND u.created_at < ?`
	args := []any{usageSlotSeconds, from.Unix(), to.Unix()}
	if userID != 0 {
		query += ` AND u.user_id = ?`
		args = append(args, userID)
	}
	rows, err := r.db.QueryContext(ctx, query+` GROUP BY slot, u.project_id, u.model ORDER BY slot`, args...)
	if err != nil {
		return nil, fmt.Errorf("querying usage: %w", err)
	}
	defer rows.Close()

	var slots []domain.UsageSlot
	for rows.Next() {
		var slot domain.UsageSlot
		var start int64
		t := &slot.Totals
		if err := rows.Scan(&start, &slot.ProjectID, &slot.ProjectName, &slot.ProjectDeleted, &slot.Model,
			&t.Requests, &t.UnpricedRequests, &t.InputTokens, &t.OutputTokens,
			&t.CacheReadTokens, &t.CacheWriteTokens, &t.CostUSD); err != nil {
			return nil, fmt.Errorf("scanning usage: %w", err)
		}
		slot.Start = time.Unix(start*usageSlotSeconds, 0).UTC()
		slots = append(slots, slot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating usage: %w", err)
	}
	return slots, nil
}
