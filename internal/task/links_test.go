package task

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/auth"
	appdb "github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/project"
)

func (e *env) createNamed(t *testing.T, title string, in CreateInput) *Task {
	t.Helper()
	in.Title = title
	if in.Model == "" {
		in.Model = "claude-opus-5-5"
	}
	task, err := e.svc.Create(context.Background(), owner, 1, in)
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return task
}

func (e *env) link(t *testing.T, taskID int64, kind LinkKind, otherID int64) {
	t.Helper()
	if _, err := e.svc.Link(context.Background(), owner, 1, taskID, kind, otherID); err != nil {
		t.Fatalf("link #%d %s #%d: %v", taskID, kind, otherID, err)
	}
}

// stays checks that a task is still in the state it is in a while later,
// after any background work a call set off has had time to run.
func (e *env) stays(t *testing.T, id int64, cond func(*Task) bool) *Task {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	got, _ := e.repo.GetByID(context.Background(), id)
	if got == nil || !cond(got) {
		t.Fatalf("task %d changed unexpectedly: %+v", id, got)
	}
	return got
}

func waiting(t *Task) bool { return t.Status == StatusTodo && t.StartWhenUnblocked }

func TestBlockedTaskStartsOnceItsBlockerIsMerged(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	schema := e.createNamed(t, "Add the users table", CreateInput{})
	page := e.createNamed(t, "Build the profile page", CreateInput{})
	e.link(t, page.ID, LinkBlockedBy, schema.ID)

	started, err := e.svc.Start(ctx, owner, 1, page.ID, StartInput{Message: "Use the new table."})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waiting(started) || started.PendingMessage != "Use the new table." {
		t.Fatalf("expected the blocked task left to start when unblocked: %+v", started)
	}
	e.stays(t, page.ID, waiting)
	if got := e.sandboxes.snapshot().created; len(got) != 0 {
		t.Fatalf("a blocked task got a sandbox: %+v", got)
	}

	if _, err := e.svc.Merged(ctx, owner, 1, schema.ID); err != nil {
		t.Fatalf("merge blocker: %v", err)
	}
	running := e.waitTask(t, page.ID, hasRun)
	if running.StartWhenUnblocked {
		t.Fatalf("a started task still waits: %+v", running)
	}
	prompt := e.runs.startedRequests()[0].Message.Text()
	for _, want := range []string{"## Linked tasks", "- #1 Add the users table (merged)", "Use the new table."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBlockedTaskNotStartedStaysToDo(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	first := e.createNamed(t, "First", CreateInput{})
	second := e.createNamed(t, "Second", CreateInput{BlockedBy: []int64{first.ID}})
	if _, err := e.svc.Merged(ctx, owner, 1, first.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	e.stays(t, second.ID, status(StatusTodo))

	// Once unblocked, Start queues it at once.
	e.start(t, second.ID, StartInput{})
	e.waitTask(t, second.ID, hasRun)
}

func TestTaskWaitsForEveryBlocker(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.createNamed(t, "A", CreateInput{})
	b := e.createNamed(t, "B", CreateInput{})
	c := e.createNamed(t, "C", CreateInput{BlockedBy: []int64{a.ID, b.ID}, Start: true})
	if !waiting(c) {
		t.Fatalf("expected a task created to start while blocked to wait: %+v", c)
	}
	if _, err := e.svc.Merged(ctx, owner, 1, a.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	e.stays(t, c.ID, waiting)
	if _, err := e.svc.Merged(ctx, owner, 1, b.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	e.waitTask(t, c.ID, hasRun)
}

func TestCancelledBlockerHoldsUntilTheLinkGoes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	blocker := e.createNamed(t, "Blocker", CreateInput{})
	blocked := e.createNamed(t, "Blocked", CreateInput{BlockedBy: []int64{blocker.ID}, Start: true})
	if _, err := e.svc.Cancel(ctx, owner, 1, blocker.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	e.stays(t, blocked.ID, waiting)

	got, err := e.svc.Unlink(ctx, owner, 1, blocked.ID, blocker.ID)
	if err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if got.Status != StatusQueued && got.Status != StatusInProgress {
		t.Fatalf("expected the unlinked task queued, got %+v", got)
	}
	e.waitTask(t, blocked.ID, hasRun)
	if _, err := e.svc.Unlink(ctx, owner, 1, blocked.ID, blocker.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unlinking tasks not linked: expected ErrInvalidInput, got %v", err)
	}
}

func TestDeletingABlockerFreesTheTasksItBlocked(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	blocker := e.createNamed(t, "Blocker", CreateInput{})
	blocked := e.createNamed(t, "Blocked", CreateInput{BlockedBy: []int64{blocker.ID}, Start: true})
	if err := e.svc.Delete(ctx, owner, 1, blocker.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	e.waitTask(t, blocked.ID, hasRun)
	links, err := e.svc.Links(ctx, owner, 1)
	if err != nil || len(links) != 0 {
		t.Fatalf("expected the deleted task's links gone: %v %v", links, err)
	}
}

func TestCancelStartLeavesTheTaskToDo(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	blocker := e.createNamed(t, "Blocker", CreateInput{})
	blocked := e.createNamed(t, "Blocked", CreateInput{BlockedBy: []int64{blocker.ID}, Start: true})
	got, err := e.svc.CancelStart(ctx, owner, 1, blocked.ID)
	if err != nil || got.StartWhenUnblocked {
		t.Fatalf("cancel start: %+v %v", got, err)
	}
	if _, err := e.svc.Merged(ctx, owner, 1, blocker.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	e.stays(t, blocked.ID, func(t *Task) bool { return t.Status == StatusTodo && !t.StartWhenUnblocked })
}

func TestUnblockedTaskThatCannotStartRecordsWhy(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	blocker := e.createNamed(t, "Blocker", CreateInput{})
	blocked := e.createNamed(t, "Blocked", CreateInput{BlockedBy: []int64{blocker.ID}, Start: true})
	e.sandboxes.set(func(f *fakeSandboxes) { f.project.Status = project.StatusCreating })
	if _, err := e.svc.Merged(ctx, owner, 1, blocker.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	got := e.waitTask(t, blocked.ID, func(t *Task) bool { return !t.StartWhenUnblocked })
	if got.Status != StatusTodo || !strings.Contains(got.Error, "could not start") {
		t.Fatalf("expected the failed start recorded on the task: %+v", got)
	}
}

func TestReconcileStartsTasksABlockerMergedBefore(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	blocker := e.createNamed(t, "Blocker", CreateInput{})
	blocked := e.createNamed(t, "Blocked", CreateInput{BlockedBy: []int64{blocker.ID}, Start: true})
	// The merge was saved, but devpad stopped before it unblocked anything.
	stored, _ := e.repo.GetByID(ctx, blocker.ID)
	stored.Status = StatusMerged
	e.repo.Update(ctx, stored)
	e.stays(t, blocked.ID, waiting)

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	e.waitTask(t, blocked.ID, hasRun)
}

func TestLinkRules(t *testing.T) {
	e := setup(t)
	e.sandboxes.set(func(f *fakeSandboxes) { f.branches = []string{"feature"} })
	ctx := context.Background()
	a := e.createNamed(t, "A", CreateInput{})
	b := e.createNamed(t, "B", CreateInput{})
	c := e.createNamed(t, "C", CreateInput{})
	d := e.createNamed(t, "D", CreateInput{})
	// Tasks merge into main whatever they start from.
	onFeature := e.createNamed(t, "On feature", CreateInput{BaseBranch: "feature"})
	alsoOnFeature := e.createNamed(t, "Also on feature", CreateInput{BaseBranch: "feature"})
	started := e.createNamed(t, "Started", CreateInput{})
	e.start(t, started.ID, StartInput{})
	cancelled := e.createNamed(t, "Cancelled", CreateInput{})
	if _, err := e.svc.Cancel(ctx, owner, 1, cancelled.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	elsewhere := &Task{ProjectID: 2, UserID: owner, Title: "Elsewhere", Status: StatusTodo}
	e.repo.Create(ctx, elsewhere)

	e.link(t, a.ID, LinkBlocks, b.ID)
	e.link(t, b.ID, LinkBlocks, c.ID)

	tests := []struct {
		name    string
		userID  int64
		taskID  int64
		kind    LinkKind
		otherID int64
		want    error
	}{
		{"itself", owner, a.ID, LinkRelated, a.ID, ErrInvalidInput},
		{"unknown kind", owner, a.ID, "duplicates", c.ID, ErrInvalidInput},
		{"a cycle", owner, c.ID, LinkBlocks, a.ID, ErrInvalidInput},
		{"a direct cycle", owner, a.ID, LinkBlockedBy, b.ID, ErrInvalidInput},
		{"already linked another way", owner, b.ID, LinkRelated, a.ID, ErrAlreadyLinked},
		{"blocking a started task", owner, started.ID, LinkBlockedBy, a.ID, ErrInvalidState},
		{"blocked by a cancelled task", owner, a.ID, LinkBlockedBy, cancelled.ID, ErrInvalidState},
		{"blocking a task that starts from another branch", owner, onFeature.ID, LinkBlockedBy, d.ID, ErrInvalidInput},
		{"blocking it the other way", owner, d.ID, LinkBlocks, onFeature.ID, ErrInvalidInput},
		{"both starting from a branch neither merges into", owner, onFeature.ID, LinkBlocks, alsoOnFeature.ID, ErrInvalidInput},
		{"related across branches", owner, onFeature.ID, LinkRelated, alsoOnFeature.ID, nil},
		{"blocking a task that starts from where it merges", owner, onFeature.ID, LinkBlocks, d.ID, nil},
		{"another project's task", owner, a.ID, LinkRelated, elsewhere.ID, ErrInvalidInput},
		{"another user's task", stranger, a.ID, LinkRelated, c.ID, ErrNotFound},
		{"a started task blocking", owner, started.ID, LinkBlocks, a.ID, nil},
		{"related to a started task", owner, started.ID, LinkRelated, c.ID, nil},
		{"related to a cancelled task", owner, cancelled.ID, LinkRelated, a.ID, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.Link(ctx, tt.userID, 1, tt.taskID, tt.kind, tt.otherID)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}

	links, err := e.svc.Links(ctx, owner, 1)
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	if got := links[a.ID]; len(got.BlockedBy) != 1 || got.BlockedBy[0] != started.ID || len(got.Blocks) != 1 || got.Blocks[0] != b.ID || len(got.Related) != 1 || got.Related[0] != cancelled.ID || !got.Blocked {
		t.Fatalf("unexpected links of A: %+v", got)
	}
	if got := links[c.ID]; len(got.Related) != 1 || got.Related[0] != started.ID || !got.Blocked {
		t.Fatalf("unexpected links of C: %+v", got)
	}
}

func TestCreateChecksItsLinksFirst(t *testing.T) {
	e := setup(t)
	e.sandboxes.set(func(f *fakeSandboxes) { f.branches = []string{"feature"} })
	ctx := context.Background()
	a := e.createNamed(t, "A", CreateInput{})
	cancelled := e.createNamed(t, "Cancelled", CreateInput{})
	if _, err := e.svc.Cancel(ctx, owner, 1, cancelled.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	for name, in := range map[string]CreateInput{
		"an unknown task":   {BlockedBy: []int64{99}},
		"a cancelled task":  {BlockedBy: []int64{cancelled.ID}},
		"one task twice":    {BlockedBy: []int64{a.ID}, Related: []int64{a.ID}},
		"and starting":      {BlockedBy: []int64{99}, Start: true},
		"a related unknown": {Related: []int64{99}},
		"another base":      {BlockedBy: []int64{a.ID}, BaseBranch: "feature"},
	} {
		in.Title, in.Model = "New", "claude-opus-5-5"
		if _, err := e.svc.Create(ctx, owner, 1, in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if tasks, _ := e.svc.List(ctx, owner, 1); len(tasks) != 2 {
		t.Fatalf("a refused create left a task behind: %d tasks", len(tasks))
	}

	related := e.createNamed(t, "Related", CreateInput{Related: []int64{a.ID}})
	links, _ := e.svc.Links(ctx, owner, 1)
	if got := links[related.ID]; len(got.Related) != 1 || got.Related[0] != a.ID || got.Blocked {
		t.Fatalf("unexpected links: %+v", got)
	}
}

// A task is listed as soon as it is stored, so before Create links it,
// another task may already have been made to wait for it.
func TestNewTaskLinksRefuseACycleMadeMeanwhile(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	newTask := e.createNamed(t, "New", CreateInput{})
	x := e.createNamed(t, "X", CreateInput{})
	y := e.createNamed(t, "Y", CreateInput{})
	e.link(t, newTask.ID, LinkBlocks, x.ID)
	e.link(t, x.ID, LinkBlocks, y.ID)

	err := e.svc.linkNew(ctx, newTask, []pendingLink{{other: y, blocks: true}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected the cycle to be refused, got %v", err)
	}
	if links, _ := e.repo.LinksOf(ctx, y.ID); len(links) != 1 {
		t.Fatalf("a refused link was stored: %+v", links)
	}
}

func TestLinkHandlers(t *testing.T) {
	e := setup(t)
	h := NewHandler(e.svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{id}/tasks", h.HandleCreate)
	mux.HandleFunc("GET /api/projects/{id}/tasks", h.HandleList)
	mux.HandleFunc("POST /api/projects/{id}/tasks/{taskId}/start", h.HandleStart)
	mux.HandleFunc("POST /api/projects/{id}/tasks/{taskId}/cancel-start", h.HandleCancelStart)
	mux.HandleFunc("POST /api/projects/{id}/tasks/{taskId}/links", h.HandleLink)
	mux.HandleFunc("DELETE /api/projects/{id}/tasks/{taskId}/links/{otherId}", h.HandleUnlink)
	do := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: owner}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var res struct {
			Task map[string]any `json:"task"`
		}
		json.Unmarshal(rec.Body.Bytes(), &res)
		return rec.Code, res.Task
	}

	do(http.MethodPost, "/api/projects/1/tasks", `{"title":"A","model":"claude-opus-5-5"}`)
	code, b := do(http.MethodPost, "/api/projects/1/tasks", `{"title":"B","model":"claude-opus-5-5","start":true,"blockedBy":[1]}`)
	if code != http.StatusCreated || b["status"] != "todo" || b["startWhenUnblocked"] != true || b["blocked"] != true {
		t.Fatalf("create blocked: %d %v", code, b)
	}
	if got, _ := json.Marshal(b["blockedBy"]); string(got) != "[1]" {
		t.Fatalf("unexpected blockedBy: %s", got)
	}
	if got, _ := json.Marshal(b["pipelineRounds"]); string(got) != `{"count":0,"held":"","limit":2}` {
		t.Fatalf("unexpected pipelineRounds: %s", got)
	}
	code, a := do(http.MethodPost, "/api/projects/1/tasks/1/links", `{"kind":"related","taskId":2}`)
	if code != http.StatusConflict {
		t.Fatalf("second link: %d %v", code, a)
	}
	if code, b = do(http.MethodPost, "/api/projects/1/tasks/2/cancel-start", `{}`); code != http.StatusOK || b["startWhenUnblocked"] != false {
		t.Fatalf("cancel start: %d %v", code, b)
	}
	if code, a = do(http.MethodDelete, "/api/projects/1/tasks/1/links/2", ""); code != http.StatusOK || a["blocks"] == nil {
		t.Fatalf("unlink: %d %v", code, a)
	}
	if got, _ := json.Marshal(a["blocks"]); string(got) != "[]" {
		t.Fatalf("expected no links left: %s", got)
	}
	if code, a = do(http.MethodPost, "/api/projects/1/tasks/1/links", `{"kind":"blocks","taskId":2}`); code != http.StatusOK {
		t.Fatalf("link: %d %v", code, a)
	}
	if code, _ = do(http.MethodPost, "/api/projects/1/tasks/1/links", `{"kind":"blocks","taskId":7}`); code != http.StatusBadRequest {
		t.Fatalf("link to an unknown task: %d", code)
	}
}

func TestLinkRepositoryAgainstMigratedSchema(t *testing.T) {
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn := db.Conn()
	ctx := context.Background()
	res, err := conn.Exec(`INSERT INTO users (username, email, password) VALUES ('u', 'u@example.com', 'x')`)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	userID, _ := res.LastInsertId()
	res, err = conn.Exec(`INSERT INTO projects (user_id, name, status) VALUES (?, 'p', 'ready')`, userID)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	projectID, _ := res.LastInsertId()

	repo := NewRepository(conn)
	var tasks []*Task
	for i := 0; i < 3; i++ {
		task := &Task{ProjectID: projectID, UserID: userID, Title: "t", Status: StatusTodo, StartWhenUnblocked: i == 1}
		if err := repo.Create(ctx, task); err != nil {
			t.Fatalf("create: %v", err)
		}
		tasks = append(tasks, task)
	}
	a, b, c := tasks[0], tasks[1], tasks[2]
	if got, _ := repo.GetByID(ctx, b.ID); !got.StartWhenUnblocked {
		t.Fatal("start_when_unblocked was not stored")
	}
	if err := repo.CreateLink(ctx, projectID, Link{From: a.ID, To: b.ID, Blocks: true}); err != nil {
		t.Fatalf("blocks link: %v", err)
	}
	if err := repo.CreateLink(ctx, projectID, Link{From: b.ID, To: a.ID, Blocks: true}); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("reversed link: expected ErrAlreadyLinked, got %v", err)
	}
	if err := repo.CreateLink(ctx, projectID, Link{From: a.ID, To: b.ID}); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("related link on a blocked pair: expected ErrAlreadyLinked, got %v", err)
	}
	if err := repo.CreateLink(ctx, projectID, Link{From: c.ID, To: a.ID}); err == nil {
		t.Fatal("a related link must be stored with the lower id first")
	}
	if err := repo.CreateLink(ctx, projectID, Link{From: a.ID, To: c.ID}); err != nil {
		t.Fatalf("related link: %v", err)
	}

	a.Status = StatusMerged
	if err := repo.Update(ctx, a); err != nil {
		t.Fatalf("update: %v", err)
	}
	links, err := repo.LinksOf(ctx, b.ID)
	if err != nil || len(links) != 1 || links[0].FromStatus != StatusMerged || !links[0].Blocks {
		t.Fatalf("links of b: %+v %v", links, err)
	}
	if l := linksOf(b.ID, links); l.Blocked || len(l.BlockedBy) != 1 {
		t.Fatalf("a merged blocker still blocks: %+v", l)
	}

	if err := repo.Delete(ctx, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if links, _ := repo.ListLinks(ctx, projectID); len(links) != 1 {
		t.Fatalf("expected the deleted task's link gone: %+v", links)
	}
	if found, err := repo.DeleteLink(ctx, b.ID, a.ID); err != nil || !found {
		t.Fatalf("delete link: %v %v", found, err)
	}
	if found, _ := repo.DeleteLink(ctx, b.ID, a.ID); found {
		t.Fatal("deleted a link that was gone")
	}
}
