-- Claude Opus 4.7 and Claude Sonnet 4.6 left the model catalog for Claude
-- Opus 5.5 and Claude Sonnet 5.5. Move every saved choice of them that
-- devpad still acts on, as 051 did for Haiku: an agent default would
-- otherwise be skipped without a word, and a task or pending review would
-- fail to start. Closed tasks and finished reviews keep the model they ran
-- on. Each 5.5 model takes every effort its older model did, so saved
-- efforts stay; an empty one now means the 5.5 model's default, medium.
UPDATE ai_agent_defaults
   SET model = CASE model
                 WHEN 'claude-opus-4-7' THEN 'claude-opus-5-5'
                 ELSE 'claude-sonnet-5-5'
               END,
       updated_at = CURRENT_TIMESTAMP
 WHERE model IN ('claude-opus-4-7', 'claude-sonnet-4-6');

UPDATE tasks
   SET model = CASE model
                 WHEN 'claude-opus-4-7' THEN 'claude-opus-5-5'
                 ELSE 'claude-sonnet-5-5'
               END
 WHERE model IN ('claude-opus-4-7', 'claude-sonnet-4-6')
   AND status NOT IN ('merged', 'cancelled');

UPDATE merge_request_reviews
   SET model = CASE model
                 WHEN 'claude-opus-4-7' THEN 'claude-opus-5-5'
                 ELSE 'claude-sonnet-5-5'
               END
 WHERE model IN ('claude-opus-4-7', 'claude-sonnet-4-6')
   AND status = 'pending';
