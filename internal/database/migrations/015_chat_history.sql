CREATE TABLE ai_conversations (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    title        TEXT    NOT NULL DEFAULT '',
    model        TEXT    NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_ai_conversations_user_workspace
    ON ai_conversations(user_id, workspace_id);

CREATE TABLE ai_messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL,
    role            TEXT    NOT NULL,
    content         TEXT    NOT NULL DEFAULT '',
    reasoning_content TEXT  NOT NULL DEFAULT '',
    thinking_state  TEXT    NOT NULL DEFAULT '',
    tool_calls      TEXT    NOT NULL DEFAULT '',
    tool_call_id    TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX idx_ai_messages_conversation
    ON ai_messages(conversation_id, position);
