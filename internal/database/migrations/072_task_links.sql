-- Links between a project's tasks. A 'blocks' link holds to_task_id back
-- until from_task_id is merged; a 'related' link only points the two at each
-- other, and is stored with the lower id first. Two tasks have at most one
-- link between them, of either kind and in either direction.
CREATE TABLE task_links (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    from_task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    to_task_id   INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind         TEXT    NOT NULL CHECK(kind IN ('blocks', 'related')),
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK(from_task_id <> to_task_id),
    CHECK(kind <> 'related' OR from_task_id < to_task_id)
);

CREATE UNIQUE INDEX idx_task_links_pair ON task_links(MIN(from_task_id, to_task_id), MAX(from_task_id, to_task_id));
CREATE INDEX idx_task_links_project_id ON task_links(project_id);
CREATE INDEX idx_task_links_to_task_id ON task_links(to_task_id);

-- A task to do whose agent the user asked to start while other tasks still
-- blocked it: it queues itself once the last of them is merged.
ALTER TABLE tasks ADD COLUMN start_when_unblocked INTEGER NOT NULL DEFAULT 0;
