-- +migrate: up
ALTER TABLE workspaces ADD COLUMN agent_token TEXT NOT NULL DEFAULT '';
