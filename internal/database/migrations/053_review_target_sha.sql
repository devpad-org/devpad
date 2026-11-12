-- The commit of the target branch an agent review compares the change
-- against, recorded when its sandbox starts. The review's instructions and
-- skills are read from that commit, not from wherever the branch has moved
-- since. Empty for reviews from before it was recorded, and for the user's
-- own reviews.
ALTER TABLE merge_request_reviews ADD COLUMN target_sha TEXT NOT NULL DEFAULT '';
