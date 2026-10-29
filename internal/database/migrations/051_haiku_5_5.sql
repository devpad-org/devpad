-- Claude Haiku 4.5 left the model catalog for Claude Haiku 5.5. Move every
-- saved choice of it that devpad still acts on: an agent default would
-- otherwise be skipped without a word, so a sub-agent or review meant for
-- Haiku would run on a costlier model, and a task or pending review would
-- fail to start. Closed tasks and finished reviews keep the model they ran
-- on. Haiku 4.5 took no effort, so every saved effort is already the empty
-- default, which Haiku 5.5 reads as medium.
UPDATE ai_agent_defaults
   SET model = 'claude-haiku-5-5', updated_at = CURRENT_TIMESTAMP
 WHERE model = 'claude-haiku-4-5';

UPDATE tasks
   SET model = 'claude-haiku-5-5'
 WHERE model = 'claude-haiku-4-5'
   AND status NOT IN ('merged', 'cancelled');

UPDATE merge_request_reviews
   SET model = 'claude-haiku-5-5'
 WHERE model = 'claude-haiku-4-5'
   AND status = 'pending';
