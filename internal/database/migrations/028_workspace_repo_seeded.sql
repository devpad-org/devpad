-- A project workspace or sandbox is only usable once devpad has seeded its
-- repo. Recording that lets devpad spot one left half-made by an interrupted
-- open or a crash, and replace it. Workspaces that already exist were seeded
-- or never need it.

ALTER TABLE workspaces ADD COLUMN repo_seeded INTEGER NOT NULL DEFAULT 0;

UPDATE workspaces SET repo_seeded = 1;
