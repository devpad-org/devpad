-- Projects own a canonical bare repo in an isolated repo container, and any
-- number of sandbox workspaces seeded from it. Sandboxes never touch the repo
-- directly; devpad moves commits between them with git bundles.

CREATE TABLE projects (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name              TEXT    NOT NULL,
    description       TEXT    NOT NULL DEFAULT '',
    default_branch    TEXT    NOT NULL DEFAULT 'main',
    upstream_url      TEXT    NOT NULL DEFAULT '',
    setup_script      TEXT    NOT NULL DEFAULT '',
    max_sandboxes     INTEGER NOT NULL DEFAULT 3 CHECK(max_sandboxes > 0),
    status            TEXT    NOT NULL DEFAULT 'creating' CHECK(status IN ('creating','ready','error')),
    error             TEXT    NOT NULL DEFAULT '',
    repo_container_id TEXT    NOT NULL DEFAULT '',
    repo_volume_name  TEXT    NOT NULL DEFAULT '',
    repo_network_name TEXT    NOT NULL DEFAULT '',
    repo_agent_token  TEXT    NOT NULL DEFAULT '',
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_projects_user_id ON projects(user_id);

-- A project cannot be deleted while it still has sandboxes, so their Docker
-- resources are always removed through the workspace lifecycle first.
ALTER TABLE workspaces ADD COLUMN project_id INTEGER REFERENCES projects(id);
ALTER TABLE workspaces ADD COLUMN kind TEXT NOT NULL DEFAULT 'interactive' CHECK(kind IN ('interactive','task','review'));
ALTER TABLE workspaces ADD COLUMN branch TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_workspaces_project_id ON workspaces(project_id);

-- Each sandbox owns exactly one branch in its project's canonical repo.
CREATE UNIQUE INDEX idx_workspaces_project_branch
    ON workspaces(project_id, branch)
    WHERE project_id IS NOT NULL;
