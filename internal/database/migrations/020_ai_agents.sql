CREATE TABLE ai_agents (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id INTEGER REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    purpose      TEXT    NOT NULL DEFAULT '',
    instructions TEXT    NOT NULL,
    is_global    INTEGER NOT NULL DEFAULT 0 CHECK(is_global IN (0, 1)),
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((is_global = 1 AND workspace_id IS NULL) OR (is_global = 0 AND workspace_id IS NOT NULL))
);

CREATE INDEX idx_ai_agents_user_workspace
    ON ai_agents(user_id, workspace_id, name);

CREATE INDEX idx_ai_agents_user_global
    ON ai_agents(user_id, is_global, name);

ALTER TABLE ai_agent_runs
    ADD COLUMN agent_id TEXT NOT NULL DEFAULT 'default';
