-- Before conversation history was saved by the server, continuing a
-- conversation from one of its runs linked the new run to the conversation's
-- first run, the link that marks a sub-agent. The run was a message the user
-- sent, and the Agent Runs panel drew it as a sub-agent. A sub-agent always
-- starts from its one prompt, and a continued conversation from its history
-- so far, so a linked run that started from more than one turn was a message.
UPDATE ai_agent_runs
SET parent_run_id = NULL
WHERE parent_run_id IS NOT NULL
  AND json_valid(input_turns)
  AND json_array_length(input_turns) > 1;
