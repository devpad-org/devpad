-- The commit a task's branch was first made from. A task that starts from a
-- branch other than the default already differs from the default, so "did
-- the agent commit anything" is answered against this commit instead.
ALTER TABLE tasks ADD COLUMN base_sha TEXT NOT NULL DEFAULT '';
