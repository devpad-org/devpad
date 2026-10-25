-- The latest run of a project's setup script in each of its workspaces.
-- Devpad starts it when a workspace is first seeded, and the user can run
-- it again in the project workspace. A sandbox's agent starts only once its
-- setup has finished, and is told how it went. The output is the end of
-- what the script printed, read from the workspace, so it is untrusted.

CREATE TABLE workspace_setups (
    workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    script       TEXT    NOT NULL,
    status       TEXT    NOT NULL CHECK(status IN ('running','succeeded','failed')),
    command_id   TEXT    NOT NULL DEFAULT '',
    exit_code    INTEGER,
    output       TEXT    NOT NULL DEFAULT '',
    error        TEXT    NOT NULL DEFAULT '',
    started_at   DATETIME NOT NULL,
    finished_at  DATETIME
);

CREATE INDEX idx_workspace_setups_status ON workspace_setups(status);
