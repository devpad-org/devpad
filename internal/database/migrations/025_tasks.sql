-- Tasks are units of work in a project that an AI agent implements in its own
-- sandbox, on a branch named after the task.

CREATE TABLE tasks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title           TEXT    NOT NULL,
    body            TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL DEFAULT 'todo' CHECK(status IN ('todo','queued','in_progress','in_review','changes_requested','merged','cancelled','failed')),
    branch          TEXT    NOT NULL DEFAULT '',
    workspace_id    INTEGER REFERENCES workspaces(id) ON DELETE SET NULL,
    conversation_id INTEGER REFERENCES ai_conversations(id) ON DELETE SET NULL,
    run_id          INTEGER REFERENCES ai_agent_runs(id) ON DELETE SET NULL,
    agent_id        TEXT    NOT NULL DEFAULT '',
    model           TEXT    NOT NULL DEFAULT '',
    effort          TEXT    NOT NULL DEFAULT '',
    pending_message TEXT    NOT NULL DEFAULT '',
    head_sha        TEXT    NOT NULL DEFAULT '',
    error           TEXT    NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tasks_project_id ON tasks(project_id);
CREATE INDEX idx_tasks_run_id ON tasks(run_id);
CREATE INDEX idx_tasks_status ON tasks(status);
