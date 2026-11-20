-- The name and email a user commits as. Empty means their username or account
-- email. git_identity_saved records that they have saved a choice, even an
-- empty one: until then devpad leaves the identity in existing repos alone.
ALTER TABLE users ADD COLUMN git_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN git_email TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN git_identity_saved BOOLEAN NOT NULL DEFAULT 0;
