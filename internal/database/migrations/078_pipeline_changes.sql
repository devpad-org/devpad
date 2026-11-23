-- What a pipeline is for. An official pipeline is one devpad's results come
-- from: a protected branch's own, or a merge request's with the jobs of its
-- target branch, which the merge gate, the badges and everything that acts
-- on a result read. A proposed pipeline is a merge request's change's own
-- pipeline file, read at the trial merge and run on it for information
-- only: nothing reads it as a result of the merge request. Only a merge
-- request has one.
ALTER TABLE pipelines ADD COLUMN purpose TEXT NOT NULL DEFAULT 'official'
    CHECK (purpose IN ('official', 'proposed') AND (purpose = 'official' OR kind = 'merge_request'));

-- The look-closely mark of a merge request's change: the files it touches
-- that a pipeline doesn't guard, deleted tests and the files jobs commonly
-- depend on, as JSON ({"files": [{"path", "oldPath", "reason"}], "more"}),
-- and the head it was worked out for. A mark of another head than the
-- merge request's is out of date and is never shown.
ALTER TABLE merge_requests ADD COLUMN look_closely TEXT NOT NULL DEFAULT '';
ALTER TABLE merge_requests ADD COLUMN look_closely_head TEXT NOT NULL DEFAULT '';
