-- One row for each provider request, kept for the usage page. Unlike a
-- run's totals, a row outlives the workspace, run, project and user it was
-- for, so the ids are plain columns rather than foreign keys. project_name
-- is the project's name when the request was made, shown once the project
-- is deleted. cost_usd is NULL when the model's prices were not known.
-- created_at is in Unix seconds, so periods can be counted in any zone.
CREATE TABLE ai_usage (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id            INTEGER NOT NULL,
    project_id         INTEGER,
    project_name       TEXT    NOT NULL DEFAULT '',
    workspace_id       INTEGER,
    workspace_kind     TEXT    NOT NULL DEFAULT '',
    run_id             INTEGER,
    model              TEXT    NOT NULL,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd           REAL,
    created_at         INTEGER NOT NULL
);

CREATE INDEX idx_ai_usage_created ON ai_usage(created_at);
CREATE INDEX idx_ai_usage_user_created ON ai_usage(user_id, created_at);

-- Runs from before the ledger become one row each, at the time they
-- started. Runs whose sandboxes were already deleted are gone.
INSERT INTO ai_usage (user_id, project_id, project_name, workspace_id, workspace_kind, run_id, model,
                      input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, created_at)
SELECT r.user_id, w.project_id, COALESCE(p.name, ''), r.workspace_id, w.kind, r.id, r.model,
       r.input_tokens, r.output_tokens, r.cache_read_tokens, r.cache_write_tokens, r.cost_usd,
       CAST(strftime('%s', COALESCE(r.started_at, r.created_at)) AS INTEGER)
FROM ai_agent_runs r
JOIN workspaces w ON w.id = r.workspace_id
LEFT JOIN projects p ON p.id = w.project_id
WHERE (r.input_tokens + r.output_tokens + r.cache_read_tokens + r.cache_write_tokens > 0 OR r.cost_usd IS NOT NULL)
  AND strftime('%s', COALESCE(r.started_at, r.created_at)) IS NOT NULL;
