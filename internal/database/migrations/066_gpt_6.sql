-- GPT-5.4 and GPT-5.5 left the model catalog for the GPT-6 models. Move every
-- saved choice of them that devpad still acts on, as 052 did for Claude: an
-- agent default would otherwise be skipped without a word, and a task or
-- pending review would fail to start. Closed tasks and finished reviews keep
-- the model they ran on. Both move to GPT-6.2 Sol, which costs less than either,
-- so that nobody's spending rises unasked. Sol takes every effort they did, so
-- saved efforts stay.
UPDATE user_ai_agent_defaults
   SET model = 'gpt-6.2-sol',
       updated_at = CURRENT_TIMESTAMP
 WHERE model IN ('gpt-5.4', 'gpt-5.5');

UPDATE user_ai_settings
   SET summary_model = 'gpt-6.2-sol',
       updated_at = CURRENT_TIMESTAMP
 WHERE summary_model IN ('gpt-5.4', 'gpt-5.5');

UPDATE tasks
   SET model = 'gpt-6.2-sol'
 WHERE model IN ('gpt-5.4', 'gpt-5.5')
   AND status NOT IN ('merged', 'cancelled');

UPDATE merge_request_reviews
   SET model = 'gpt-6.2-sol'
 WHERE model IN ('gpt-5.4', 'gpt-5.5')
   AND status = 'pending';
