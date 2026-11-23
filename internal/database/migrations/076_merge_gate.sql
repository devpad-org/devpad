-- A merge the user made although the merge request's pipeline had not let
-- it through: the merge gate's state then, one of those the user can merge
-- over, and the pipeline that stood, if any. Empty when the merge didn't
-- override the gate. Only the user overrides it, so only a merge by the
-- user has one, and only an override names a pipeline.
ALTER TABLE merge_requests ADD COLUMN pipeline_override TEXT NOT NULL DEFAULT ''
    CHECK (pipeline_override = '' OR (status = 'merged' AND merged_by = 'user'));
ALTER TABLE merge_requests ADD COLUMN pipeline_override_id INTEGER REFERENCES pipelines(id) ON DELETE SET NULL
    CHECK (pipeline_override_id IS NULL OR pipeline_override != '');

-- The gate states the user can merge over (pipeline.Gate.Overridable). The
-- list is checked by triggers rather than in the column's CHECK, which
-- SQLite changes only by rebuilding merge_requests, whose rows so many
-- tables reference: a migration that changes the list drops these two and
-- creates them again.
CREATE TRIGGER merge_requests_pipeline_override_insert BEFORE INSERT ON merge_requests
WHEN NEW.pipeline_override NOT IN ('', 'queued', 'running', 'not_run', 'out_of_date', 'failed', 'invalid', 'error', 'cancelled')
BEGIN
    SELECT RAISE(ABORT, 'a pipeline override names a gate state the user can merge over');
END;
CREATE TRIGGER merge_requests_pipeline_override_update BEFORE UPDATE OF pipeline_override ON merge_requests
WHEN NEW.pipeline_override NOT IN ('', 'queued', 'running', 'not_run', 'out_of_date', 'failed', 'invalid', 'error', 'cancelled')
BEGIN
    SELECT RAISE(ABORT, 'a pipeline override names a gate state the user can merge over');
END;

-- The few merge requests that name a pipeline, so deleting pipelines scans
-- none of the rest.
CREATE INDEX idx_merge_requests_pipeline_override ON merge_requests(pipeline_override_id) WHERE pipeline_override_id IS NOT NULL;

-- The tree of what a pipeline's jobs got (tested_sha's), which a merge the
-- pipeline lets through must land. Empty until it is pinned, and when the
-- repo's agent did not say.
ALTER TABLE pipelines ADD COLUMN tested_tree TEXT NOT NULL DEFAULT '';
