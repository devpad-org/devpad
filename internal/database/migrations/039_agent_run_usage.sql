-- Agent runs total the tokens their provider requests were billed for and,
-- when the model's prices are known, what they cost. cost_usd stays NULL
-- until a priced request is recorded.
ALTER TABLE ai_agent_runs ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ai_agent_runs ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ai_agent_runs ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ai_agent_runs ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ai_agent_runs ADD COLUMN cost_usd REAL;
