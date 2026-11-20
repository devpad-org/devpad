-- The model and reasoning effort an admin set for a built-in agent, which
-- every model picker for that agent starts on. agent_id is a built-in
-- agent's ID; an agent without a row uses devpad's own default. An empty
-- effort means the model's default effort.
CREATE TABLE ai_agent_defaults (
    agent_id   TEXT     PRIMARY KEY,
    model      TEXT     NOT NULL,
    effort     TEXT     NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
