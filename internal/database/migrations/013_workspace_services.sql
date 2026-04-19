CREATE TABLE IF NOT EXISTS workspace_services (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    service_type  TEXT    NOT NULL CHECK(service_type IN ('postgres','mongodb')),
    container_id  TEXT    NOT NULL DEFAULT '',
    volume_name   TEXT    NOT NULL DEFAULT '',
    status        TEXT    NOT NULL DEFAULT 'stopped' CHECK(status IN ('running','stopped')),
    config        TEXT    NOT NULL DEFAULT '{}',
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_workspace_services_workspace_id ON workspace_services(workspace_id);
