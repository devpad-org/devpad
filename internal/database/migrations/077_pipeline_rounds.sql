-- A task's automatic pipeline rounds: how many times in a row devpad sent
-- the task back to its agent because its merge request's pipeline failed or
-- found a conflict, the fingerprint of the failure the last of them was sent
-- for, so a round that fails the same way ends them early, the head the
-- round still outstanding was sent for (empty once a newer pipeline is
-- decided), so a round that brings no new result is held too, and the
-- newest pipeline devpad decided on, so a pipeline told twice, or after a
-- newer one, is never acted on again. pipeline_held says why the latest
-- failure waits for the user instead: the rounds ran out, the agent is
-- stuck, the task's budget is spent, or the agent could not be started.
-- Stepping in (starting the task, requesting changes, editing a task that
-- holds nothing) and a pass start the count again; the decided pipeline is
-- never reset.
ALTER TABLE tasks ADD COLUMN pipeline_rounds INTEGER NOT NULL DEFAULT 0 CHECK (pipeline_rounds >= 0);
ALTER TABLE tasks ADD COLUMN pipeline_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN pipeline_sent_head TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN pipeline_decided_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN pipeline_held TEXT NOT NULL DEFAULT '';

-- Why a failure can wait for the user (task.PipelineHold), checked by
-- triggers rather than a CHECK on the column, which SQLite changes only by
-- rebuilding tasks, whose rows so many tables reference: a migration that
-- adds a reason drops these two and creates them again. Triggers go with
-- their table: a migration that rebuilds tasks must create these two again,
-- or the reasons are no longer checked at all.
CREATE TRIGGER tasks_pipeline_held_insert BEFORE INSERT ON tasks
WHEN NEW.pipeline_held NOT IN ('', 'limit', 'stuck', 'budget', 'not_started')
BEGIN
    SELECT RAISE(ABORT, 'a task''s pipeline hold names a reason devpad knows');
END;
CREATE TRIGGER tasks_pipeline_held_update BEFORE UPDATE OF pipeline_held ON tasks
WHEN NEW.pipeline_held NOT IN ('', 'limit', 'stuck', 'budget', 'not_started')
BEGIN
    SELECT RAISE(ABORT, 'a task''s pipeline hold names a reason devpad knows');
END;

-- When the task's pending message quotes a pipeline's job output, devpad's
-- own summary of that failure (the pipeline, its failed jobs' names and
-- how they ended, never what they printed), which is all anything but the
-- task's own agent and its user is shown of it.
ALTER TABLE tasks ADD COLUMN pending_pipeline TEXT NOT NULL DEFAULT '';

-- The automatic review of a new merge request that a pipeline guards
-- waits for the pipeline to pass: set when it opens, cleared once the
-- review starts or another review makes it moot.
ALTER TABLE merge_requests ADD COLUMN review_awaits_pipeline INTEGER NOT NULL DEFAULT 0
    CHECK (review_awaits_pipeline IN (0, 1));
