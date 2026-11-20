-- Converting a standalone workspace into a project is recorded from the
-- moment the project is made. Once the workspace is attached the conversion
-- cannot be undone, so the steps after it (making the project ready,
-- restarting the container, rotating the user's SSH key) are recorded as
-- pending first and cleared as each is done: a crash or a lost response
-- cannot skip them, and the user sees what happened until they dismiss it.
-- Stripped credentials are kept as kind and location only, never values.

CREATE TABLE project_conversions (
    project_id       INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id     INTEGER NOT NULL,
    in_place         INTEGER NOT NULL DEFAULT 0,
    init_sha         TEXT    NOT NULL DEFAULT '',
    state            TEXT    NOT NULL DEFAULT 'importing' CHECK(state IN ('importing','attached','done')),
    stripped         TEXT    NOT NULL DEFAULT '[]',
    ssh_kept         TEXT    NOT NULL DEFAULT '[]',
    restart_pending  INTEGER NOT NULL DEFAULT 0,
    restart_error    TEXT    NOT NULL DEFAULT '',
    rotation_pending INTEGER NOT NULL DEFAULT 0,
    key_rotated      INTEGER NOT NULL DEFAULT 0,
    public_key       TEXT    NOT NULL DEFAULT '',
    rotation_error   TEXT    NOT NULL DEFAULT '',
    dismissed        INTEGER NOT NULL DEFAULT 0,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_project_conversions_state ON project_conversions(state);
