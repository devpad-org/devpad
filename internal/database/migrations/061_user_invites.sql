-- Invites let an admin hand someone a one-time link to create their own
-- account. Only a hash of each token is kept: the link is shown once.
-- An admin's outstanding invites go when the admin does.
CREATE TABLE user_invites (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash  TEXT     NOT NULL UNIQUE,
    email       TEXT     NOT NULL DEFAULT '',
    is_admin    BOOLEAN  NOT NULL DEFAULT 0,
    created_by  INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  DATETIME NOT NULL,
    used_at     DATETIME,
    used_by     INTEGER  REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_user_invites_pending ON user_invites(used_at);
