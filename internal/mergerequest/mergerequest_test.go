package mergerequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/domain"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	"github.com/devpad-org/devpad/internal/auth"
	appdb "github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/task"
	"github.com/devpad-org/devpad/internal/workspace"
)

// fakeProjects stands in for the project service. Sandboxes it creates are
// real workspace rows, so review rows can reference them.
type fakeProjects struct {
	mu        sync.Mutex
	db        *sql.DB
	project   project.Project
	branches  []agent.RepoRef
	files     []agent.RepoDiffFile
	sandboxes []project.SandboxInput
	diffs     [][2]string
	merges    []project.MergeInput
	mergeErr  error
	pushes    int
	pushErr   error
}

func (f *fakeProjects) Get(_ context.Context, userID, projectID int64) (*project.Project, error) {
	if projectID != f.project.ID {
		return nil, project.ErrNotFound
	}
	if userID != f.project.UserID {
		return nil, project.ErrForbidden
	}
	p := f.project
	return &p, nil
}

func (f *fakeProjects) Branches(context.Context, int64, int64) (*agent.RepoRefs, error) {
	return &agent.RepoRefs{DefaultBranch: "main", Branches: f.branches}, nil
}

func (f *fakeProjects) Compare(_ context.Context, _, _ int64, base, head string) (*agent.RepoComparison, error) {
	return &agent.RepoComparison{Base: base, Head: head, Ahead: 2, Behind: 1}, nil
}

func (f *fakeProjects) Diff(_ context.Context, _, _ int64, base, head string) (*agent.RepoDiff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.diffs = append(f.diffs, [2]string{base, head})
	return &agent.RepoDiff{BaseSHA: base, HeadSHA: head, MergeBase: "mergebase", Files: f.files}, nil
}

func (f *fakeProjects) CreateSandbox(_ context.Context, userID, projectID int64, in project.SandboxInput) (*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sandboxes = append(f.sandboxes, in)
	res, err := f.db.Exec(`INSERT INTO workspaces (user_id, name, status, project_id, kind, branch) VALUES (?, ?, 'running', ?, ?, ?)`, userID, in.Name, projectID, in.Kind, in.Branch)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &workspace.Workspace{ID: id, UserID: userID, ProjectID: projectID, Kind: workspace.Kind(in.Kind), Branch: in.Branch, Status: workspace.StatusRunning}, nil
}

func (f *fakeProjects) Merge(_ context.Context, _, _ int64, in project.MergeInput) (*agent.RepoMergeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.merges = append(f.merges, in)
	if f.mergeErr != nil {
		return nil, f.mergeErr
	}
	return &agent.RepoMergeResult{Target: "main", SHA: "mergecommit", MergeBase: "mergebase"}, nil
}

func (f *fakeProjects) Push(context.Context, int64, int64) (*agent.RepoBranch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes++
	if f.pushErr != nil {
		return nil, f.pushErr
	}
	return &agent.RepoBranch{Branch: "main"}, nil
}

type fakeWorkspaces struct {
	mu      sync.Mutex
	db      *sql.DB
	stopped []int64
	deleted []int64
}

func (f *fakeWorkspaces) Stop(_ context.Context, _, id int64) (*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	f.db.Exec(`UPDATE workspaces SET status = 'stopped' WHERE id = ?`, id)
	return &workspace.Workspace{ID: id, Status: workspace.StatusStopped}, nil
}

func (f *fakeWorkspaces) Delete(_ context.Context, _, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	_, err := f.db.Exec(`DELETE FROM workspaces WHERE id = ?`, id)
	return err
}

func (f *fakeWorkspaces) calls() (stopped, deleted []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.stopped...), append([]int64(nil), f.deleted...)
}

type changeRequest struct {
	taskID   int64
	feedback string
	start    bool
}

type fakeTasks struct {
	mu       sync.Mutex
	tasks    map[int64]*task.Task
	requests []changeRequest
	merged   []int64
}

func (f *fakeTasks) Get(_ context.Context, userID, projectID, taskID int64) (*task.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[taskID]
	if !ok || t.UserID != userID || t.ProjectID != projectID {
		return nil, task.ErrNotFound
	}
	clone := *t
	return &clone, nil
}

func (f *fakeTasks) RequestChanges(_ context.Context, _, _, taskID int64, feedback string, start bool) (*task.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.tasks[taskID]
	if t.Status != task.StatusInReview && t.Status != task.StatusChangesRequested {
		return nil, task.ErrInvalidState
	}
	f.requests = append(f.requests, changeRequest{taskID, feedback, start})
	t.Status = task.StatusChangesRequested
	if start {
		t.Status = task.StatusQueued
	}
	return t, nil
}

func (f *fakeTasks) Merged(_ context.Context, _, _, taskID int64) (*task.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.merged = append(f.merged, taskID)
	f.tasks[taskID].Status = task.StatusMerged
	return f.tasks[taskID], nil
}

func (f *fakeTasks) changeRequests() []changeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]changeRequest(nil), f.requests...)
}

// fakeRuns records runs as real agent run rows and lets tests finish them.
type fakeRuns struct {
	mu        sync.Mutex
	db        *sql.DB
	requests  []app.StartAgentRunRequest
	cancelled []int64
	onFinish  func(domain.AgentRun)
}

func (f *fakeRuns) StartRun(_ context.Context, req app.StartAgentRunRequest) (*domain.AgentRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res, err := f.db.Exec(`INSERT INTO ai_agent_runs (user_id, workspace_id, conversation_id, model, status) VALUES (?, ?, ?, ?, 'running')`, req.UserID, req.WorkspaceID, req.ConversationID, req.Model)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	f.requests = append(f.requests, req)
	return &domain.AgentRun{ID: id, UserID: req.UserID, WorkspaceID: req.WorkspaceID, Status: domain.AgentRunRunning}, nil
}

func (f *fakeRuns) GetRun(_ context.Context, userID, runID int64) (*domain.AgentRun, error) {
	run := domain.AgentRun{ID: runID}
	err := f.db.QueryRow(`SELECT user_id, status, error FROM ai_agent_runs WHERE id = ?`, runID).Scan(&run.UserID, &run.Status, &run.Error)
	if err != nil || run.UserID != userID {
		return nil, domain.ErrAgentRunNotFound
	}
	return &run, nil
}

func (f *fakeRuns) CancelRun(_ context.Context, _, runID int64) error {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, runID)
	f.mu.Unlock()
	f.finish(runID, domain.AgentRunCancelled, "agent run cancelled")
	return nil
}

func (f *fakeRuns) finish(runID int64, status domain.AgentRunStatus, msg string) {
	f.db.Exec(`UPDATE ai_agent_runs SET status = ?, error = ? WHERE id = ?`, status, msg, runID)
	if f.onFinish != nil {
		f.onFinish(domain.AgentRun{ID: runID, Status: status, Error: msg})
	}
}

func (f *fakeRuns) started() []app.StartAgentRunRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]app.StartAgentRunRequest(nil), f.requests...)
}

type fakeConversations struct{ db *sql.DB }

func (f fakeConversations) CreateConversation(_ context.Context, userID, workspaceID int64, model string) (*domain.Conversation, error) {
	res, err := f.db.Exec(`INSERT INTO ai_conversations (user_id, workspace_id, model) VALUES (?, ?, ?)`, userID, workspaceID, model)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &domain.Conversation{ID: id, UserID: userID, WorkspaceID: workspaceID}, nil
}

type env struct {
	svc        *service
	db         *sql.DB
	projects   *fakeProjects
	workspaces *fakeWorkspaces
	tasks      *fakeTasks
	runs       *fakeRuns
	owner      int64
	stranger   int64
	taskID     int64
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn := db.Conn()
	exec := func(query string, args ...any) int64 {
		t.Helper()
		res, err := conn.Exec(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	owner := exec(`INSERT INTO users (username, email, password) VALUES ('owner', 'owner@example.com', 'x')`)
	stranger := exec(`INSERT INTO users (username, email, password) VALUES ('stranger', 's@example.com', 'x')`)
	projectID := exec(`INSERT INTO projects (user_id, name, status) VALUES (?, 'shop', 'ready')`, owner)
	taskID := exec(`INSERT INTO tasks (project_id, user_id, title, body, status, branch, model) VALUES (?, ?, 'Add a cart', 'Users need a cart.', 'in_review', 'task/1', 'claude-opus-5-5')`, projectID, owner)

	e := &env{
		db:         conn,
		projects:   &fakeProjects{db: conn, project: project.Project{ID: projectID, UserID: owner, Name: "shop", DefaultBranch: "main", Status: project.StatusReady}, files: []agent.RepoDiffFile{{Path: "cart.go", Status: "A", Additions: 40}, {Path: "README.md", Status: "M", Additions: 2, Deletions: 1}}},
		workspaces: &fakeWorkspaces{db: conn},
		tasks:      &fakeTasks{tasks: map[int64]*task.Task{taskID: {ID: taskID, ProjectID: projectID, UserID: owner, Title: "Add a cart", Body: "Users need a cart.", Status: task.StatusInReview, Branch: "task/1", Model: "claude-opus-5-5", Effort: "high"}}},
		runs:       &fakeRuns{db: conn},
		owner:      owner,
		stranger:   stranger,
		taskID:     taskID,
	}
	e.svc = NewService(NewRepository(conn), e.projects, e.workspaces, e.tasks, e.runs, fakeConversations{conn}).(*service)
	e.runs.onFinish = e.svc.RunFinished
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.svc.Shutdown(ctx)
	})
	return e
}

func (e *env) pid() int64 { return e.projects.project.ID }

// openTaskMR opens the merge request a task gets when it reaches review.
func (e *env) openTaskMR(t *testing.T) *MergeRequest {
	t.Helper()
	tk, _ := e.tasks.Get(context.Background(), e.owner, e.pid(), e.taskID)
	tk.HeadSHA = "head1"
	e.svc.TaskReadyForReview(context.Background(), tk, "Added a cart.")
	mr, err := e.svc.repo.GetOpenBySource(context.Background(), e.pid(), "task/1")
	if err != nil || mr == nil {
		t.Fatalf("expected an open merge request: %v", err)
	}
	return mr
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startAgentReview starts a review agent and waits for its run.
func (e *env) startAgentReview(t *testing.T, mr *MergeRequest, in AgentReviewInput) *Review {
	t.Helper()
	rv, err := e.svc.StartAgentReview(context.Background(), e.owner, e.pid(), mr.ID, in)
	if err != nil {
		t.Fatalf("start agent review: %v", err)
	}
	waitFor(t, "the review run", func() bool {
		got, _ := e.svc.repo.GetReview(context.Background(), rv.ID)
		return got.RunID != 0 || got.Status != ReviewPending
	})
	got, _ := e.svc.repo.GetReview(context.Background(), rv.ID)
	return got
}

func (e *env) tool(userID, workspaceID int64, name, args string) (domain.ToolResultPart, bool) {
	return e.svc.ExecuteTool(context.Background(), aitools.ExecutionRequest{UserID: userID, WorkspaceID: workspaceID, ToolName: name, Arguments: json.RawMessage(args)})
}

func TestTaskReadyForReviewOpensThenUpdatesOneMergeRequest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	if mr.Title != "Add a cart" || mr.Description != "Added a cart." || mr.HeadSHA != "head1" || mr.TargetBranch != "main" || mr.TaskID != e.taskID {
		t.Fatalf("unexpected merge request: %+v", mr)
	}

	tk, _ := e.tasks.Get(ctx, e.owner, e.pid(), e.taskID)
	tk.HeadSHA = "head2"
	e.svc.TaskReadyForReview(ctx, tk, "")
	all, _ := e.svc.List(ctx, e.owner, e.pid())
	if len(all) != 1 || all[0].HeadSHA != "head2" || all[0].Description != "Added a cart." {
		t.Fatalf("expected one updated merge request keeping its description, got %+v", all)
	}
}

func TestCreateOpensMergeRequestForPublishedBranch(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.projects.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "feature", SHA: "f1"}}

	mr, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	if err != nil || mr.HeadSHA != "f1" || mr.TaskID != 0 {
		t.Fatalf("create: %+v %v", mr, err)
	}
	tests := []struct {
		name string
		in   CreateInput
		want error
	}{
		{"duplicate", CreateInput{Title: "Again", SourceBranch: "feature"}, ErrInvalidState},
		{"default branch", CreateInput{Title: "Main", SourceBranch: "main"}, ErrInvalidInput},
		{"missing branch", CreateInput{Title: "Nope", SourceBranch: "nope"}, ErrInvalidInput},
		{"no title", CreateInput{SourceBranch: "feature"}, ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.svc.Create(ctx, e.owner, e.pid(), tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
	if _, err := e.svc.Create(ctx, e.stranger, e.pid(), CreateInput{Title: "x", SourceBranch: "feature"}); !errors.Is(err, project.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestUserReviewRequestingChangesFeedsTheTask(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	invalidReviews := []ReviewInput{
		{Verdict: "lgtm"},
		{Verdict: VerdictComment, Comments: []CommentInput{{Path: "cart.go", Line: 0, Body: "x"}}},
		{Verdict: VerdictComment, Comments: []CommentInput{{Path: "../etc/passwd", Line: 1, Body: "x"}}},
		{Verdict: VerdictComment, Comments: []CommentInput{{Path: "cart.go", Line: 1, Body: " "}}},
	}
	for _, in := range invalidReviews {
		if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%+v: expected ErrInvalidInput, got %v", in, err)
		}
	}

	rv, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{
		Verdict: VerdictRequestChanges, Body: "Handle an empty cart.", SendToAgent: true,
		Comments: []CommentInput{{Path: "cart.go", Line: 12, Body: "This panics on nil."}},
	})
	if err != nil || rv.Author != AuthorUser || rv.Status != ReviewSubmitted || rv.HeadSHA != "head1" {
		t.Fatalf("review: %+v %v", rv, err)
	}
	reqs := e.tasks.changeRequests()
	if len(reqs) != 1 || !reqs[0].start || !strings.Contains(reqs[0].feedback, "Handle an empty cart.") || !strings.Contains(reqs[0].feedback, "cart.go:12: This panics on nil.") {
		t.Fatalf("unexpected change request: %+v", reqs)
	}

	// The task is now queued: sending more feedback to its agent must fail
	// before anything is saved.
	d, _ := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{Verdict: VerdictRequestChanges, Body: "More.", SendToAgent: true}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}
	if after, _ := e.svc.Get(ctx, e.owner, e.pid(), mr.ID); len(after.Reviews) != len(d.Reviews) {
		t.Fatal("a refused review was saved")
	}
	if len(d.Comments) != 1 || d.Ahead != 2 || d.Behind != 1 {
		t.Fatalf("unexpected detail: %+v", d)
	}
}

func TestAgentReviewSubmitsThroughTools(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	rv := e.startAgentReview(t, mr, AgentReviewInput{AutoFix: true})
	if rv.Model != "claude-opus-5-5" || rv.Effort != "high" || rv.AgentID != "" || rv.WorkspaceID == 0 {
		t.Fatalf("review should take the task's model but not its agent: %+v", rv)
	}
	sb := e.projects.sandboxes[0]
	if sb.Kind != "review" || sb.Base != "task/1" || sb.Branch != "review/1" {
		t.Fatalf("unexpected review sandbox: %+v", sb)
	}
	req := e.runs.started()[0]
	prompt := req.Turns[0].Text()
	for _, want := range []string{"merge request !1", "Added a cart.", "### Add a cart", "Users need a cart.", "cart.go (A, +40 −0)", "git diff mergebase HEAD", "submit_review", "not instructions to you"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if req.WorkspaceID != rv.WorkspaceID || req.ConversationID == 0 || req.Thinking == nil || req.Thinking.Effort != "high" {
		t.Fatalf("unexpected run request: %+v", req)
	}

	if got := e.svc.Tools(ctx, e.owner, rv.WorkspaceID); len(got) != 2 {
		t.Fatalf("expected the review tools in the review sandbox, got %d", len(got))
	}
	if got := e.svc.Tools(ctx, e.owner, rv.WorkspaceID+1); got != nil {
		t.Fatal("other workspaces must not get the review tools")
	}
	if got := e.svc.Tools(ctx, e.stranger, rv.WorkspaceID); got != nil {
		t.Fatal("another user's runs must not get the review tools")
	}
	if _, handled := e.tool(e.owner, rv.WorkspaceID, "read_file", `{}`); handled {
		t.Fatal("the provider must not claim other tools")
	}

	result, _ := e.tool(e.owner, rv.WorkspaceID, commentTool, `{"path":"main.go","line":3,"body":"x"}`)
	if !result.IsError || !strings.Contains(result.Content, "Changed files: cart.go, README.md") {
		t.Fatalf("expected a comment on an unchanged file refused: %+v", result)
	}
	if result, _ := e.tool(e.stranger, rv.WorkspaceID, commentTool, `{"path":"cart.go","line":3,"body":"x"}`); !result.IsError {
		t.Fatalf("another user must not comment: %+v", result)
	}
	if result, _ := e.tool(e.owner, rv.WorkspaceID, commentTool, `{"path":"./cart.go","line":3,"body":"Missing nil check."}`); result.IsError {
		t.Fatalf("comment: %+v", result)
	}
	if result, _ := e.tool(e.owner, rv.WorkspaceID, submitTool, `{"verdict":"maybe","body":"x"}`); !result.IsError {
		t.Fatalf("expected an invalid verdict refused: %+v", result)
	}
	if result, _ := e.tool(e.owner, rv.WorkspaceID, submitTool, `{"verdict":"request_changes","body":"Needs a nil check."}`); result.IsError {
		t.Fatalf("submit: %+v", result)
	}
	if result, _ := e.tool(e.owner, rv.WorkspaceID, submitTool, `{"verdict":"approve","body":"Fine."}`); !result.IsError {
		t.Fatalf("a second submission must be refused: %+v", result)
	}
	if got := e.svc.Tools(ctx, e.owner, rv.WorkspaceID); got != nil {
		t.Fatal("the review tools should go away once the review is submitted")
	}

	reqs := e.tasks.changeRequests()
	if len(reqs) != 1 || !reqs[0].start || !strings.Contains(reqs[0].feedback, "A review agent requested changes.") || !strings.Contains(reqs[0].feedback, "cart.go:3: Missing nil check.") {
		t.Fatalf("auto-fix should start the task's agent with the review: %+v", reqs)
	}

	e.runs.finish(rv.RunID, domain.AgentRunCompleted, "")
	waitFor(t, "the review sandbox to stop", func() bool { stopped, _ := e.workspaces.calls(); return len(stopped) == 1 })
	got, _ := e.svc.repo.GetReview(ctx, rv.ID)
	if got.Status != ReviewSubmitted || got.Verdict != VerdictRequestChanges {
		t.Fatalf("unexpected final review: %+v", got)
	}
}

func TestAgentReviewThatNeverSubmitsFails(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	rv := e.startAgentReview(t, mr, AgentReviewInput{Model: "claude-sonnet-5-5"})
	if _, err := e.svc.StartAgentReview(ctx, e.owner, e.pid(), mr.ID, AgentReviewInput{}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected one review agent at a time, got %v", err)
	}

	e.runs.finish(rv.RunID, domain.AgentRunCompleted, "")
	waitFor(t, "the review to fail", func() bool {
		got, _ := e.svc.repo.GetReview(ctx, rv.ID)
		return got.Status == ReviewFailed
	})
	got, _ := e.svc.repo.GetReview(ctx, rv.ID)
	if !strings.Contains(got.Error, "without submitting") {
		t.Fatalf("unexpected error: %q", got.Error)
	}
	if stopped, _ := e.workspaces.calls(); len(stopped) != 1 || stopped[0] != rv.WorkspaceID {
		t.Fatalf("expected the review sandbox stopped, got %v", stopped)
	}
	if len(e.tasks.changeRequests()) != 0 {
		t.Fatal("a failed review must not touch the task")
	}
	// A new review agent can start once the last one has ended.
	e.startAgentReview(t, mr, AgentReviewInput{Model: "claude-sonnet-5-5"})
}

func TestMergeMergesReviewedHeadAndCleansUp(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	finished := e.startAgentReview(t, mr, AgentReviewInput{})
	e.tool(e.owner, finished.WorkspaceID, submitTool, `{"verdict":"approve","body":"Looks good."}`)
	e.runs.finish(finished.RunID, domain.AgentRunCompleted, "")
	waitFor(t, "the first review sandbox to stop", func() bool { stopped, _ := e.workspaces.calls(); return len(stopped) == 1 })
	running := e.startAgentReview(t, mr, AgentReviewInput{})

	result, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{Strategy: "squash", Push: true})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	want := project.MergeInput{Source: "task/1", Strategy: "squash", Message: "Merge !1: Add a cart", ExpectedSourceSHA: "head1"}
	if len(e.projects.merges) != 1 || e.projects.merges[0] != want {
		t.Fatalf("merge input = %+v", e.projects.merges)
	}
	got := result.MergeRequest
	if got.Status != StatusMerged || got.MergeSHA != "mergecommit" || got.BaseSHA != "mergebase" || got.MergedAt == nil || !result.Pushed {
		t.Fatalf("unexpected merge result: %+v %+v", got, result)
	}
	if e.tasks.merged[0] != e.taskID {
		t.Fatalf("expected the task closed, got %v", e.tasks.merged)
	}
	if len(e.runs.cancelled) != 1 || e.runs.cancelled[0] != running.RunID {
		t.Fatalf("expected the running review cancelled, got %v", e.runs.cancelled)
	}
	if _, deleted := e.workspaces.calls(); len(deleted) != 2 {
		t.Fatalf("expected both review sandboxes removed, got %v", deleted)
	}
	reviews, _ := e.svc.repo.ListReviews(ctx, mr.ID)
	if reviews[0].Status != ReviewSubmitted || reviews[1].Status != ReviewFailed || reviews[1].WorkspaceID != 0 {
		t.Fatalf("unexpected reviews after merge: %+v %+v", reviews[0], reviews[1])
	}
	diff, _ := e.svc.Diff(ctx, e.owner, e.pid(), mr.ID)
	if diff.BaseSHA != "mergebase" || diff.HeadSHA != "head1" {
		t.Fatalf("a merged request should diff from its merge base: %+v", diff)
	}
	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("merging twice: expected ErrInvalidState, got %v", err)
	}
}

func TestMergeFailuresKeepTheRequestOpen(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	e.projects.mergeErr = &agent.AgentError{StatusCode: http.StatusConflict, Message: "task/1 conflicts with main", Conflicts: []string{"cart.go"}}
	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{}); err == nil {
		t.Fatal("expected the conflict to fail the merge")
	}
	if got, _ := e.svc.repo.GetByID(ctx, mr.ID); got.Status != StatusOpen || len(e.tasks.merged) != 0 {
		t.Fatalf("a failed merge must leave the request open and the task alone: %+v", got)
	}

	if err := e.svc.ResolveConflicts(ctx, e.owner, e.pid(), mr.ID, []string{"cart.go"}); err != nil {
		t.Fatalf("resolve conflicts: %v", err)
	}
	reqs := e.tasks.changeRequests()
	if len(reqs) != 1 || !reqs[0].start || !strings.Contains(reqs[0].feedback, "Merge `devpad/main` into `task/1`") || !strings.Contains(reqs[0].feedback, "- cart.go") {
		t.Fatalf("unexpected conflict request: %+v", reqs)
	}

	e.projects.mergeErr = nil
	e.projects.pushErr = errors.New("rejected: non-fast-forward")
	result, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{Push: true})
	if err != nil || result.Pushed || !strings.Contains(result.PushError, "non-fast-forward") || result.MergeRequest.Status != StatusMerged {
		t.Fatalf("a failed push should not undo the merge: %+v %v", result, err)
	}
}

func TestCloseRemovesReviewSandboxesButLeavesTheTask(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	rv := e.startAgentReview(t, mr, AgentReviewInput{})

	closed, err := e.svc.Close(ctx, e.owner, e.pid(), mr.ID)
	if err != nil || closed.Status != StatusClosed {
		t.Fatalf("close: %+v %v", closed, err)
	}
	if _, deleted := e.workspaces.calls(); len(deleted) != 1 || deleted[0] != rv.WorkspaceID {
		t.Fatalf("expected the review sandbox removed, got %v", deleted)
	}
	if len(e.tasks.merged) != 0 {
		t.Fatal("closing must not close the task")
	}
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{Verdict: VerdictComment}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("reviewing a closed request: expected ErrInvalidState, got %v", err)
	}
	// A new round of the task opens a fresh merge request.
	reopened := e.openTaskMR(t)
	if reopened.ID == mr.ID {
		t.Fatal("expected a new merge request after closing the old one")
	}
}

func TestReconcileFailsInterruptedReviews(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	rv := e.startAgentReview(t, mr, AgentReviewInput{})
	never := &Review{MergeRequestID: mr.ID, Author: AuthorAgent, Status: ReviewPending, Model: "m"}
	e.svc.repo.CreateReview(ctx, never)
	e.db.Exec(`UPDATE ai_agent_runs SET status = 'failed', error = 'agent run stopped because the server restarted or shut down' WHERE id = ?`, rv.RunID)

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitFor(t, "both reviews to fail", func() bool {
		a, _ := e.svc.repo.GetReview(ctx, rv.ID)
		b, _ := e.svc.repo.GetReview(ctx, never.ID)
		return a.Status == ReviewFailed && b.Status == ReviewFailed
	})
	got, _ := e.svc.repo.GetReview(ctx, rv.ID)
	if !strings.Contains(got.Error, "server restarted") {
		t.Fatalf("unexpected error: %q", got.Error)
	}
}

func TestHandler(t *testing.T) {
	e := setup(t)
	mr := e.openTaskMR(t)
	h := NewHandler(e.svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/merge-requests", h.HandleList)
	mux.HandleFunc("GET /api/projects/{id}/merge-requests/{mrId}", h.HandleGet)
	mux.HandleFunc("POST /api/projects/{id}/merge-requests/{mrId}/reviews", h.HandleReview)
	mux.HandleFunc("POST /api/projects/{id}/merge-requests/{mrId}/merge", h.HandleMerge)

	do := func(userID int64, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: userID}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	base := "/api/projects/" + itoa(e.pid()) + "/merge-requests"
	one := base + "/" + itoa(mr.ID)

	rec := do(e.owner, http.MethodPost, one+"/reviews", `{"verdict":"comment","body":"Nice.","comments":[{"path":"cart.go","line":4,"body":"Typo."}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("review: %d %s", rec.Code, rec.Body)
	}
	rec = do(e.owner, http.MethodGet, one, "")
	var got struct {
		MergeRequest struct {
			Status   string           `json:"status"`
			Reviews  []map[string]any `json:"reviews"`
			Comments []map[string]any `json:"comments"`
			Ahead    int              `json:"ahead"`
		} `json:"mergeRequest"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.MergeRequest.Status != "open" || len(got.MergeRequest.Reviews) != 1 || len(got.MergeRequest.Comments) != 1 || got.MergeRequest.Ahead != 2 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	e.projects.mergeErr = &agent.AgentError{StatusCode: http.StatusConflict, Message: "task/1 conflicts with main", Conflicts: []string{"cart.go"}}
	rec = do(e.owner, http.MethodPost, one+"/merge", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"conflicts":["cart.go"]`) {
		t.Fatalf("conflict: %d %s", rec.Code, rec.Body)
	}

	tests := []struct {
		name   string
		userID int64
		method string
		path   string
		body   string
		want   int
	}{
		{"list", e.owner, http.MethodGet, base, "", http.StatusOK},
		{"other user's request", e.stranger, http.MethodGet, one, "", http.StatusNotFound},
		{"other user's project", e.stranger, http.MethodGet, base, "", http.StatusForbidden},
		{"bad verdict", e.owner, http.MethodPost, one + "/reviews", `{"verdict":"meh"}`, http.StatusBadRequest},
		{"bad id", e.owner, http.MethodGet, base + "/x", "", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := do(tt.userID, tt.method, tt.path, tt.body); rec.Code != tt.want {
				t.Fatalf("expected %d, got %d %s", tt.want, rec.Code, rec.Body)
			}
		})
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
