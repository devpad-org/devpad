-- A user's theme is NULL until they choose one, so a user who never chose
-- follows the default (settings.DefaultTheme), including when it changes.
-- 057 stored the default in every row instead, and SQLite cannot drop NOT NULL
-- in place, so the table is recreated. 057 only ever ran on builds of the
-- branch that added themes, where the default was 'gruvbox-dark', so a stored
-- 'gruvbox-dark' is taken as not chosen.

CREATE TABLE user_preferences_new (
    user_id                INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    diff_view_side_by_side BOOLEAN NOT NULL DEFAULT 1,
    -- A theme id from frontend/src/theme/index.ts, or NULL for the default.
    theme                  TEXT,
    created_at             DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at             DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO user_preferences_new (user_id, diff_view_side_by_side, theme, created_at, updated_at)
SELECT user_id, diff_view_side_by_side, NULLIF(theme, 'gruvbox-dark'), created_at, updated_at
FROM user_preferences;

DROP TABLE user_preferences;

ALTER TABLE user_preferences_new RENAME TO user_preferences;
