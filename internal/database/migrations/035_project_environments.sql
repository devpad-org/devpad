-- A project's environment is the body of a Dockerfile that devpad builds on
-- its own workspace image; the project's workspaces are made from the
-- result. environment_image is the last image that built, which new
-- workspaces get even while a newer build is failing; empty means devpad's
-- own image.
ALTER TABLE projects ADD COLUMN environment TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN environment_image TEXT NOT NULL DEFAULT '';

-- The image a workspace's container was made from; empty means devpad's
-- own image. (The old image column predates devpad's image and is unused.)
ALTER TABLE workspaces ADD COLUMN container_image TEXT NOT NULL DEFAULT '';

-- The latest build of each project's environment. Its log is the end of the
-- build's output.
CREATE TABLE project_builds (
    project_id  INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    build_id    TEXT    NOT NULL,
    hash        TEXT    NOT NULL,
    image       TEXT    NOT NULL,
    status      TEXT    NOT NULL CHECK(status IN ('building','succeeded','failed')),
    log         TEXT    NOT NULL DEFAULT '',
    error       TEXT    NOT NULL DEFAULT '',
    started_at  DATETIME NOT NULL,
    finished_at DATETIME
);
