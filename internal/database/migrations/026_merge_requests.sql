-- Merge requests propose merging a branch of a project's canonical repo into
-- its default branch. Users and review agents review them; only users merge.

CREATE TABLE merge_requests (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_id       INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
    title         TEXT    NOT NULL,
    description   TEXT    NOT NULL DEFAULT '',
    source_branch TEXT    NOT NULL,
    target_branch TEXT    NOT NULL,
    status        TEXT    NOT NULL DEFAULT 'open' CHECK(status IN ('open','merged','closed')),
    head_sha      TEXT    NOT NULL DEFAULT '',
    base_sha      TEXT    NOT NULL DEFAULT '',
    merge_sha     TEXT    NOT NULL DEFAULT '',
    merged_at     DATETIME,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_merge_requests_project_id ON merge_requests(project_id);
-- A branch has at most one open merge request.
CREATE UNIQUE INDEX idx_merge_requests_open_source ON merge_requests(project_id, source_branch) WHERE status = 'open';

CREATE TABLE merge_request_reviews (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    author           TEXT    NOT NULL CHECK(author IN ('user','agent')),
    status           TEXT    NOT NULL CHECK(status IN ('pending','submitted','failed')),
    verdict          TEXT    NOT NULL DEFAULT '' CHECK(verdict IN ('','approve','request_changes','comment')),
    body             TEXT    NOT NULL DEFAULT '',
    head_sha         TEXT    NOT NULL DEFAULT '',
    workspace_id     INTEGER REFERENCES workspaces(id) ON DELETE SET NULL,
    run_id           INTEGER REFERENCES ai_agent_runs(id) ON DELETE SET NULL,
    agent_id         TEXT    NOT NULL DEFAULT '',
    model            TEXT    NOT NULL DEFAULT '',
    effort           TEXT    NOT NULL DEFAULT '',
    auto_fix         INTEGER NOT NULL DEFAULT 0,
    error            TEXT    NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_merge_request_reviews_mr ON merge_request_reviews(merge_request_id);
CREATE INDEX idx_merge_request_reviews_workspace ON merge_request_reviews(workspace_id);
CREATE INDEX idx_merge_request_reviews_run ON merge_request_reviews(run_id);

CREATE TABLE merge_request_comments (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    review_id        INTEGER REFERENCES merge_request_reviews(id) ON DELETE CASCADE,
    author           TEXT    NOT NULL CHECK(author IN ('user','agent')),
    path             TEXT    NOT NULL DEFAULT '',
    line             INTEGER NOT NULL DEFAULT 0,
    body             TEXT    NOT NULL,
    head_sha         TEXT    NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_merge_request_comments_mr ON merge_request_comments(merge_request_id);
