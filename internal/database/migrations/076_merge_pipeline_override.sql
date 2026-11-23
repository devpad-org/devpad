-- A merge the user made although the merge request's pipeline had not let
-- it through: the merge gate's state then, such as 'failed' or 'running',
-- and the pipeline that stood, if any. Empty when the merge didn't override
-- the gate. Only the user overrides it, so only a merge by the user has one.
ALTER TABLE merge_requests ADD COLUMN pipeline_override TEXT NOT NULL DEFAULT ''
    CHECK (pipeline_override = '' OR (status = 'merged' AND merged_by = 'user'));
ALTER TABLE merge_requests ADD COLUMN pipeline_override_id INTEGER REFERENCES pipelines(id) ON DELETE SET NULL;

-- The few merge requests that name a pipeline, so deleting pipelines scans
-- none of the rest.
CREATE INDEX idx_merge_requests_pipeline_override ON merge_requests(pipeline_override_id) WHERE pipeline_override_id IS NOT NULL;
