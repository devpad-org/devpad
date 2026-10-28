-- A project counts the changes to its upstream URL. The git bridge sends the
-- count to the project workspace, which forgets the old upstream's
-- refs/remotes/origin/* when it changes, and a workspace converted in place
-- is sent a hash of the upstream URL only while it is still the one given
-- when it was converted.
ALTER TABLE projects ADD COLUMN upstream_version INTEGER NOT NULL DEFAULT 0;
