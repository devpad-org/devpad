CREATE TABLE ai_parts_new (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id                INTEGER NOT NULL REFERENCES ai_turns(id) ON DELETE CASCADE,
    position               INTEGER NOT NULL,
    kind                   TEXT    NOT NULL CHECK(kind IN ('text','thinking','image','tool_call','tool_result')),
    text                   TEXT    NOT NULL DEFAULT '',
    thinking_state         TEXT    NOT NULL DEFAULT '',
    image_mime_type        TEXT    NOT NULL DEFAULT '',
    image_data             TEXT    NOT NULL DEFAULT '',
    tool_call_id           TEXT    NOT NULL DEFAULT '',
    tool_call_item_id      TEXT    NOT NULL DEFAULT '',
    tool_call_name         TEXT    NOT NULL DEFAULT '',
    tool_call_args         TEXT    NOT NULL DEFAULT '',
    tool_result_call_id    TEXT    NOT NULL DEFAULT '',
    tool_result_name       TEXT    NOT NULL DEFAULT '',
    tool_result_content    TEXT    NOT NULL DEFAULT '',
    tool_result_is_error   INTEGER NOT NULL DEFAULT 0,
    UNIQUE(turn_id, position)
);

INSERT INTO ai_parts_new (
    id, turn_id, position, kind, text, thinking_state,
    tool_call_id, tool_call_item_id, tool_call_name, tool_call_args,
    tool_result_call_id, tool_result_name, tool_result_content, tool_result_is_error
)
SELECT
    id, turn_id, position, kind, text, thinking_state,
    tool_call_id, tool_call_item_id, tool_call_name, tool_call_args,
    tool_result_call_id, tool_result_name, tool_result_content, tool_result_is_error
FROM ai_parts;

DROP TABLE ai_parts;
ALTER TABLE ai_parts_new RENAME TO ai_parts;
