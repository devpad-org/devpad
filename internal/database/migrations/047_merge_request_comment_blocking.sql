-- A comment that starts a thread says whether its problem blocks merging.
-- Review agents mark each one, and a user's comments block when their review
-- requests changes. An agent review requests changes exactly when a blocking
-- thread is open. Threads opened before this by a review requesting changes
-- are taken as blocking.

ALTER TABLE merge_request_comments ADD COLUMN blocking INTEGER NOT NULL DEFAULT 0;

UPDATE merge_request_comments SET blocking = 1
WHERE reply_to IS NULL
  AND review_id IN (SELECT id FROM merge_request_reviews WHERE verdict = 'request_changes');
