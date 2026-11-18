-- A sandbox's setup run is pending until its services are ready: the
-- script starts only then, which can be minutes after the sandbox is made,
-- and a run still pending when devpad restarts is started on the next
-- start. SQLite cannot change a CHECK constraint in place, so the table is
-- recreated. It is copied out and back rather than renamed, since renaming
-- makes SQLite check every trigger in the schema again.

CREATE TEMP TABLE workspace_setups_copy AS SELECT * FROM workspace_setups;

DROP TABLE workspace_setups;

CREATE TABLE workspace_setups (
    workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    script       TEXT    NOT NULL,
    status       TEXT    NOT NULL CHECK(status IN ('pending','running','succeeded','failed')),
    command_id   TEXT    NOT NULL DEFAULT '',
    exit_code    INTEGER,
    output       TEXT    NOT NULL DEFAULT '',
    error        TEXT    NOT NULL DEFAULT '',
    started_at   DATETIME NOT NULL,
    finished_at  DATETIME
);

INSERT INTO workspace_setups (workspace_id, user_id, script, status, command_id, exit_code, output, error, started_at, finished_at)
SELECT workspace_id, user_id, script, status, command_id, exit_code, output, error, started_at, finished_at
FROM workspace_setups_copy;

DROP TABLE workspace_setups_copy;

CREATE INDEX idx_workspace_setups_status ON workspace_setups(status);
