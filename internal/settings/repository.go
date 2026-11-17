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
	// Theme is one of Themes.
	Theme string `json:"theme"`
}

// DefaultTheme is the theme of a user who has not chosen one.
const DefaultTheme = "gruvbox-dark"

// Themes are the colour themes the frontend offers, in its order (THEMES in
// frontend/src/theme/index.ts); TestThemesMatchFrontend keeps them the same.
var Themes = []string{DefaultTheme, "gruvbox-light", "modern-dark", "modern-light"}

// PreferencesUpdate changes some of a user's preferences; a nil field keeps
// the preference as it is.
type PreferencesUpdate struct {
	DiffViewSideBySide *bool
	Theme              *string
}

// PreferencesRepository defines data access for user preferences.
type PreferencesRepository interface {
	Get(ctx context.Context, userID int64) (UserPreferences, error)
	// Update applies the update in one statement, so updates of different
	// preferences made at the same time never undo each other, and returns
	// all the preferences as they now are.
	Update(ctx context.Context, userID int64, update PreferencesUpdate) (UserPreferences, error)
}

type preferencesRepository struct {
	db *sql.DB
}

// NewPreferencesRepository creates a new PreferencesRepository backed by SQLite.
func NewPreferencesRepository(db *sql.DB) PreferencesRepository {
	return &preferencesRepository{db: db}
}

func DefaultUserPreferences() UserPreferences {
	return UserPreferences{DiffViewSideBySide: true, Theme: DefaultTheme}
}

func (r *preferencesRepository) Get(ctx context.Context, userID int64) (UserPreferences, error) {
	preferences := DefaultUserPreferences()
	err := r.db.QueryRowContext(ctx,
		`SELECT diff_view_side_by_side, theme FROM user_preferences WHERE user_id = ?`,
		userID,
	).Scan(&preferences.DiffViewSideBySide, &preferences.Theme)
	if err == sql.ErrNoRows {
		return preferences, nil
	}
	if err != nil {
		return UserPreferences{}, fmt.Errorf("querying user preferences: %w", err)
	}
	return preferences, nil
}

func (r *preferencesRepository) Update(ctx context.Context, userID int64, update PreferencesUpdate) (UserPreferences, error) {
	defaults := DefaultUserPreferences()
	now := time.Now()
	var preferences UserPreferences
	// ?2 and ?3 are the changes, NULL for a preference that keeps its value.
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO user_preferences (user_id, diff_view_side_by_side, theme, created_at, updated_at)
		 VALUES (?1, COALESCE(?2, ?4), COALESCE(?3, ?5), ?6, ?6)
		 ON CONFLICT(user_id) DO UPDATE SET
		   diff_view_side_by_side = COALESCE(?2, diff_view_side_by_side),
		   theme = COALESCE(?3, theme),
		   updated_at = excluded.updated_at
		 RETURNING diff_view_side_by_side, theme`,
		userID, update.DiffViewSideBySide, update.Theme, defaults.DiffViewSideBySide, defaults.Theme, now,
	).Scan(&preferences.DiffViewSideBySide, &preferences.Theme)
	if err != nil {
		return UserPreferences{}, fmt.Errorf("upserting user preferences: %w", err)
	}
	return preferences, nil
}
