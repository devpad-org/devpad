-- Add description column to workspaces
ALTER TABLE workspaces ADD COLUMN description TEXT NOT NULL DEFAULT '';

-- Recreate workspaces table to update the status check constraint (remove 'deleted', add hard delete)
-- SQLite does not support ALTER COLUMN or modifying constraints, so we rebuild the table.
CREATE TABLE workspaces_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'stopped' CHECK(status IN ('creating','running','stopped')),
    image       TEXT    NOT NULL DEFAULT 'ubuntu:22.04',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO workspaces_new (id, user_id, name, description, status, image, created_at, updated_at)
    SELECT id, user_id, name, description,
           CASE WHEN status = 'deleted' THEN 'stopped' ELSE status END,
           image, created_at, updated_at
    FROM workspaces
    WHERE status != 'deleted';

DROP TABLE workspaces;
ALTER TABLE workspaces_new RENAME TO workspaces;

CREATE INDEX idx_workspaces_user_id ON workspaces(user_id);
CREATE INDEX idx_workspaces_status ON workspaces(status);
