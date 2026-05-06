CREATE TABLE ai_agent_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_run_id   INTEGER REFERENCES ai_agent_runs(id) ON DELETE SET NULL,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id    INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    conversation_id INTEGER REFERENCES ai_conversations(id) ON DELETE SET NULL,
    model           TEXT    NOT NULL,
    status          TEXT    NOT NULL CHECK(status IN ('queued','running','waiting_approval','completed','failed','cancelled')),
    error           TEXT    NOT NULL DEFAULT '',
    input_turns     TEXT    NOT NULL DEFAULT '[]',
    thinking        TEXT    NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    completed_at    DATETIME
);

CREATE INDEX idx_ai_agent_runs_user_workspace
    ON ai_agent_runs(user_id, workspace_id, created_at DESC);

CREATE INDEX idx_ai_agent_runs_parent
    ON ai_agent_runs(parent_run_id);

CREATE INDEX idx_ai_agent_runs_status
    ON ai_agent_runs(status);

CREATE TABLE ai_agent_run_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     INTEGER NOT NULL REFERENCES ai_agent_runs(id) ON DELETE CASCADE,
    sequence   INTEGER NOT NULL,
    event_json TEXT    NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(run_id, sequence)
);

CREATE INDEX idx_ai_agent_run_events_run_sequence
    ON ai_agent_run_events(run_id, sequence);
