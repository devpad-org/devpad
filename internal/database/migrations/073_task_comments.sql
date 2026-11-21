-- Comments on a task, by the user, the task's agent, or the assistant in the
-- project's workspace. Each author can edit its own comments, and every
-- version is kept, so an agent cannot hide from the user what it once
-- wrote. body is the latest version and updated_at when it was written.
CREATE TABLE task_comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    INTEGER  NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author     TEXT     NOT NULL CHECK(author IN ('user', 'task_agent', 'assistant')),
    body       TEXT     NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX idx_task_comments_task_id ON task_comments(task_id);

-- Every version of every comment, the first included, oldest first.
CREATE TABLE task_comment_revisions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    comment_id INTEGER  NOT NULL REFERENCES task_comments(id) ON DELETE CASCADE,
    body       TEXT     NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE INDEX idx_task_comment_revisions_comment_id ON task_comment_revisions(comment_id);

-- The database keeps the history itself, so no code path can change a
-- comment without leaving its earlier version behind.
CREATE TRIGGER task_comments_first_revision AFTER INSERT ON task_comments
BEGIN
    INSERT INTO task_comment_revisions (comment_id, body, created_at) VALUES (NEW.id, NEW.body, NEW.created_at);
END;

CREATE TRIGGER task_comments_edit_revision AFTER UPDATE OF body ON task_comments
WHEN NEW.body IS NOT OLD.body
BEGIN
    INSERT INTO task_comment_revisions (comment_id, body, created_at) VALUES (NEW.id, NEW.body, NEW.updated_at);
END;

-- A comment stays where it was written and with who wrote it.
CREATE TRIGGER task_comments_fixed BEFORE UPDATE OF task_id, author, created_at ON task_comments
WHEN NEW.task_id IS NOT OLD.task_id OR NEW.author IS NOT OLD.author OR NEW.created_at IS NOT OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'a task comment''s task, author and creation time cannot change');
END;

-- Comments and their versions go only with their task, which deleting
-- removes by cascade: by then the task, or the comment, is gone.
CREATE TRIGGER task_comments_kept BEFORE DELETE ON task_comments
WHEN EXISTS (SELECT 1 FROM tasks WHERE id = OLD.task_id)
BEGIN
    SELECT RAISE(ABORT, 'task comments cannot be deleted');
END;

CREATE TRIGGER task_comment_revisions_kept BEFORE DELETE ON task_comment_revisions
WHEN EXISTS (SELECT 1 FROM task_comments WHERE id = OLD.comment_id)
BEGIN
    SELECT RAISE(ABORT, 'task comment revisions cannot be deleted');
END;

CREATE TRIGGER task_comment_revisions_fixed BEFORE UPDATE ON task_comment_revisions
BEGIN
    SELECT RAISE(ABORT, 'task comment revisions cannot change');
END;
