-- A task's automatic pipeline rounds: how many times in a row devpad sent
-- the task back to its agent because its merge request's pipeline failed or
-- found a conflict, the fingerprint of the failure the last of them was sent
-- for, so a round that fails the same way ends them early, and the newest
-- pipeline devpad decided on, so a pipeline told twice, or after a newer
-- one, is never acted on again. pipeline_held says why the latest failure
-- waits for the user instead: the rounds ran out, the agent is stuck, or the
-- task's budget is spent. Stepping in (starting the task, requesting
-- changes, editing it) and a pass start the count again; the decided
-- pipeline is never reset.
ALTER TABLE tasks ADD COLUMN pipeline_rounds INTEGER NOT NULL DEFAULT 0 CHECK (pipeline_rounds >= 0);
ALTER TABLE tasks ADD COLUMN pipeline_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN pipeline_decided_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN pipeline_held TEXT NOT NULL DEFAULT ''
    CHECK (pipeline_held IN ('', 'limit', 'stuck', 'budget'));

-- The automatic review of a new merge request that a pipeline guards
-- waits for the pipeline to pass: set when it opens, cleared once the
-- review starts or another review makes it moot.
ALTER TABLE merge_requests ADD COLUMN review_awaits_pipeline INTEGER NOT NULL DEFAULT 0;
