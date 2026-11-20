-- What an agent review may spend, and has spent, on priced provider
-- requests: its budget starts at the admin's per-review default and can be
-- raised to let a review that ran out continue. A review of a task's merge
-- request also spends from the task's budget. conversation_id is the review
-- agent's transcript, which a continued review picks up.
ALTER TABLE merge_request_reviews ADD COLUMN budget_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE merge_request_reviews ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE merge_request_reviews ADD COLUMN conversation_id INTEGER REFERENCES ai_conversations(id) ON DELETE SET NULL;
