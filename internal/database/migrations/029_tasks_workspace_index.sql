-- The launcher links each sandbox to its task by workspace.
CREATE INDEX idx_tasks_workspace_id ON tasks(workspace_id);
