package settings

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// UserPreferences stores per-user UI preferences.
type UserPreferences struct {
	DiffViewSideBySide bool `json:"diffViewSideBySide"`
}

// PreferencesRepository defines data access for user preferences.
type PreferencesRepository interface {
	Get(ctx context.Context, userID int64) (UserPreferences, error)
	Update(ctx context.Context, userID int64, preferences UserPreferences) (UserPreferences, error)
}

type preferencesRepository struct {
	db *sql.DB
}

// NewPreferencesRepository creates a new PreferencesRepository backed by SQLite.
func NewPreferencesRepository(db *sql.DB) PreferencesRepository {
	return &preferencesRepository{db: db}
}

func DefaultUserPreferences() UserPreferences {
	return UserPreferences{DiffViewSideBySide: true}
}

func (r *preferencesRepository) Get(ctx context.Context, userID int64) (UserPreferences, error) {
	preferences := DefaultUserPreferences()
	err := r.db.QueryRowContext(ctx,
		`SELECT diff_view_side_by_side FROM user_preferences WHERE user_id = ?`,
		userID,
	).Scan(&preferences.DiffViewSideBySide)
	if err == sql.ErrNoRows {
		return preferences, nil
	}
	if err != nil {
		return UserPreferences{}, fmt.Errorf("querying user preferences: %w", err)
	}
	return preferences, nil
}

func (r *preferencesRepository) Update(ctx context.Context, userID int64, preferences UserPreferences) (UserPreferences, error) {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO user_preferences (user_id, diff_view_side_by_side, created_at, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   diff_view_side_by_side = excluded.diff_view_side_by_side,
		   updated_at = excluded.updated_at`,
		userID, preferences.DiffViewSideBySide, now, now,
	)
	if err != nil {
		return UserPreferences{}, fmt.Errorf("upserting user preferences: %w", err)
	}
	return preferences, nil
}
