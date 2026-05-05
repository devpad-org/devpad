CREATE TABLE IF NOT EXISTS user_preferences (
    user_id                INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    diff_view_side_by_side BOOLEAN NOT NULL DEFAULT 1,
    created_at             DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at             DATETIME DEFAULT CURRENT_TIMESTAMP
);
