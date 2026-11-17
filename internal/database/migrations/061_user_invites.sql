-- Invites let an admin hand someone a one-time link to create their own
-- account. Only a hash of each token is kept: the link is shown once.
-- An admin's outstanding invites go when the admin does: deleting them
-- cascades, and demoting them deletes their unused invites in the same
-- statement, so no path that takes away admin leaves a working invite behind.
CREATE TABLE user_invites (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash  TEXT     NOT NULL UNIQUE,
    is_admin    BOOLEAN  NOT NULL DEFAULT 0,
    created_by  INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  DATETIME NOT NULL,
    used_at     DATETIME,
    used_by     INTEGER  REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_user_invites_pending ON user_invites(used_at);

CREATE TRIGGER revoke_invites_after_demotion
AFTER UPDATE OF is_admin ON users
WHEN OLD.is_admin AND NOT NEW.is_admin
BEGIN
    DELETE FROM user_invites WHERE created_by = NEW.id AND used_at IS NULL;
END;
