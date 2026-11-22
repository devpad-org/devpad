-- Each run of a project's pipeline: on a protected branch's commit, or on a
-- merge request's head merged onto its target's commit. Its jobs come from
-- .devpad/pipeline.yml at the commit they are read at (the branch's, or the
-- merge request's target's), or the project's pipeline setting.
--
-- uid names the pipeline outside the database: its ref in the canonical repo
-- (refs/devpad/pipelines/<uid>) and the Docker resources of its jobs. It is
-- random, so another devpad sharing the Docker daemon never uses the same.
-- ref_pinned says the canonical repo may still hold its ref: it is set
-- before the pin and cleared once the ref is deleted, so a ref a crash left
-- is always found. trigger is what started it; the application decides
-- which there are. announced_at is when every listener had been told it
-- finished, so that a devpad that stopped first tells them after its
-- restart. keeps_output says its jobs' output may still be stored: it is
-- set when the pipeline starts and cleared once the output is pruned, so
-- pruning reads only the pipelines that still keep some.
CREATE TABLE pipelines (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id       INTEGER  NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id          INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    uid              TEXT     NOT NULL UNIQUE,
    kind             TEXT     NOT NULL CHECK(kind IN ('branch', 'merge_request')),
    branch           TEXT     NOT NULL DEFAULT '',
    merge_request_id INTEGER  REFERENCES merge_requests(id) ON DELETE CASCADE,
    source_branch    TEXT     NOT NULL DEFAULT '',
    target_branch    TEXT     NOT NULL DEFAULT '',
    head_sha         TEXT     NOT NULL,
    target_sha       TEXT     NOT NULL DEFAULT '',
    tested_sha       TEXT     NOT NULL DEFAULT '',
    config_source    TEXT     NOT NULL CHECK(config_source IN ('file', 'project', 'none')),
    config_path      TEXT     NOT NULL DEFAULT '',
    config_hash      TEXT     NOT NULL DEFAULT '',
    image            TEXT     NOT NULL DEFAULT '',
    status           TEXT     NOT NULL CHECK(status IN (
        'queued', 'running', 'passed', 'failed', 'error', 'invalid', 'conflict',
        'not_configured', 'cancelled', 'superseded', 'interrupted')),
    error            TEXT     NOT NULL DEFAULT '',
    conflict_paths   TEXT     NOT NULL DEFAULT '[]',
    trigger          TEXT     NOT NULL,
    rerun_of         INTEGER  REFERENCES pipelines(id) ON DELETE SET NULL,
    ref_pinned       INTEGER  NOT NULL DEFAULT 0,
    keeps_output     INTEGER  NOT NULL DEFAULT 0,
    created_at       DATETIME NOT NULL,
    started_at       DATETIME,
    finished_at      DATETIME,
    announced_at     DATETIME,
    CHECK ((kind = 'branch' AND branch != '' AND merge_request_id IS NULL AND target_sha = '')
        OR (kind = 'merge_request' AND branch = '' AND merge_request_id IS NOT NULL AND target_sha != ''))
);

-- A project's, a branch's and a merge request's pipelines, newest first;
-- those of a branch's commit and of a merge request's pair of commits, to
-- find one testing the same.
CREATE INDEX idx_pipelines_project ON pipelines(project_id, id);
CREATE INDEX idx_pipelines_branch ON pipelines(project_id, branch, id) WHERE kind = 'branch';
CREATE INDEX idx_pipelines_merge_request ON pipelines(merge_request_id, id);
CREATE INDEX idx_pipelines_branch_commit ON pipelines(project_id, branch, head_sha, id) WHERE kind = 'branch';
CREATE INDEX idx_pipelines_merge_request_commits ON pipelines(merge_request_id, head_sha, target_sha, id);
-- The few pipelines each sweep or prune is after, so none reads the rest;
-- and those a pipeline runs again, so deleting one is no scan.
CREATE INDEX idx_pipelines_unfinished ON pipelines(id) WHERE status IN ('queued', 'running');
CREATE INDEX idx_pipelines_pinned ON pipelines(id) WHERE ref_pinned = 1;
CREATE INDEX idx_pipelines_unannounced ON pipelines(id) WHERE announced_at IS NULL AND status NOT IN ('queued', 'running');
CREATE INDEX idx_pipelines_keeping_output ON pipelines(project_id, id) WHERE keeps_output = 1;
CREATE INDEX idx_pipelines_rerun_of ON pipelines(rerun_of) WHERE rerun_of IS NOT NULL;

-- A pipeline's jobs, in the order its file lists them, each as it was
-- resolved when the pipeline started. output is the end of what the job
-- printed, as raw bytes, and output_bytes all it printed: output is empty
-- with output_bytes above zero once it is no longer kept. notes are
-- devpad's own notes on the job, kept apart from its output so that the job
-- cannot forge one: a JSON list of {"at", "text"}.
CREATE TABLE pipeline_jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    pipeline_id  INTEGER  NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
    position     INTEGER  NOT NULL,
    name         TEXT     NOT NULL,
    run          TEXT     NOT NULL,
    timeout_ms   INTEGER  NOT NULL,
    services     TEXT     NOT NULL DEFAULT '[]',
    status       TEXT     NOT NULL CHECK(status IN (
        'queued', 'running', 'passed', 'failed', 'timed_out', 'out_of_memory',
        'error', 'cancelled', 'skipped', 'interrupted')),
    exit_code    INTEGER,
    error        TEXT     NOT NULL DEFAULT '',
    output       BLOB     NOT NULL DEFAULT x'',
    output_bytes INTEGER  NOT NULL DEFAULT 0,
    notes        TEXT     NOT NULL DEFAULT '[]',
    started_at   DATETIME,
    finished_at  DATETIME,
    UNIQUE (pipeline_id, name),
    UNIQUE (pipeline_id, position)
);
