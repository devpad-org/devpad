-- A project's pipeline when a commit has no .devpad/pipeline.yml: the same
-- YAML, kept on the project for repos the user doesn't own. Empty means none.
ALTER TABLE projects ADD COLUMN pipeline_config TEXT NOT NULL DEFAULT '';

-- The protected branches that run the pipeline on each new commit, and on
-- each merge request into them. None until the user picks some.
ALTER TABLE projects ADD COLUMN pipeline_branches TEXT NOT NULL DEFAULT '[]';
