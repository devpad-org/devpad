-- Protected branches move only through merge requests, like the default
-- branch, which is always protected. Merge requests can target any of them,
-- such as main in a project whose default branch is develop. The column
-- holds a JSON array of branch names other than the default.
ALTER TABLE projects ADD COLUMN protected_branches TEXT NOT NULL DEFAULT '[]';
