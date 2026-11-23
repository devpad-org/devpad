-- The commit a pipeline's jobs were read from: a branch pipeline's head, a
-- merge request's official pipeline's target, or a proposed pipeline's
-- trial merge, which is also what a proposed pipeline must test: one whose
-- pin tests another commit ends in error rather than run the jobs on a tree
-- they didn't come from. Empty for pipelines from before it was kept.
ALTER TABLE pipelines ADD COLUMN config_sha TEXT NOT NULL DEFAULT '';
