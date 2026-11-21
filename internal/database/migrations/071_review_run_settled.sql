-- Whether devpad has settled the end of a submitted agent review's run:
-- stopped its sandbox and decided whether its approval merges the change.
-- Until then the approval may still merge, and a restart settles it.
ALTER TABLE merge_request_reviews ADD COLUMN run_settled INTEGER NOT NULL DEFAULT 0;

-- Every review from before was settled as its run ended, and none of them
-- is to merge now.
UPDATE merge_request_reviews SET run_settled = 1;
