-- A task can start from a branch other than the default, such as one the
-- user handed off from their project workspace, and can carry the
-- conversation it was handed off from as context for its agent.
ALTER TABLE tasks ADD COLUMN base_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN handoff TEXT NOT NULL DEFAULT '';
