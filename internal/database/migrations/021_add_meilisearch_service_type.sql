-- Add meilisearch to the service_type CHECK constraint.
-- SQLite cannot alter CHECK constraints in place, so recreate the table.

CREATE TABLE workspace_services_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    service_type  TEXT    NOT NULL CHECK(service_type IN ('postgres','mongodb','mariadb','couchdb','meilisearch')),
    container_id  TEXT    NOT NULL DEFAULT '',
    volume_name   TEXT    NOT NULL DEFAULT '',
    status        TEXT    NOT NULL DEFAULT 'stopped' CHECK(status IN ('running','stopped')),
    config        TEXT    NOT NULL DEFAULT '{}',
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO workspace_services_new (
    id,
    workspace_id,
    service_type,
    container_id,
    volume_name,
    status,
    config,
    created_at,
    updated_at
)
SELECT
    id,
    workspace_id,
    service_type,
    container_id,
    volume_name,
    status,
    config,
    created_at,
    updated_at
FROM workspace_services;

DROP TABLE workspace_services;

ALTER TABLE workspace_services_new RENAME TO workspace_services;

CREATE INDEX idx_workspace_services_workspace_id ON workspace_services(workspace_id);
