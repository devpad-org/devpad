-- Which round of the code and review loop an agent review is: a review
-- started by hand or on a new merge request is the first, and each review
-- the task's agent answers with new commits starts the next, up to three.
ALTER TABLE merge_request_reviews ADD COLUMN round INTEGER NOT NULL DEFAULT 1;
