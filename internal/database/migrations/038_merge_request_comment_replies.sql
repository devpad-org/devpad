-- Task agents comment on their merge requests and reply to review comments.
-- SQLite cannot alter CHECK constraints, so the comments table is recreated
-- with the new author and a reply_to column, keeping its rows.

CREATE TABLE merge_request_comments_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    review_id        INTEGER REFERENCES merge_request_reviews(id) ON DELETE CASCADE,
    -- reply_to is the comment this one answers, always the first of its thread.
    reply_to         INTEGER REFERENCES merge_request_comments(id) ON DELETE CASCADE,
    author           TEXT    NOT NULL CHECK(author IN ('user','agent','task_agent')),
    path             TEXT    NOT NULL DEFAULT '',
    line             INTEGER NOT NULL DEFAULT 0,
    body             TEXT    NOT NULL,
    head_sha         TEXT    NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO merge_request_comments_new (id, merge_request_id, review_id, author, path, line, body, head_sha, created_at)
SELECT id, merge_request_id, review_id, author, path, line, body, head_sha, created_at FROM merge_request_comments;

DROP TABLE merge_request_comments;
ALTER TABLE merge_request_comments_new RENAME TO merge_request_comments;

CREATE INDEX idx_merge_request_comments_mr ON merge_request_comments(merge_request_id);
CREATE INDEX idx_merge_request_comments_reply_to ON merge_request_comments(reply_to);
