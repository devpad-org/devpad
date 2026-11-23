-- What a pipeline is for. An official pipeline is one devpad's results come
-- from: a protected branch's own, or a merge request's with the jobs of its
-- target branch, which the merge gate, the badges and everything that acts
-- on a result read. A proposed pipeline is a merge request's change's own
-- pipeline file, read at the trial merge and run on it for information
-- only: nothing reads it as a result of the merge request. Only a merge
-- request has one.
ALTER TABLE pipelines ADD COLUMN purpose TEXT NOT NULL DEFAULT 'official'
    CHECK (purpose IN ('official', 'proposed') AND (purpose = 'official' OR kind = 'merge_request'));

-- The commit a pipeline's jobs were read from: a branch pipeline's head, a
-- merge request's official pipeline's target, or a proposed pipeline's
-- trial merge, which is also what a proposed pipeline must test: one whose
-- pin tests another commit ends in error rather than run the jobs on a tree
-- they didn't come from. Empty for pipelines from before it was kept.
ALTER TABLE pipelines ADD COLUMN config_sha TEXT NOT NULL DEFAULT '';

-- What a merge request keeps of the look-closely mark of its change: how
-- many files it lists for each reason, as JSON ({"reasons": [{"reason",
-- "files"}]}), and none of the change's paths, which come with its diff;
-- and the head it was worked out for. A mark of another head than the
-- merge request's is out of date and is never shown.
ALTER TABLE merge_requests ADD COLUMN look_closely TEXT NOT NULL DEFAULT '';
ALTER TABLE merge_requests ADD COLUMN look_closely_head TEXT NOT NULL DEFAULT '';
