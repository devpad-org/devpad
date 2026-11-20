-- Each project has at most one interactive workspace: the user's own IDE for
-- the project. Unlike sandboxes it owns no branch and can switch between
-- them, so branch ownership applies to sandboxes only.

DROP INDEX idx_workspaces_project_branch;

CREATE UNIQUE INDEX idx_workspaces_project_branch
    ON workspaces(project_id, branch)
    WHERE project_id IS NOT NULL AND kind != 'interactive';

CREATE UNIQUE INDEX idx_workspaces_project_interactive
    ON workspaces(project_id)
    WHERE project_id IS NOT NULL AND kind = 'interactive';
