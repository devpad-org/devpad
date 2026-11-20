-- GPT-5.4 and GPT-5.5 left the model catalog for the GPT-6 models. Move every
-- saved choice of them that devpad still acts on, as 052 did for Claude: an
-- agent default would otherwise be skipped without a word, and a task or
-- pending review would fail to start. Closed tasks and finished reviews keep
-- the model they ran on.
--
-- Agents, tasks and pending reviews move to GPT-6.2 Sol, whose input, output
-- and cached input prices are below both models'. Sol takes every effort they
-- did, so saved efforts stay. It always reasons, so a GPT-5.4 choice with no
-- effort, which did not, now also pays for reasoning at Sol's default effort.
UPDATE user_ai_agent_defaults
   SET model = 'gpt-6.2-sol',
       updated_at = CURRENT_TIMESTAMP
 WHERE model IN ('gpt-5.4', 'gpt-5.5');

-- File summaries ask for no effort, so on Sol each would pay for reasoning, and
-- one with little output could cost more than before. They go to automatic, the
-- cheapest priced model the user has a key for.
UPDATE user_ai_settings
   SET summary_model = '',
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
