-- Projects that review merge requests automatically start a review agent on
-- each one as it opens.
ALTER TABLE projects ADD COLUMN auto_review INTEGER NOT NULL DEFAULT 0;
