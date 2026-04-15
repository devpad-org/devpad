-- +migrate: up
ALTER TABLE workspaces ADD COLUMN container_id TEXT NOT NULL DEFAULT '';
