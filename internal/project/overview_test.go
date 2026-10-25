package project

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/auth"
)

func execSQL(t *testing.T, e *testEnv, query string, args ...any) int64 {
	t.Helper()
	res, err := e.db.Conn().Exec(query, args...)
	if err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestOverviews(t *testing.T) {
	e := setupTest(t)
	ctx := context.Background()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	busy := execSQL(t, e, `INSERT INTO projects (user_id, name, status, created_at, updated_at) VALUES (?, 'busy', 'ready', ?, ?)`, e.userID, old, old)
	quiet := execSQL(t, e, `INSERT INTO projects (user_id, name, status, created_at, updated_at) VALUES (?, 'quiet', 'ready', ?, ?)`, e.userID, old, old)
	foreign := execSQL(t, e, `INSERT INTO projects (user_id, name, status) VALUES (?, 'foreign', 'ready')`, e.otherID)

	// Workspaces: the project workspace and a stopped sandbox don't count as
	// running agents; a running task and review sandbox do.
	execSQL(t, e, `INSERT INTO workspaces (user_id, name, status, project_id, kind, branch) VALUES (?, 'mine', 'running', ?, 'interactive', '')`, e.userID, busy)
	taskWS := execSQL(t, e, `INSERT INTO workspaces (user_id, name, status, project_id, kind, branch) VALUES (?, 't', 'running', ?, 'task', 'task/1')`, e.userID, busy)
	reviewWS := execSQL(t, e, `INSERT INTO workspaces (user_id, name, status, project_id, kind, branch) VALUES (?, 'r', 'running', ?, 'review', 'review/1')`, e.userID, busy)
	execSQL(t, e, `INSERT INTO workspaces (user_id, name, status, project_id, kind, branch) VALUES (?, 's', 'stopped', ?, 'task', 'task/2')`, e.userID, busy)
	execSQL(t, e, `INSERT INTO workspaces (user_id, name, status) VALUES (?, 'standalone', 'running')`, e.userID)

	working := execSQL(t, e, `INSERT INTO tasks (project_id, user_id, title, status, workspace_id, updated_at) VALUES (?, ?, 'working', 'in_progress', ?, ?)`, busy, e.userID, taskWS, recent)
	execSQL(t, e, `INSERT INTO tasks (project_id, user_id, title, status) VALUES (?, ?, 'waiting', 'queued')`, busy, e.userID)
	reviewed := execSQL(t, e, `INSERT INTO tasks (project_id, user_id, title, status) VALUES (?, ?, 'done', 'in_review')`, busy, e.userID)
	execSQL(t, e, `INSERT INTO tasks (project_id, user_id, title, status) VALUES (?, ?, 'merged', 'merged')`, quiet, e.userID)

	mr := func(task int64, branch, head string) int64 {
		var taskID any
		if task != 0 {
			taskID = task
		}
		return execSQL(t, e, `INSERT INTO merge_requests (project_id, user_id, task_id, title, source_branch, target_branch, head_sha) VALUES (?, ?, ?, 'mr', ?, 'main', ?)`, busy, e.userID, taskID, branch, head)
	}
	// Waits on the user: nobody has reviewed it.
	mr(reviewed, "task/3", "aaa")
	// Waits on the user: their review is of an older head.
	stale := mr(0, "feature/a", "bbb")
	execSQL(t, e, `INSERT INTO merge_request_reviews (merge_request_id, author, status, verdict, head_sha) VALUES (?, 'user', 'submitted', 'comment', 'old')`, stale)
	// Not waiting: the user reviewed this head.
	done := mr(0, "feature/b", "ccc")
	execSQL(t, e, `INSERT INTO merge_request_reviews (merge_request_id, author, status, verdict, head_sha) VALUES (?, 'user', 'submitted', 'request_changes', 'ccc')`, done)
	// Not waiting: an agent is reviewing it.
	agentReviewing := mr(0, "feature/c", "ddd")
	execSQL(t, e, `INSERT INTO merge_request_reviews (merge_request_id, author, status, head_sha, workspace_id) VALUES (?, 'agent', 'pending', 'ddd', ?)`, agentReviewing, reviewWS)
	// Not waiting: the task's agent is reworking it.
	mr(working, "task/1", "eee")
	// Closed merge requests don't count at all.
	closed := mr(0, "feature/d", "fff")
	execSQL(t, e, `UPDATE merge_requests SET status = 'closed' WHERE id = ?`, closed)

	overviews, links, err := e.svc.Overviews(ctx, e.userID)
	if err != nil {
		t.Fatalf("Overviews: %v", err)
	}
	if len(overviews) != 2 {
		t.Fatalf("expected 2 overviews (foreign project %d excluded), got %d", foreign, len(overviews))
	}
	byID := map[int64]*Overview{}
	for _, o := range overviews {
		byID[o.ProjectID] = o
	}
	b := byID[busy]
	if b == nil {
		t.Fatal("missing busy project")
	}
	if b.RunningAgents != 2 || b.ActiveTasks != 2 || b.OpenMergeRequests != 5 || b.AwaitingReview != 2 {
		t.Fatalf("busy overview = %+v", b)
	}
	if b.LastActivityAt.Before(recent) {
		t.Fatalf("busy last activity = %v, want at least %v", b.LastActivityAt, recent)
	}
	q := byID[quiet]
	if q == nil || q.RunningAgents != 0 || q.ActiveTasks != 0 || q.OpenMergeRequests != 0 || q.AwaitingReview != 0 {
		t.Fatalf("quiet overview = %+v", q)
	}
	if !q.LastActivityAt.After(old) {
		t.Fatalf("quiet last activity %v should come from its task, after %v", q.LastActivityAt, old)
	}

	linkByWS := map[int64]*WorkspaceLink{}
	for _, l := range links {
		linkByWS[l.WorkspaceID] = l
	}
	if len(links) != 4 {
		t.Fatalf("expected 4 project workspace links (standalone excluded), got %d", len(links))
	}
	if l := linkByWS[taskWS]; l == nil || l.TaskID != working || l.ProjectID != busy || l.Kind != "task" {
		t.Fatalf("task sandbox link = %+v", l)
	}
	if l := linkByWS[reviewWS]; l == nil || l.MergeRequestID != agentReviewing || l.Kind != "review" {
		t.Fatalf("review sandbox link = %+v", l)
	}
}

func TestHandleOverview(t *testing.T) {
	e := setupTest(t)
	p := execSQL(t, e, `INSERT INTO projects (user_id, name, status) VALUES (?, 'p', 'ready')`, e.userID)
	execSQL(t, e, `INSERT INTO merge_requests (project_id, user_id, title, source_branch, target_branch, head_sha) VALUES (?, ?, 'mr', 'x', 'main', 'abc')`, p, e.userID)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/overview", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: e.userID}))
	rec := httptest.NewRecorder()
	NewHandler(e.svc).HandleOverview(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Projects []struct {
			ProjectID      int64 `json:"projectId"`
			AwaitingReview int   `json:"awaitingReview"`
		} `json:"projects"`
		Workspaces []any `json:"workspaces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Projects) != 1 || body.Projects[0].ProjectID != p || body.Projects[0].AwaitingReview != 1 {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if body.Workspaces == nil {
		t.Fatal("workspaces should be an empty list, not null")
	}
}
