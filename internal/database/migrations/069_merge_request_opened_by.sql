-- Who opened a merge request: the user, a task's agent finishing (devpad
-- opens it for the task), or the assistant in the project's workspace with
-- the user's approval.
ALTER TABLE merge_requests ADD COLUMN opened_by TEXT NOT NULL DEFAULT 'user'
    CHECK(opened_by IN ('user','task_agent','assistant'));

UPDATE merge_requests SET opened_by = 'task_agent' WHERE task_id IS NOT NULL;
