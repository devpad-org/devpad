-- The SHA-256 of the file a tool's image was made from, so a run that reads
-- the same image file again refers to the copy it already stored.
ALTER TABLE ai_attachments ADD COLUMN source_sha256 TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_ai_attachments_source ON ai_attachments(source_sha256) WHERE source_sha256 != '';
