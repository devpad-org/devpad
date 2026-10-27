-- Each time an open merge request's source branch moves to a new commit,
-- devpad records it, so the activity timeline can show the commits each move
-- added at the time devpad saw them, rather than at the dates the commits
-- claim. source is what moved the branch: 'task' when a task's agent
-- finished a round, 'workspace' when it was published from the project
-- workspace, 'merge' when another merge request was merged into it.

CREATE TABLE merge_request_pushes (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    source           TEXT    NOT NULL CHECK(source IN ('task','workspace','merge')),
    before_sha       TEXT    NOT NULL DEFAULT '',
    head_sha         TEXT    NOT NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_merge_request_pushes_mr ON merge_request_pushes(merge_request_id);
