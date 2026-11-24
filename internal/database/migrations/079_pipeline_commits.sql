-- The Git graph reads, by commit, the pipelines of each branch a commit was
-- tested on, whichever branches those were.
CREATE INDEX idx_pipelines_commit ON pipelines(project_id, head_sha, id) WHERE kind = 'branch';
