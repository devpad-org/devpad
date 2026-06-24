ALTER TABLE workspaces ADD COLUMN default_agent_id TEXT NOT NULL DEFAULT 'default';

CREATE INDEX idx_workspaces_default_agent_id
    ON workspaces(default_agent_id);

CREATE TRIGGER reset_workspace_default_agent_after_delete
AFTER DELETE ON ai_agents
BEGIN
    UPDATE workspaces
       SET default_agent_id = 'default',
           updated_at = CURRENT_TIMESTAMP
     WHERE default_agent_id = CAST(OLD.id AS TEXT);
END;

CREATE TRIGGER reset_workspace_default_agent_after_update
AFTER UPDATE OF user_id, workspace_id, is_global ON ai_agents
BEGIN
    UPDATE workspaces
       SET default_agent_id = 'default',
           updated_at = CURRENT_TIMESTAMP
     WHERE default_agent_id = CAST(OLD.id AS TEXT)
       AND NOT EXISTS (
           SELECT 1
             FROM ai_agents
            WHERE id = OLD.id
              AND user_id = workspaces.user_id
              AND (is_global = 1 OR workspace_id = workspaces.id)
       );
END;
