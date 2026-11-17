-- Demoting an admin deletes their unused invites, and deleting one cascades
-- to all of theirs: both find them by created_by.
CREATE INDEX idx_user_invites_created_by ON user_invites(created_by);
