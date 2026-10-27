-- Each comment that starts a thread on a merge request can be resolved, by
-- the user or by a review agent that finds the change addresses it. Replies
-- are never resolved themselves: their thread is. resolved_by is the author
-- kind that resolved the thread, empty while it is open.

ALTER TABLE merge_request_comments ADD COLUMN resolved_by TEXT NOT NULL DEFAULT '';
ALTER TABLE merge_request_comments ADD COLUMN resolved_at DATETIME;
