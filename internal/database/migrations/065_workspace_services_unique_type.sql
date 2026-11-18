-- A workspace has at most one service of each type: their volume and
-- container are named by the workspace and type, so a second would share
-- the first's volume and never get a container of its own. Creating one
-- only checked for an existing one first, which two requests at once could
-- both pass, so the database now refuses the second.
--
-- Of any duplicates left by such a race, the one with a container is kept,
-- or else the oldest: only one of them can ever have had a container.
DELETE FROM workspace_services
WHERE id NOT IN (
    SELECT COALESCE(MIN(CASE WHEN container_id != '' THEN id END), MIN(id))
    FROM workspace_services
    GROUP BY workspace_id, service_type
);

-- The unique index covers lookups by workspace, as the old index did.
DROP INDEX IF EXISTS idx_workspace_services_workspace_id;

CREATE UNIQUE INDEX idx_workspace_services_workspace_type ON workspace_services(workspace_id, service_type);
