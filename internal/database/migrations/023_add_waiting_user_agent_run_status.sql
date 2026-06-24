-- Add waiting_user to the ai_agent_runs status CHECK constraint.
-- SQLite cannot alter CHECK constraints directly, so recreate the run table
-- and its dependent event table while preserving existing rows.

CREATE TABLE ai_agent_runs_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_run_id   INTEGER REFERENCES ai_agent_runs_new(id) ON DELETE SET NULL,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id    INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    conversation_id INTEGER REFERENCES ai_conversations(id) ON DELETE SET NULL,
    agent_id        TEXT    NOT NULL DEFAULT 'default',
    model           TEXT    NOT NULL,
    status          TEXT    NOT NULL CHECK(status IN ('queued','running','waiting_approval','waiting_user','completed','failed','cancelled')),
    error           TEXT    NOT NULL DEFAULT '',
    input_turns     TEXT    NOT NULL DEFAULT '[]',
    thinking        TEXT    NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    completed_at    DATETIME
);

INSERT INTO ai_agent_runs_new (
    id,
    parent_run_id,
    user_id,
    workspace_id,
    conversation_id,
    agent_id,
    model,
    status,
    error,
    input_turns,
    thinking,
    created_at,
    updated_at,
    started_at,
    completed_at
)
SELECT
    id,
    parent_run_id,
    user_id,
    workspace_id,
    conversation_id,
    agent_id,
    model,
    status,
    error,
    input_turns,
    thinking,
    created_at,
    updated_at,
    started_at,
    completed_at
FROM ai_agent_runs;

CREATE TABLE ai_agent_run_events_new (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     INTEGER NOT NULL REFERENCES ai_agent_runs_new(id) ON DELETE CASCADE,
    sequence   INTEGER NOT NULL,
    event_json TEXT    NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(run_id, sequence)
);

INSERT INTO ai_agent_run_events_new (
    id,
    run_id,
    sequence,
    event_json,
    created_at
)
SELECT
    id,
    run_id,
    sequence,
    event_json,
    created_at
FROM ai_agent_run_events;

DROP TABLE ai_agent_run_events;
DROP TABLE ai_agent_runs;

ALTER TABLE ai_agent_runs_new RENAME TO ai_agent_runs;
ALTER TABLE ai_agent_run_events_new RENAME TO ai_agent_run_events;

CREATE INDEX idx_ai_agent_runs_user_workspace
    ON ai_agent_runs(user_id, workspace_id, created_at DESC);

CREATE INDEX idx_ai_agent_runs_parent
    ON ai_agent_runs(parent_run_id);

CREATE INDEX idx_ai_agent_runs_status
    ON ai_agent_runs(status);

CREATE INDEX idx_ai_agent_run_events_run_sequence
    ON ai_agent_run_events(run_id, sequence);
