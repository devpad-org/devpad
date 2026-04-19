-- Add mariadb and couchdb to the service_type CHECK constraint.
-- SQLite does not support ALTER TABLE … DROP/ADD CONSTRAINT, so we
-- recreate the table with the updated check.

CREATE TABLE workspace_services_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    service_type  TEXT    NOT NULL CHECK(service_type IN ('postgres','mongodb','mariadb','couchdb')),
    container_id  TEXT    NOT NULL DEFAULT '',
    volume_name   TEXT    NOT NULL DEFAULT '',
    status        TEXT    NOT NULL DEFAULT 'stopped' CHECK(status IN ('running','stopped')),
    config        TEXT    NOT NULL DEFAULT '{}',
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO workspace_services_new
    SELECT * FROM workspace_services;

DROP TABLE workspace_services;

ALTER TABLE workspace_services_new RENAME TO workspace_services;

CREATE INDEX idx_workspace_services_workspace_id ON workspace_services(workspace_id);
