-- Images in AI conversations. The bytes live once in ai_attachments; turn
-- parts, run input turns and run events only refer to them.

CREATE TABLE ai_attachments (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Set once a saved conversation refers to the image. Uploads that never
    -- reach one are swept after a day.
    conversation_id INTEGER REFERENCES ai_conversations(id) ON DELETE CASCADE,
    -- Set for an image a tool returned during a run, so it is not swept
    -- while the run's events refer to it. Deleting either the run or the
    -- conversation deletes the image: a run's events can outlive its
    -- conversation, and show the image as unavailable then.
    run_id          INTEGER REFERENCES ai_agent_runs(id) ON DELETE CASCADE,
    media_type      TEXT    NOT NULL CHECK(media_type IN ('image/png','image/jpeg')),
    width           INTEGER NOT NULL,
    height          INTEGER NOT NULL,
    byte_size       INTEGER NOT NULL,
    data            BLOB    NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_ai_attachments_user ON ai_attachments(user_id, created_at);
CREATE INDEX idx_ai_attachments_conversation ON ai_attachments(conversation_id);
CREATE INDEX idx_ai_attachments_run ON ai_attachments(run_id);

-- Add 'image' to the ai_parts kind CHECK, with columns for an image part's
-- reference and a tool result's images. SQLite cannot alter a CHECK
-- constraint, so the table is rebuilt; nothing refers to ai_parts.
CREATE TABLE ai_parts_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id         INTEGER NOT NULL REFERENCES ai_turns(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL,
    kind            TEXT    NOT NULL CHECK(kind IN ('text','thinking','tool_call','tool_result','image')),
    text                   TEXT    NOT NULL DEFAULT '',
    thinking_state         TEXT    NOT NULL DEFAULT '',
    tool_call_id           TEXT    NOT NULL DEFAULT '',
    tool_call_name         TEXT    NOT NULL DEFAULT '',
    tool_call_args         TEXT    NOT NULL DEFAULT '',
    tool_result_call_id    TEXT    NOT NULL DEFAULT '',
    tool_result_name       TEXT    NOT NULL DEFAULT '',
    tool_result_content    TEXT    NOT NULL DEFAULT '',
    tool_result_is_error   INTEGER NOT NULL DEFAULT 0,
    tool_call_item_id      TEXT    NOT NULL DEFAULT '',
    image_ref              TEXT    NOT NULL DEFAULT '',
    tool_result_images     TEXT    NOT NULL DEFAULT '',
    UNIQUE(turn_id, position)
);

INSERT INTO ai_parts_new (
    id, turn_id, position, kind, text, thinking_state,
    tool_call_id, tool_call_name, tool_call_args,
    tool_result_call_id, tool_result_name, tool_result_content, tool_result_is_error,
    tool_call_item_id
)
SELECT
    id, turn_id, position, kind, text, thinking_state,
    tool_call_id, tool_call_name, tool_call_args,
    tool_result_call_id, tool_result_name, tool_result_content, tool_result_is_error,
    tool_call_item_id
FROM ai_parts;

DROP TABLE ai_parts;
ALTER TABLE ai_parts_new RENAME TO ai_parts;
