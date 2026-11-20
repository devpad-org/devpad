-- The sidecar services a project's workspaces get. Each new sandbox and
-- project workspace gets its own fresh copy of each, never one shared with
-- another workspace; the project workspace gains ones added later when it is
-- rebuilt. license_acceptance is the JSON record of the user accepting the
-- service's licence terms, for a service that has them; each copy is made
-- under it.
CREATE TABLE project_services (
    project_id         INTEGER  NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    service_type       TEXT     NOT NULL,
    license_acceptance TEXT,
    created_at         DATETIME NOT NULL,
    -- UNIQUE rather than the primary key, so a duplicate is a UNIQUE
    -- violation (database.IsUniqueViolation).
    UNIQUE (project_id, service_type)
);

-- A project starts with the services its project workspace already has, so
-- its sandboxes get what the user works with.
INSERT OR IGNORE INTO project_services (project_id, service_type, license_acceptance, created_at)
SELECT w.project_id, s.service_type, json_extract(s.config, '$.licenseAcceptance'), s.created_at
FROM workspace_services s
JOIN workspaces w ON w.id = s.workspace_id
WHERE w.project_id IS NOT NULL AND w.kind = 'interactive';
