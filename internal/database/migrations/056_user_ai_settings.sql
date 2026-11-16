-- AI provider keys, default models and budgets belong to each user, not to
-- the server: a user's runs, their tasks' and reviews' included, use only
-- that user's keys.

-- A user's key for one provider, encrypted with the server's encryption
-- key. A provider is configured for a user exactly when they have a key
-- for it.
CREATE TABLE user_ai_provider_keys (
    user_id     INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id TEXT     NOT NULL,
    api_key     TEXT     NOT NULL,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, provider_id)
);

-- The model and reasoning effort a user set for a built-in agent, which
-- every model picker for that agent starts on. An agent without a row uses
-- devpad's own default. An empty effort means the model's default effort.
CREATE TABLE user_ai_agent_defaults (
    user_id    INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id   TEXT     NOT NULL,
    model      TEXT     NOT NULL,
    effort     TEXT     NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, agent_id)
);

-- What a user's agents may spend, in USD; zero means no limit. A new task
-- starts with task_budget_usd as its budget; review_budget_usd covers one
-- review agent and assistant_budget_usd one reply of the assistant, each
-- with the sub-agents it starts. summary_model is the model summarize_file
-- uses; empty means the cheapest priced model the user has a key for. A
-- user without a row has the column defaults.
CREATE TABLE user_ai_settings (
    user_id              INTEGER  PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    task_budget_usd      REAL     NOT NULL DEFAULT 20,
    review_budget_usd    REAL     NOT NULL DEFAULT 5,
    assistant_budget_usd REAL     NOT NULL DEFAULT 0,
    summary_model        TEXT     NOT NULL DEFAULT '',
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Everyone starts from the defaults and budgets the admin had set. The
-- server's keys are not handed to anyone: each user enters their own.
INSERT INTO user_ai_agent_defaults (user_id, agent_id, model, effort)
SELECT u.id, d.agent_id, d.model, d.effort FROM users u CROSS JOIN ai_agent_defaults d;

INSERT INTO user_ai_settings (user_id, task_budget_usd, review_budget_usd, assistant_budget_usd)
SELECT u.id, b.task_usd, b.review_usd, b.assistant_usd FROM users u CROSS JOIN ai_budget_defaults b;

DROP TABLE IF EXISTS ai_providers;
DROP TABLE IF EXISTS ai_agent_defaults;
DROP TABLE IF EXISTS ai_budget_defaults;
