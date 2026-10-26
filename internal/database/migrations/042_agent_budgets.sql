-- What agents may spend, in USD, as an admin set it. Zero means no limit.
-- A new task starts with task_usd as its budget; review_usd covers one
-- review agent and assistant_usd one reply of the assistant, each with the
-- sub-agents it starts. There is only ever the one row.
CREATE TABLE ai_budget_defaults (
    id            INTEGER  PRIMARY KEY CHECK (id = 1),
    task_usd      REAL     NOT NULL DEFAULT 20,
    review_usd    REAL     NOT NULL DEFAULT 5,
    assistant_usd REAL     NOT NULL DEFAULT 0,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO ai_budget_defaults (id) VALUES (1);

-- A task's budget covers every round of its agent and the sub-agents they
-- start; cost_usd is what they have spent on priced requests so far. Tasks
-- from before budgets have none.
ALTER TABLE tasks ADD COLUMN budget_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0;
