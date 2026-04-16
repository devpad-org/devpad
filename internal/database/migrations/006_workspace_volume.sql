-- +migrate: up
ALTER TABLE workspaces ADD COLUMN volume_name TEXT NOT NULL DEFAULT '';
