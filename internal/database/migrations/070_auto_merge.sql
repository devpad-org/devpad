-- The protected branches a task's merge request merges into on its own once
-- a review agent approves it. None until the user picks some.
ALTER TABLE projects ADD COLUMN auto_merge_branches TEXT NOT NULL DEFAULT '[]';

-- Who merged a merge request: the user, or devpad after a review agent
-- approved it. Empty while it is not merged.
ALTER TABLE merge_requests ADD COLUMN merged_by TEXT NOT NULL DEFAULT ''
    CHECK(merged_by IN ('', 'user', 'auto'));

UPDATE merge_requests SET merged_by = 'user' WHERE status = 'merged';

-- Why an agent review's approval did not merge its merge request, when the
-- project merges into its target automatically.
ALTER TABLE merge_request_reviews ADD COLUMN auto_merge_error TEXT NOT NULL DEFAULT '';
