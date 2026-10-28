-- A merge request's source branch can also move when devpad fast-forwards a
-- protected branch from the project's upstream, as when develop, the source
-- of a release into main, gets commits that landed upstream. source is
-- 'upstream' then. SQLite cannot alter a CHECK constraint, so the table is
-- recreated with its rows; nothing references it.

CREATE TABLE merge_request_pushes_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    source           TEXT    NOT NULL CHECK(source IN ('task','workspace','merge','upstream')),
    before_sha       TEXT    NOT NULL DEFAULT '',
    head_sha         TEXT    NOT NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO merge_request_pushes_new (id, merge_request_id, source, before_sha, head_sha, created_at)
SELECT id, merge_request_id, source, before_sha, head_sha, created_at FROM merge_request_pushes;

DROP TABLE merge_request_pushes;
ALTER TABLE merge_request_pushes_new RENAME TO merge_request_pushes;

CREATE INDEX idx_merge_request_pushes_mr ON merge_request_pushes(merge_request_id);
