DROP TABLE IF EXISTS ai_messages;

CREATE TABLE ai_turns (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL,
    role            TEXT    NOT NULL CHECK(role IN ('system','user','assistant')),
    UNIQUE(conversation_id, position)
);

CREATE INDEX idx_ai_turns_conversation ON ai_turns(conversation_id, position);

CREATE TABLE ai_parts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id         INTEGER NOT NULL REFERENCES ai_turns(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL,
    kind            TEXT    NOT NULL CHECK(kind IN ('text','thinking','tool_call','tool_result')),
    text                   TEXT    NOT NULL DEFAULT '',
    thinking_state         TEXT    NOT NULL DEFAULT '',
    tool_call_id           TEXT    NOT NULL DEFAULT '',
    tool_call_name         TEXT    NOT NULL DEFAULT '',
    tool_call_args         TEXT    NOT NULL DEFAULT '',
    tool_result_call_id    TEXT    NOT NULL DEFAULT '',
    tool_result_name       TEXT    NOT NULL DEFAULT '',
    tool_result_content    TEXT    NOT NULL DEFAULT '',
    tool_result_is_error   INTEGER NOT NULL DEFAULT 0,
    UNIQUE(turn_id, position)
);
