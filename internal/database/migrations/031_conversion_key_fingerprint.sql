-- A conversion records the fingerprint of the SSH key its workspace held, so
-- one resumed after a lost progress write rotates the key only while it is
-- still that key, instead of replacing a key the user may already have added
-- upstream. It also keeps the last progress write that failed, so the user
-- is told a step may have run twice.
ALTER TABLE project_conversions ADD COLUMN old_key_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE project_conversions ADD COLUMN progress_error TEXT NOT NULL DEFAULT '';
