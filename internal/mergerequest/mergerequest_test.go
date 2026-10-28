package mergerequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
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
	logs      [][2]string
	merges    []project.MergeInput
	mergeErr  error
	// landed, when set, is where a merge leaves its target instead of the
	// merge commit, as if another merge landed on top before anyone looked.
	landed  string
	pushed  []string
	pushErr error
	// setup is what AwaitSetup reports; while setupGate is open (non-nil
	// and not closed) it waits. awaited records each wait's sandbox.
	setup     *project.Setup
	setupGate chan struct{}
	awaited   []int64
	// environment is what AwaitEnvironment reports.
	environment *project.Environment
	// sandboxErr, when set, is what CreateSandbox fails with.
	sandboxErr error
}

func (f *fakeProjects) AwaitSetup(ctx context.Context, _, _, workspaceID int64) (*project.Setup, error) {
	f.mu.Lock()
	f.awaited = append(f.awaited, workspaceID)
	gate := f.setupGate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setup, nil
}

func (f *fakeProjects) AwaitEnvironment(context.Context, int64, int64) (*project.Environment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.environment, nil
}

func (f *fakeProjects) awaitedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.awaited)
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
	f.mu.Lock()
	defer f.mu.Unlock()
	return &agent.RepoRefs{DefaultBranch: f.project.DefaultBranch, Branches: append([]agent.RepoRef(nil), f.branches...)}, nil
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

func (f *fakeProjects) Log(_ context.Context, _, _ int64, base, head string) (*agent.RepoLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, [2]string{base, head})
	return &agent.RepoLog{Commits: []agent.RepoCommit{{SHA: head, Parents: []string{base}, Subject: "work"}}}, nil
}

func (f *fakeProjects) CreateSandbox(_ context.Context, userID, projectID int64, in project.SandboxInput) (*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sandboxErr != nil {
		return nil, f.sandboxErr
	}
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
	target := in.Target
	if target == "" {
		target = f.project.DefaultBranch
	}
	// The target moves to the merge commit, as it does in the canonical repo.
	moved := "mergecommit"
	if f.landed != "" {
		moved = f.landed
	}
	for i := range f.branches {
		if f.branches[i].Name == target {
			f.branches[i].SHA = moved
		}
	}
	return &agent.RepoMergeResult{Target: target, SHA: "mergecommit", MergeBase: "mergebase"}, nil
}

func (f *fakeProjects) Push(_ context.Context, _, _ int64, branch string) (*agent.RepoBranch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushed = append(f.pushed, branch)
	if f.pushErr != nil {
		return nil, f.pushErr
	}
	return &agent.RepoBranch{Branch: branch}, nil
}

type fakeWorkspaces struct {
	mu      sync.Mutex
	db      *sql.DB
	stopped []int64
	deleted []int64
}

func (f *fakeWorkspaces) Get(_ context.Context, userID, id int64) (*workspace.Workspace, error) {
	ws := workspace.Workspace{ID: id}
	var projectID sql.NullInt64
	err := f.db.QueryRow(`SELECT user_id, project_id, kind, branch, status FROM workspaces WHERE id = ?`, id).Scan(&ws.UserID, &projectID, &ws.Kind, &ws.Branch, &ws.Status)
	if err != nil {
		return nil, workspace.ErrNotFound
	}
	if ws.UserID != userID {
		return nil, workspace.ErrForbidden
	}
	ws.ProjectID = projectID.Int64
	return &ws, nil
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
	starts   []task.StartInput
	merged   []int64
}

func (f *fakeTasks) Start(_ context.Context, _, _, taskID int64, in task.StartInput) (*task.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.tasks[taskID]
	if !t.Status.Startable() {
		return nil, task.ErrInvalidState
	}
	f.starts = append(f.starts, in)
	t.Status = task.StatusQueued
	clone := *t
	return &clone, nil
}

func (f *fakeTasks) started() []task.StartInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]task.StartInput(nil), f.starts...)
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
	defaults   fakeDefaults
	budgets    *fakeBudgets
	owner      int64
	stranger   int64
	taskID     int64
}

// fakeDefaults is the admin's agent defaults, keyed by agent ID. Its models
// support every effort except "unsupported".
type fakeDefaults map[string]domain.AgentDefaults

// fakeBudgets is the admin's budget defaults, or err when it is set.
type fakeBudgets struct {
	defaults domain.BudgetDefaults
	err      error
}

func (f *fakeBudgets) BudgetDefaults(context.Context) (domain.BudgetDefaults, error) {
	return f.defaults, f.err
}

func (f fakeDefaults) DefaultsFor(_ context.Context, agentID, effort string) (*domain.AgentDefaults, error) {
	if agentID == "" {
		agentID = domain.DefaultAgentID
	}
	d, ok := f[agentID]
	if !ok {
		return nil, nil
	}
	if effort != "" && effort != "unsupported" {
		d.Effort = effort
	}
	return &d, nil
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
		defaults:   fakeDefaults{},
		budgets:    &fakeBudgets{defaults: domain.BudgetDefaults{ReviewUSD: 5}},
		owner:      owner,
		stranger:   stranger,
		taskID:     taskID,
	}
	e.svc = NewService(NewRepository(conn), e.projects, e.workspaces, e.tasks, e.runs, fakeConversations{conn}, e.defaults, e.budgets).(*service)
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

// gitFlow makes the project's default branch develop, with main protected
// for releases.
func (e *env) gitFlow() {
	e.projects.project.DefaultBranch = "develop"
	e.projects.project.ProtectedBranches = []string{"main"}
	e.projects.branches = []agent.RepoRef{{Name: "main", SHA: "m1"}, {Name: "develop", SHA: "d1"}, {Name: "feature", SHA: "f1"}, {Name: "staging", SHA: "s1"}}
}

func TestCreateTargetsAProtectedBranch(t *testing.T) {
	e := setup(t)
	e.gitFlow()
	ctx := context.Background()

	feature, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	if err != nil || feature.TargetBranch != "develop" {
		t.Fatalf("expected the default branch as target: %+v %v", feature, err)
	}
	// A release merges one protected branch into another.
	release, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Release", SourceBranch: "develop", TargetBranch: "main"})
	if err != nil || release.SourceBranch != "develop" || release.TargetBranch != "main" || release.HeadSHA != "d1" {
		t.Fatalf("release: %+v %v", release, err)
	}
	tests := []struct {
		name string
		in   CreateInput
	}{
		{"unprotected target", CreateInput{Title: "x", SourceBranch: "staging", TargetBranch: "feature"}},
		{"missing target", CreateInput{Title: "x", SourceBranch: "staging", TargetBranch: "nope"}},
		{"into itself", CreateInput{Title: "x", SourceBranch: "main", TargetBranch: "main"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.svc.Create(ctx, e.owner, e.pid(), tt.in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

func TestMergeIntoAProtectedBranchMovesItsMergeRequest(t *testing.T) {
	e := setup(t)
	e.gitFlow()
	ctx := context.Background()
	feature, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	release, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Release", SourceBranch: "develop", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}

	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), feature.ID, MergeInput{Push: true}); err != nil {
		t.Fatalf("merge feature: %v", err)
	}
	if got := e.projects.merges[0]; got.Source != "feature" || got.Target != "develop" {
		t.Fatalf("expected feature merged into develop, got %+v", got)
	}
	if len(e.projects.pushed) != 1 || e.projects.pushed[0] != "develop" {
		t.Fatalf("expected develop pushed, got %v", e.projects.pushed)
	}
	// develop moved, so the release now proposes the merged commit.
	if got, _ := e.svc.repo.GetByID(ctx, release.ID); got.HeadSHA != "mergecommit" {
		t.Fatalf("expected the release to follow develop, got %q", got.HeadSHA)
	}
	if pushes, _ := e.svc.repo.ListPushes(ctx, release.ID); len(pushes) != 1 || *pushes[0] != (Push{ID: pushes[0].ID, MergeRequestID: release.ID, Source: PushMerge, BeforeSHA: "d1", HeadSHA: "mergecommit", CreatedAt: pushes[0].CreatedAt}) {
		t.Fatalf("expected the merge recorded as a push, got %+v", pushes)
	}

	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), release.ID, MergeInput{}); err != nil {
		t.Fatalf("merge release: %v", err)
	}
	if got := e.projects.merges[1]; got.Source != "develop" || got.Target != "main" || got.ExpectedSourceSHA != "mergecommit" {
		t.Fatalf("expected develop merged into main at the merged commit, got %+v", got)
	}
}

func TestMergedTargetFollowsTheCanonicalBranch(t *testing.T) {
	e := setup(t)
	e.gitFlow()
	ctx := context.Background()
	feature, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	release, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Release", SourceBranch: "develop", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	// Another merge into develop lands before this one's follow-up runs:
	// the release must propose develop as it is, not this merge's commit.
	e.projects.landed = "later"
	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), feature.ID, MergeInput{}); err != nil {
		t.Fatalf("merge feature: %v", err)
	}
	if got, _ := e.svc.repo.GetByID(ctx, release.ID); got.HeadSHA != "later" {
		t.Fatalf("expected the release on develop's current commit, got %q", got.HeadSHA)
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

func TestAgentReviewThatCannotStartLeavesNoConversation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	e.budgets.err = errors.New("database is locked")

	rv := e.startAgentReview(t, mr, AgentReviewInput{})
	if rv.Status != ReviewFailed || !strings.Contains(rv.Error, "database is locked") {
		t.Fatalf("expected the review to fail on the budget lookup, got %+v", rv)
	}
	var conversations int
	if err := e.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_conversations`).Scan(&conversations); err != nil {
		t.Fatal(err)
	}
	if conversations != 0 {
		t.Fatalf("expected no conversation left in the review sandbox, got %d", conversations)
	}
}

func TestAgentReviewSubmitsThroughTools(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	rv := e.startAgentReview(t, mr, AgentReviewInput{AutoFix: true})
	if rv.Model != "claude-opus-5-5" || rv.Effort != "high" || rv.AgentID != domain.ReviewAgentID || rv.WorkspaceID == 0 {
		t.Fatalf("review should take the task's model but run the review agent: %+v", rv)
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
	if req.WorkspaceID != rv.WorkspaceID || req.AgentID != domain.ReviewAgentID || req.ConversationID == 0 || req.Thinking == nil || req.Thinking.Effort != "high" {
		t.Fatalf("unexpected run request: %+v", req)
	}
	// The review gets the admin's review budget, $5 in these tests.
	req.Budget.Spend(4.99)
	if req.Budget.Exhausted() {
		t.Fatal("review budget exhausted early")
	}
	req.Budget.Spend(0.01)
	if !req.Budget.Exhausted() {
		t.Fatal("expected the review budget to be $5")
	}

	if got := e.svc.Tools(ctx, e.owner, rv.WorkspaceID); len(got) != len(reviewTools) {
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

	result, _ := e.tool(e.owner, rv.WorkspaceID, commentTool, `{"path":"main.go","line":3,"body":"x","blocking":false}`)
	if !result.IsError || !strings.Contains(result.Content, "Changed files: cart.go, README.md") {
		t.Fatalf("expected a comment on an unchanged file refused: %+v", result)
	}
	if result, _ := e.tool(e.stranger, rv.WorkspaceID, commentTool, `{"path":"cart.go","line":3,"body":"x"}`); !result.IsError {
		t.Fatalf("another user must not comment: %+v", result)
	}
	if result, _ := e.tool(e.owner, rv.WorkspaceID, commentTool, `{"path":"cart.go","line":3,"body":"x"}`); !result.IsError || !strings.Contains(result.Content, "blocking: true or false") {
		t.Fatalf("a new thread must say whether it blocks: %+v", result)
	}
	if result, _ := e.tool(e.owner, rv.WorkspaceID, commentTool, `{"path":"cart.go","line":8,"body":"Could be named better.","blocking":false}`); result.IsError {
		t.Fatalf("comment: %+v", result)
	}
	if result, _ := e.tool(e.owner, rv.WorkspaceID, submitTool, `{"verdict":"request_changes","body":"Rename it."}`); !result.IsError || !strings.Contains(result.Content, "no blocking thread is open") {
		t.Fatalf("changes must not be requested over suggestions alone: %+v", result)
	}
	if result, _ := e.tool(e.owner, rv.WorkspaceID, commentTool, `{"path":"./cart.go","line":3,"body":"Missing nil check.","blocking":true}`); result.IsError {
		t.Fatalf("comment: %+v", result)
	}
	if result, _ := e.tool(e.owner, rv.WorkspaceID, submitTool, `{"verdict":"approve","body":"Fine."}`); !result.IsError || !strings.Contains(result.Content, "1 blocking threads are still open") {
		t.Fatalf("a change with a blocking thread open must not be approved: %+v", result)
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
	if len(reqs) != 1 || !reqs[0].start || !strings.Contains(reqs[0].feedback, "A review agent requested changes.") || !strings.Contains(reqs[0].feedback, ", blocking] cart.go:3: Missing nil check.") || !strings.Contains(reqs[0].feedback, ", non-blocking] cart.go:8: Could be named better.") {
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
	reviewing := func() bool {
		mrs, err := e.svc.List(ctx, e.owner, e.pid())
		if err != nil || len(mrs) != 1 {
			t.Fatalf("listing merge requests: %v %v", mrs, err)
		}
		return mrs[0].AgentReviewing
	}
	if !reviewing() {
		t.Fatal("the list should say a review agent is at work")
	}

	e.runs.finish(rv.RunID, domain.AgentRunCompleted, "")
	waitFor(t, "the review to fail", func() bool {
		got, _ := e.svc.repo.GetReview(ctx, rv.ID)
		return got.Status == ReviewFailed
	})
	if reviewing() {
		t.Fatal("the list should stop saying a review agent is at work once it has ended")
	}
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
	want := project.MergeInput{Source: "task/1", Target: "main", Strategy: "squash", Message: "Merge !1: Add a cart", ExpectedSourceSHA: "head1"}
	if len(e.projects.merges) != 1 || e.projects.merges[0] != want {
		t.Fatalf("merge input = %+v", e.projects.merges)
	}
	if len(e.projects.pushed) != 1 || e.projects.pushed[0] != "main" {
		t.Fatalf("expected main pushed, got %v", e.projects.pushed)
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

func TestGuardDeleteBranchKeepsOpenSources(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	deleted := 0
	del := func() error { deleted++; return nil }
	if err := e.svc.GuardDeleteBranch(ctx, e.owner, e.pid(), "task/1", del); !errors.Is(err, project.ErrBranchHasMergeRequest) {
		t.Fatalf("expected ErrBranchHasMergeRequest for an open merge request's branch, got %v", err)
	}
	if deleted != 0 {
		t.Fatal("an open merge request's branch was deleted")
	}
	if err := e.svc.GuardDeleteBranch(ctx, e.owner, e.pid(), "feature", del); err != nil || deleted != 1 {
		t.Fatalf("a branch without a merge request: deleted %d, %v", deleted, err)
	}
	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := e.svc.GuardDeleteBranch(ctx, e.owner, e.pid(), "task/1", del); err != nil || deleted != 2 {
		t.Fatalf("a merged branch: deleted %d, %v", deleted, err)
	}
}

// A merge request cannot open from a branch while it is being deleted: it
// waits for the deletion and then finds the branch gone.
func TestCreateWaitsForABranchBeingDeleted(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.projects.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "feature", SHA: "f"}}

	inDelete, release := make(chan struct{}), make(chan struct{})
	guarded := make(chan error, 1)
	go func() {
		guarded <- e.svc.GuardDeleteBranch(ctx, e.owner, e.pid(), "feature", func() error {
			close(inDelete)
			<-release
			e.projects.mu.Lock()
			e.projects.branches = e.projects.branches[:1]
			e.projects.mu.Unlock()
			return nil
		})
	}()
	<-inDelete
	created := make(chan error, 1)
	go func() {
		_, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
		created <- err
	}()
	select {
	case err := <-created:
		t.Fatalf("Create did not wait for the deletion: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-guarded; err != nil {
		t.Fatalf("guard: %v", err)
	}
	if err := <-created; !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for the deleted branch, got %v", err)
	}
}

func TestBranchPublishedMovesTheOpenMergeRequest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	// The user adds a commit to the branch from the project workspace.
	e.svc.BranchPublished(ctx, e.owner, e.pid(), "task/1", "head2")
	got, _ := e.svc.repo.GetByID(ctx, mr.ID)
	if got.HeadSHA != "head2" {
		t.Fatalf("expected the merge request to follow the published head, got %q", got.HeadSHA)
	}
	// Other users' branches and branches without an open request are left be.
	e.svc.BranchPublished(ctx, e.stranger, e.pid(), "task/1", "evil")
	e.svc.BranchPublished(ctx, e.owner, e.pid(), "feature", "f2")
	if got, _ := e.svc.repo.GetByID(ctx, mr.ID); got.HeadSHA != "head2" {
		t.Fatalf("unexpected head after unrelated publishes: %q", got.HeadSHA)
	}

	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(e.projects.merges) != 1 || e.projects.merges[0].ExpectedSourceSHA != "head2" {
		t.Fatalf("expected the merge pinned to the published head, got %+v", e.projects.merges)
	}
	// A merged request no longer follows the branch.
	e.svc.BranchPublished(ctx, e.owner, e.pid(), "task/1", "head3")
	if got, _ := e.svc.repo.GetByID(ctx, mr.ID); got.HeadSHA != "head2" || got.Status != StatusMerged {
		t.Fatalf("a merged request moved: %+v", got)
	}
}

func TestBranchFastForwardedFromUpstreamMovesTheOpenMergeRequest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	// The head is read from the canonical repo, not taken from the caller,
	// so a merge that moved the branch since wins.
	e.projects.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "task/1", SHA: "upstream2"}}
	e.svc.BranchFastForwarded(ctx, e.owner, e.pid(), "task/1", "upstream1")
	d, err := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if d.MergeRequest.HeadSHA != "upstream2" || len(d.Pushes) != 1 || d.Pushes[0].Source != PushUpstream {
		t.Fatalf("head %q, pushes %+v", d.MergeRequest.HeadSHA, d.Pushes)
	}
}

func TestEachMoveOfTheHeadIsRecordedAsAPush(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	if d, err := e.svc.Get(ctx, e.owner, e.pid(), mr.ID); err != nil || len(d.Pushes) != 0 {
		t.Fatalf("a new merge request has no pushes: %+v %v", d, err)
	}

	tk, _ := e.tasks.Get(ctx, e.owner, e.pid(), e.taskID)
	tk.HeadSHA = "head2"
	e.svc.TaskReadyForReview(ctx, tk, "")
	// A round that changes nothing, and a publish of the same commit, move
	// nothing.
	e.svc.TaskReadyForReview(ctx, tk, "Still a cart.")
	e.svc.BranchPublished(ctx, e.owner, e.pid(), "task/1", "head2")
	e.svc.BranchPublished(ctx, e.owner, e.pid(), "task/1", "head3")
	e.svc.BranchPublished(ctx, e.stranger, e.pid(), "task/1", "evil")

	d, err := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	type move struct {
		source       PushSource
		before, head string
	}
	var got []move
	for _, p := range d.Pushes {
		got = append(got, move{p.Source, p.BeforeSHA, p.HeadSHA})
		if p.CreatedAt.IsZero() {
			t.Errorf("push %d has no time", p.ID)
		}
	}
	want := []move{{PushTask, "head1", "head2"}, {PushWorkspace, "head2", "head3"}}
	if !slices.Equal(got, want) {
		t.Fatalf("pushes = %+v, want %+v", got, want)
	}
	if d.MergeRequest.HeadSHA != "head3" || d.MergeRequest.Description != "Still a cart." {
		t.Fatalf("unexpected merge request: %+v", d.MergeRequest)
	}
}

func TestCommitsListFromTheTargetUntilMerged(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	log, err := e.svc.Commits(ctx, e.owner, e.pid(), mr.ID)
	if err != nil || len(log.Commits) != 1 || log.Commits[0].SHA != "head1" {
		t.Fatalf("commits: %+v %v", log, err)
	}
	if _, err := e.svc.Merge(ctx, e.owner, e.pid(), mr.ID, MergeInput{}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := e.svc.Commits(ctx, e.owner, e.pid(), mr.ID); err != nil {
		t.Fatalf("commits after merge: %v", err)
	}
	want := [][2]string{{"main", "head1"}, {"mergebase", "head1"}}
	if !slices.Equal(e.projects.logs, want) {
		t.Fatalf("logs = %v, want %v", e.projects.logs, want)
	}
	if _, err := e.svc.Commits(ctx, e.stranger, e.pid(), mr.ID); err == nil {
		t.Fatal("a stranger listed the commits")
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

// Reconcile fails reviews whose agent the restart interrupted, and starts
// again the ones that were still waiting for a sandbox.
func TestReconcileSettlesInterruptedReviews(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	rv := e.startAgentReview(t, mr, AgentReviewInput{})
	stranded := &Review{MergeRequestID: mr.ID, Author: AuthorAgent, Status: ReviewPending, Model: "m", WorkspaceID: rv.WorkspaceID}
	e.svc.repo.CreateReview(ctx, stranded)
	waiting := &Review{MergeRequestID: mr.ID, Author: AuthorAgent, Status: ReviewPending, HeadSHA: mr.HeadSHA, AgentID: domain.ReviewAgentID, Model: "m"}
	e.svc.repo.CreateReview(ctx, waiting)
	e.db.Exec(`UPDATE ai_agent_runs SET status = 'failed', error = 'agent run stopped because the server restarted or shut down' WHERE id = ?`, rv.RunID)

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitFor(t, "the interrupted reviews to fail and the waiting one to start", func() bool {
		a, _ := e.svc.repo.GetReview(ctx, rv.ID)
		b, _ := e.svc.repo.GetReview(ctx, stranded.ID)
		c, _ := e.svc.repo.GetReview(ctx, waiting.ID)
		return a.Status == ReviewFailed && b.Status == ReviewFailed && c.RunID != 0
	})
	got, _ := e.svc.repo.GetReview(ctx, rv.ID)
	if !strings.Contains(got.Error, "server restarted") {
		t.Fatalf("unexpected error: %q", got.Error)
	}
	if got, _ := e.svc.repo.GetReview(ctx, waiting.ID); got.Status != ReviewPending || got.WorkspaceID == 0 {
		t.Fatalf("expected the waiting review running in a sandbox, got %+v", got)
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
	mux.HandleFunc("POST /api/projects/{id}/merge-requests/{mrId}/send-back", h.HandleSendBack)
	mux.HandleFunc("POST /api/projects/{id}/merge-requests/{mrId}/comments", h.HandleComment)
	mux.HandleFunc("POST /api/projects/{id}/merge-requests/{mrId}/comments/{commentId}/resolve", h.HandleResolveThread)

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
			Task     struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
			} `json:"task"`
		} `json:"mergeRequest"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.MergeRequest.Status != "open" || len(got.MergeRequest.Reviews) != 1 || len(got.MergeRequest.Comments) != 1 || got.MergeRequest.Ahead != 2 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if got.MergeRequest.Task.ID != e.taskID || got.MergeRequest.Task.Status != "in_review" {
		t.Fatalf("expected the task in the detail: %s", rec.Body)
	}

	rec = do(e.owner, http.MethodPost, one+"/comments", `{"replyTo":`+itoa(int64(got.MergeRequest.Comments[0]["id"].(float64)))+`,"body":"Fixed."}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"replyTo":1`) {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}
	rec = do(e.owner, http.MethodPost, one+"/comments/2/resolve", `{"resolved":true}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":1`) || !strings.Contains(rec.Body.String(), `"resolvedBy":"user"`) {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
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
		{"empty comment", e.owner, http.MethodPost, one + "/comments", `{"body":" "}`, http.StatusBadRequest},
		{"other user's comment", e.stranger, http.MethodPost, one + "/comments", `{"body":"x"}`, http.StatusNotFound},
		{"resolve a comment elsewhere", e.owner, http.MethodPost, one + "/comments/999/resolve", `{"resolved":true}`, http.StatusBadRequest},
		{"bad comment id", e.owner, http.MethodPost, one + "/comments/x/resolve", `{"resolved":true}`, http.StatusBadRequest},
		{"send back", e.owner, http.MethodPost, one + "/send-back", `{"message":"Fix the typo."}`, http.StatusAccepted},
		{"send back while the agent works", e.owner, http.MethodPost, one + "/send-back", `{"message":"Again."}`, http.StatusConflict},
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

func TestAgentReviewStartsOnceSetupFinishes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	gate := make(chan struct{})
	e.projects.mu.Lock()
	e.projects.setupGate = gate
	e.projects.setup = &project.Setup{Script: "go mod download", Status: project.SetupSucceeded}
	e.projects.mu.Unlock()

	rv, err := e.svc.StartAgentReview(ctx, e.owner, e.pid(), mr.ID, AgentReviewInput{})
	if err != nil {
		t.Fatalf("start agent review: %v", err)
	}
	waitFor(t, "the wait for setup", func() bool { return e.projects.awaitedCount() == 1 })
	got, _ := e.svc.repo.GetReview(ctx, rv.ID)
	if got.Status != ReviewPending || got.WorkspaceID == 0 || got.RunID != 0 || len(e.runs.started()) != 0 {
		t.Fatalf("expected the review waiting on its sandbox, got %+v", got)
	}

	close(gate)
	waitFor(t, "the review run", func() bool { return len(e.runs.started()) == 1 })
	prompt := e.runs.started()[0].Turns[0].Text()
	for _, want := range []string{"## Setup", "go mod download", "It succeeded.", "## How to review"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestCloseWhileReviewSetupRunsDoesNotWaitForIt(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	gate := make(chan struct{})
	e.projects.mu.Lock()
	e.projects.setupGate = gate
	e.projects.mu.Unlock()

	rv, err := e.svc.StartAgentReview(ctx, e.owner, e.pid(), mr.ID, AgentReviewInput{})
	if err != nil {
		t.Fatalf("start agent review: %v", err)
	}
	waitFor(t, "the wait for setup", func() bool { return e.projects.awaitedCount() == 1 })

	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Close(ctx, e.owner, e.pid(), mr.ID)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closing waited for the review's setup script")
	}
	close(gate)
	time.Sleep(20 * time.Millisecond)
	if len(e.runs.started()) != 0 {
		t.Fatal("a review agent started for a closed merge request")
	}
	if got, _ := e.svc.repo.GetReview(ctx, rv.ID); got.Status != ReviewFailed || got.WorkspaceID != 0 {
		t.Fatalf("expected the review ended with its sandbox removed, got %+v", got)
	}
}

func TestReviewPromptSaysTheEnvironmentWasStillBuilding(t *testing.T) {
	e := setup(t)
	mr := e.openTaskMR(t)
	e.projects.mu.Lock()
	e.projects.environment = &project.Environment{Build: &project.Build{Status: project.BuildRunning}}
	e.projects.mu.Unlock()
	e.startAgentReview(t, mr, AgentReviewInput{})
	prompt := e.runs.started()[0].Turns[0].Text()
	for _, want := range []string{"## Environment", "still building", "devpad's own image without it"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// A review without a picked model runs the one the admin set for the review
// agent rather than the task's.
func TestAgentReviewUsesTheReviewAgentsDefaults(t *testing.T) {
	e := setup(t)
	e.defaults[domain.ReviewAgentID] = domain.AgentDefaults{AgentID: domain.ReviewAgentID, Model: "admin-review", Effort: "medium"}
	mr := e.openTaskMR(t)

	rv, err := e.svc.StartAgentReview(context.Background(), e.owner, e.pid(), mr.ID, AgentReviewInput{})
	if err != nil {
		t.Fatalf("start agent review: %v", err)
	}
	if rv.AgentID != domain.ReviewAgentID || rv.Model != "admin-review" || rv.Effort != "medium" {
		t.Fatalf("expected the review agent on the admin's model, got %+v", rv)
	}
}

func TestAgentReviewKeepsAPickedModel(t *testing.T) {
	e := setup(t)
	e.defaults[domain.ReviewAgentID] = domain.AgentDefaults{AgentID: domain.ReviewAgentID, Model: "admin-review", Effort: "medium"}
	mr := e.openTaskMR(t)

	rv, err := e.svc.StartAgentReview(context.Background(), e.owner, e.pid(), mr.ID, AgentReviewInput{Model: "picked", Effort: "low"})
	if err != nil {
		t.Fatalf("start agent review: %v", err)
	}
	if rv.Model != "picked" || rv.Effort != "low" {
		t.Fatalf("expected the picked model, got %+v", rv)
	}
}

// A project that reviews automatically starts the review agent on each new
// merge request, once, and its requested changes wait for the user.
func TestAutoReviewStartsOnNewMergeRequests(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.projects.project.AutoReview = true
	mr := e.openTaskMR(t)
	waitFor(t, "the automatic review run", func() bool { return len(e.runs.started()) == 1 })
	reviews, _ := e.svc.repo.ListReviews(ctx, mr.ID)
	if len(reviews) != 1 || reviews[0].Author != AuthorAgent || reviews[0].AgentID != domain.ReviewAgentID || reviews[0].Model != "claude-opus-5-5" || reviews[0].AutoFix {
		t.Fatalf("expected one review agent on the task's model without auto-fix, got %+v", reviews)
	}

	// New commits on the same merge request are not a new merge request.
	tk, _ := e.tasks.Get(ctx, e.owner, e.pid(), e.taskID)
	tk.HeadSHA = "head2"
	e.svc.TaskReadyForReview(ctx, tk, "")
	if reviews, _ := e.svc.repo.ListReviews(ctx, mr.ID); len(reviews) != 1 {
		t.Fatalf("expected no second review for new commits, got %d", len(reviews))
	}

	e.defaults[domain.ReviewAgentID] = domain.AgentDefaults{AgentID: domain.ReviewAgentID, Model: "admin-review"}
	e.projects.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "feature", SHA: "f1"}}
	created, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	waitFor(t, "the second automatic review run", func() bool { return len(e.runs.started()) == 2 })
	if reviews, _ := e.svc.repo.ListReviews(ctx, created.ID); len(reviews) != 1 || reviews[0].Model != "admin-review" {
		t.Fatalf("expected the review agent on the admin's model, got %+v", reviews)
	}
}

func TestNoAutoReviewUnlessTheProjectAsks(t *testing.T) {
	e := setup(t)
	mr := e.openTaskMR(t)
	time.Sleep(20 * time.Millisecond)
	if reviews, _ := e.svc.repo.ListReviews(context.Background(), mr.ID); len(reviews) != 0 || len(e.runs.started()) != 0 {
		t.Fatalf("expected no review, got %+v", reviews)
	}
}

// An automatic review with no model to run is recorded as failed, so the
// merge request says why it was not reviewed.
func TestAutoReviewWithoutAModelSaysWhy(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.projects.project.AutoReview = true
	e.projects.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "feature", SHA: "f1"}}
	mr, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	reviews, _ := e.svc.repo.ListReviews(ctx, mr.ID)
	if len(reviews) != 1 || reviews[0].Status != ReviewFailed || !strings.Contains(reviews[0].Error, "no default model") {
		t.Fatalf("expected a failed review saying there is no model, got %+v", reviews)
	}
	if _, err := e.svc.StartAgentReview(ctx, e.owner, e.pid(), mr.ID, AgentReviewInput{Model: "picked"}); err != nil {
		t.Fatalf("a failed automatic review should not block starting one: %v", err)
	}
}

// A review started while the project has no sandbox slot free waits for one
// instead of failing.
func TestAgentReviewWaitsForAFreeSandbox(t *testing.T) {
	retry := sandboxRetry
	sandboxRetry = 10 * time.Millisecond
	t.Cleanup(func() { sandboxRetry = retry })
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	e.projects.mu.Lock()
	e.projects.sandboxErr = project.ErrSandboxLimit
	e.projects.mu.Unlock()

	rv, err := e.svc.StartAgentReview(ctx, e.owner, e.pid(), mr.ID, AgentReviewInput{})
	if err != nil {
		t.Fatalf("start agent review: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got, _ := e.svc.repo.GetReview(ctx, rv.ID); got.Status != ReviewPending || got.WorkspaceID != 0 {
		t.Fatalf("expected the review waiting for a sandbox, got %+v", got)
	}

	e.projects.mu.Lock()
	e.projects.sandboxErr = nil
	e.projects.mu.Unlock()
	waitFor(t, "the review run", func() bool { return len(e.runs.started()) == 1 })
}

func TestSendBackStartsTheTaskAgentWithTheFeedback(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	if _, err := e.svc.SendBack(ctx, e.owner, e.pid(), mr.ID, SendBackInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected nothing to send without feedback or a note, got %v", err)
	}
	// An approval is not feedback to send back.
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{Verdict: VerdictApprove}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := e.svc.SendBack(ctx, e.owner, e.pid(), mr.ID, SendBackInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected an approval alone refused, got %v", err)
	}
	// Feedback on an older head was for an earlier round.
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{Verdict: VerdictComment, Body: "Old round."}); err != nil {
		t.Fatalf("review: %v", err)
	}
	e.svc.BranchPublished(ctx, e.owner, e.pid(), "task/1", "head2")
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{
		Verdict: VerdictComment, Body: "Rename the cart.",
		Comments: []CommentInput{{Path: "cart.go", Line: 4, Body: "Call it basket."}},
	}); err != nil {
		t.Fatalf("review: %v", err)
	}

	tk, err := e.svc.SendBack(ctx, e.owner, e.pid(), mr.ID, SendBackInput{Message: "  Also add tests.  "})
	if err != nil || tk.Status != task.StatusQueued {
		t.Fatalf("send back: %+v %v", tk, err)
	}
	starts := e.tasks.started()
	if len(starts) != 1 {
		t.Fatalf("expected the task's agent started once, got %+v", starts)
	}
	msg := starts[0].Message
	for _, want := range []string{"!1", "Also add tests.", "The reviewer left comments.", "Rename the cart.", "cart.go:4: Call it basket.", readCommentsTool, mrCommentTool} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "Old round.") {
		t.Errorf("feedback on an older head should be left to the tools:\n%s", msg)
	}
	d, _ := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	last := d.Comments[len(d.Comments)-1]
	if last.Author != AuthorUser || last.Body != "Also add tests." || last.Path != "" || last.ReviewID != 0 || last.HeadSHA != "head2" {
		t.Fatalf("the note should be kept as a comment: %+v", last)
	}
	if d.Task == nil || d.Task.Status != task.StatusQueued {
		t.Fatalf("the detail should carry the task: %+v", d.Task)
	}

	// The agent is working now: it cannot be sent back again.
	if _, err := e.svc.SendBack(ctx, e.owner, e.pid(), mr.ID, SendBackInput{Message: "More."}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState while the agent works, got %v", err)
	}
	if _, err := e.svc.SendBack(ctx, e.stranger, e.pid(), mr.ID, SendBackInput{Message: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected another user refused, got %v", err)
	}
}

func TestSendBackNeedsATask(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.projects.branches = []agent.RepoRef{{Name: "feature", SHA: "f1"}}
	mr, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := e.svc.SendBack(ctx, e.owner, e.pid(), mr.ID, SendBackInput{Message: "x"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

// taskSandbox gives the task a running sandbox on its branch, as a round of
// its agent has.
func (e *env) taskSandbox(t *testing.T) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO workspaces (user_id, name, status, project_id, kind, branch) VALUES (?, 'Task #1', 'running', ?, 'task', 'task/1')`, e.owner, e.pid())
	if err != nil {
		t.Fatalf("insert sandbox: %v", err)
	}
	id, _ := res.LastInsertId()
	e.tasks.mu.Lock()
	e.tasks.tasks[e.taskID].WorkspaceID = id
	e.tasks.mu.Unlock()
	return id
}

func TestTaskAgentReadsAndAnswersTheComments(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	wsID := e.taskSandbox(t)
	if got := e.svc.Tools(ctx, e.owner, wsID); got != nil {
		t.Fatal("a task without a merge request gets no merge request tools")
	}

	mr := e.openTaskMR(t)
	if got := e.svc.Tools(ctx, e.owner, wsID); len(got) != 2 {
		t.Fatalf("expected the task agent's tools, got %d", len(got))
	}
	if got := e.svc.Tools(ctx, e.stranger, wsID); got != nil {
		t.Fatal("another user's runs must not get the tools")
	}
	if prompt := e.svc.Prompt(ctx, e.owner, wsID); !strings.Contains(prompt, "!1") || !strings.Contains(prompt, mrCommentTool) {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{
		Verdict: VerdictRequestChanges, Body: "Handle an empty cart.",
		Comments: []CommentInput{{Path: "cart.go", Line: 12, Body: "This panics on nil."}},
	}); err != nil {
		t.Fatalf("review: %v", err)
	}
	d, _ := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	lineComment := d.Comments[0]

	result, _ := e.tool(e.owner, wsID, readCommentsTool, `{}`)
	if result.IsError {
		t.Fatalf("read: %+v", result)
	}
	var read struct {
		Reviews []struct {
			Author  string `json:"author"`
			Verdict string `json:"verdict"`
			Body    string `json:"body"`
		} `json:"reviews"`
		Comments []struct {
			ID       int64  `json:"id"`
			Author   string `json:"author"`
			Path     string `json:"path"`
			Line     int    `json:"line"`
			Body     string `json:"body"`
			ReplyTo  int64  `json:"reply_to"`
			Outdated bool   `json:"outdated"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(result.Content), &read); err != nil {
		t.Fatalf("decode %s: %v", result.Content, err)
	}
	if len(read.Reviews) != 1 || read.Reviews[0].Verdict != "request_changes" || len(read.Comments) != 1 || read.Comments[0].ID != lineComment.ID || read.Comments[0].Path != "cart.go" {
		t.Fatalf("unexpected discussion: %s", result.Content)
	}

	reply, _ := e.tool(e.owner, wsID, mrCommentTool, fmt.Sprintf(`{"reply_to":%d,"body":"Added a nil check."}`, lineComment.ID))
	if reply.IsError {
		t.Fatalf("reply: %+v", reply)
	}
	if general, _ := e.tool(e.owner, wsID, mrCommentTool, `{"body":"Ready for another look."}`); general.IsError {
		t.Fatalf("comment: %+v", general)
	}
	d, _ = e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	if len(d.Comments) != 3 {
		t.Fatalf("expected two task agent comments, got %+v", d.Comments)
	}
	answer, general := d.Comments[1], d.Comments[2]
	if answer.Author != AuthorTaskAgent || answer.ReplyTo != lineComment.ID || answer.Path != "cart.go" || answer.Line != 12 || answer.ReviewID != 0 {
		t.Fatalf("a reply should sit with its comment: %+v", answer)
	}
	if general.ReplyTo != 0 || general.Path != "" || general.HeadSHA != "head1" {
		t.Fatalf("unexpected general comment: %+v", general)
	}
	// A reply to a reply answers the thread.
	if r, _ := e.tool(e.owner, wsID, mrCommentTool, fmt.Sprintf(`{"reply_to":%d,"body":"And a test."}`, answer.ID)); r.IsError {
		t.Fatalf("reply to reply: %+v", r)
	}
	if d, _ = e.svc.Get(ctx, e.owner, e.pid(), mr.ID); d.Comments[3].ReplyTo != lineComment.ID {
		t.Fatalf("expected the thread's first comment as reply_to: %+v", d.Comments[3])
	}

	for _, args := range []string{`{"body":"  "}`, `{"reply_to":999,"body":"x"}`, `{"body":"` + strings.Repeat("x", maxCommentBytes+1) + `"}`} {
		if r, _ := e.tool(e.owner, wsID, mrCommentTool, args); !r.IsError {
			t.Errorf("%.40s: expected refused, got %+v", args, r)
		}
	}
	if r, _ := e.tool(e.stranger, wsID, mrCommentTool, `{"body":"x"}`); !r.IsError {
		t.Fatalf("another user must not comment: %+v", r)
	}

	// Once the sandbox is no longer the task's, it loses the tools.
	e.tasks.mu.Lock()
	e.tasks.tasks[e.taskID].WorkspaceID = 0
	e.tasks.mu.Unlock()
	if got := e.svc.Tools(ctx, e.owner, wsID); got != nil {
		t.Fatal("a sandbox the task no longer has must not get the tools")
	}
	if r, _ := e.tool(e.owner, wsID, mrCommentTool, `{"body":"x"}`); !r.IsError {
		t.Fatalf("expected refused: %+v", r)
	}
}

func TestTaskPushKeepsItsAgentsCommentsCurrent(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	wsID := e.taskSandbox(t)
	mr := e.openTaskMR(t)
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{
		Verdict: VerdictRequestChanges, Body: "Handle an empty cart.",
		Comments: []CommentInput{{Path: "cart.go", Line: 12, Body: "This panics on nil."}},
	}); err != nil {
		t.Fatalf("review: %v", err)
	}
	d, _ := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	userThread := d.Comments[0]

	// The agent answers during its round, before its commits land.
	if r, _ := e.tool(e.owner, wsID, mrCommentTool, fmt.Sprintf(`{"reply_to":%d,"body":"Added a nil check."}`, userThread.ID)); r.IsError {
		t.Fatalf("reply: %+v", r)
	}
	if r, _ := e.tool(e.owner, wsID, mrCommentTool, `{"body":"Also renamed the cart package."}`); r.IsError {
		t.Fatalf("comment: %+v", r)
	}
	d, _ = e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	agentThread := d.Comments[2]
	if _, err := e.svc.Comment(ctx, e.owner, e.pid(), mr.ID, UserCommentInput{ReplyTo: agentThread.ID, Body: "Why?"}); err != nil {
		t.Fatalf("user reply: %v", err)
	}

	tk, _ := e.tasks.Get(ctx, e.owner, e.pid(), e.taskID)
	tk.HeadSHA = "head2"
	e.svc.TaskReadyForReview(ctx, tk, "")

	heads := func() map[int64]string {
		d, err := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		got := map[int64]string{}
		for _, c := range d.Comments {
			got[c.ID] = c.HeadSHA
		}
		return got
	}
	got := heads()
	// The user's thread is still on the code it was written about; the
	// agent's thread, with its replies, is about the commits just pushed.
	want := map[int64]string{userThread.ID: "head1", userThread.ID + 1: "head1", agentThread.ID: "head2", agentThread.ID + 1: "head2"}
	if !maps.Equal(got, want) {
		t.Fatalf("heads = %v, want %v", got, want)
	}

	// Any other push leaves them behind.
	e.svc.BranchPublished(ctx, e.owner, e.pid(), "task/1", "head3")
	if got := heads(); got[agentThread.ID] != "head2" {
		t.Fatalf("a publish moved the agent's thread: %v", got)
	}
}

func TestReReviewIsToldAboutEarlierReviews(t *testing.T) {
	e := setup(t)
	mr := e.openTaskMR(t)
	first := e.startAgentReview(t, mr, AgentReviewInput{})
	if prompt := e.runs.started()[0].Turns[0].Text(); strings.Contains(prompt, "Earlier reviews") {
		t.Fatalf("a first review has no earlier reviews:\n%s", prompt)
	}
	e.tool(e.owner, first.WorkspaceID, commentTool, `{"path":"cart.go","line":3,"body":"No test covers this.","blocking":true}`)
	e.tool(e.owner, first.WorkspaceID, submitTool, `{"verdict":"request_changes","body":"Needs tests."}`)
	e.runs.finish(first.RunID, domain.AgentRunCompleted, "")
	waitFor(t, "the first review sandbox to stop", func() bool { stopped, _ := e.workspaces.calls(); return len(stopped) == 1 })

	second := e.startAgentReview(t, mr, AgentReviewInput{})
	if prompt := e.runs.started()[1].Turns[0].Text(); !strings.Contains(prompt, "Earlier reviews") || !strings.Contains(prompt, readCommentsTool) {
		t.Fatalf("a re-review should be pointed at the earlier reviews:\n%s", prompt)
	}
	result, _ := e.tool(e.owner, second.WorkspaceID, readCommentsTool, `{}`)
	if result.IsError || !strings.Contains(result.Content, "Needs tests.") || !strings.Contains(result.Content, `"review_agent"`) || !strings.Contains(result.Content, `"blocking":true`) {
		t.Fatalf("the review agent should read the earlier review: %+v", result)
	}
	// Nothing moved since the first review, so only blocking problems are new.
	if prompt := e.runs.started()[1].Turns[0].Text(); !strings.Contains(prompt, "(1 blocking)") || !strings.Contains(prompt, "of this same commit") {
		t.Fatalf("a re-review of the same commit should check the threads:\n%s", prompt)
	}
	e.tool(e.owner, second.WorkspaceID, submitTool, `{"verdict":"request_changes","body":"Still needs tests."}`)
	e.runs.finish(second.RunID, domain.AgentRunCompleted, "")
	waitFor(t, "the second review sandbox to stop", func() bool { stopped, _ := e.workspaces.calls(); return len(stopped) == 2 })

	// After a push, the next review looks at what changed since.
	e.svc.BranchPublished(context.Background(), e.owner, e.pid(), "task/1", "head2")
	e.startAgentReview(t, mr, AgentReviewInput{})
	if prompt := e.runs.started()[2].Turns[0].Text(); !strings.Contains(prompt, "`git diff head1 HEAD`") || !strings.Contains(prompt, "only if it is blocking") {
		t.Fatalf("a re-review should be scoped to what changed since the last one:\n%s", prompt)
	}
}

func TestReviewToolSchemasAreValidJSON(t *testing.T) {
	for _, tool := range append(slices.Clone(reviewTools), taskTools...) {
		if !json.Valid(tool.Function.Parameters) {
			t.Errorf("%s: invalid parameters schema: %s", tool.Function.Name, tool.Function.Parameters)
		}
	}
}

func TestAssistantCommentsOnAHandMadeMergeRequest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.projects.branches = []agent.RepoRef{{Name: "main", SHA: "m"}, {Name: "feature", SHA: "f1"}}
	mr, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{
		Verdict: VerdictRequestChanges, Body: "Almost.",
		Comments: []CommentInput{{Path: "cart.go", Line: 12, Body: "This panics on nil."}},
	}); err != nil {
		t.Fatalf("review: %v", err)
	}
	d, _ := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	lineComment := d.Comments[0]

	reply, err := e.svc.AssistantComment(ctx, e.owner, e.pid(), mr.ID, AgentCommentInput{ReplyTo: lineComment.ID, Body: "Added a nil check."})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if reply.Author != AuthorAssistant || reply.ReplyTo != lineComment.ID || reply.Path != "cart.go" || reply.Line != 12 {
		t.Fatalf("a reply should sit with its comment: %+v", reply)
	}
	general, err := e.svc.AssistantComment(ctx, e.owner, e.pid(), mr.ID, AgentCommentInput{Body: "Ready for another look."})
	if err != nil || general.ReplyTo != 0 || general.Path != "" || general.HeadSHA != "f1" {
		t.Fatalf("comment: %+v %v", general, err)
	}

	if _, err := e.svc.AssistantComment(ctx, e.stranger, e.pid(), mr.ID, AgentCommentInput{Body: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user must not comment: %v", err)
	}
	if _, err := e.svc.AssistantComment(ctx, e.owner, e.pid(), mr.ID, AgentCommentInput{Body: "  "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected an empty comment refused: %v", err)
	}
	if _, err := e.svc.AssistantComment(ctx, e.owner, e.pid(), mr.ID, AgentCommentInput{ReplyTo: 999, Body: "x"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected a reply to an unknown comment refused: %v", err)
	}

	for i := 2; i < maxAssistantComments; i++ {
		if _, err := e.svc.AssistantComment(ctx, e.owner, e.pid(), mr.ID, AgentCommentInput{Body: "more"}); err != nil {
			t.Fatalf("comment %d: %v", i, err)
		}
	}
	if _, err := e.svc.AssistantComment(ctx, e.owner, e.pid(), mr.ID, AgentCommentInput{Body: "one too many"}); !errors.Is(err, ErrCommentLimit) {
		t.Fatalf("expected the comment limit, got %v", err)
	}

	if _, err := e.svc.Close(ctx, e.owner, e.pid(), mr.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := e.svc.AssistantComment(ctx, e.owner, e.pid(), mr.ID, AgentCommentInput{Body: "x"}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected a closed merge request refused: %v", err)
	}
}

func TestUserCommentsAndResolvesThreads(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)

	general, err := e.svc.Comment(ctx, e.owner, e.pid(), mr.ID, UserCommentInput{Body: "  Looks close.  "})
	if err != nil || general.Author != AuthorUser || general.Body != "Looks close." || general.Path != "" || general.ReviewID != 0 || general.HeadSHA != "head1" {
		t.Fatalf("general comment: %+v %v", general, err)
	}
	line, err := e.svc.Comment(ctx, e.owner, e.pid(), mr.ID, UserCommentInput{Path: "cart.go", Line: 7, Body: "Off by one."})
	if err != nil || line.Path != "cart.go" || line.Line != 7 {
		t.Fatalf("line comment: %+v %v", line, err)
	}
	answer, err := e.svc.AssistantComment(ctx, e.owner, e.pid(), mr.ID, AgentCommentInput{ReplyTo: line.ID, Body: "Fixed."})
	if err != nil {
		t.Fatalf("assistant reply: %v", err)
	}
	// A reply to a reply joins the thread, where the thread sits.
	e.svc.BranchPublished(ctx, e.owner, e.pid(), "task/1", "head2")
	reply, err := e.svc.Comment(ctx, e.owner, e.pid(), mr.ID, UserCommentInput{ReplyTo: answer.ID, Path: "other.go", Line: 1, Body: "Thanks."})
	if err != nil || reply.ReplyTo != line.ID || reply.Path != "cart.go" || reply.Line != 7 || reply.HeadSHA != "head1" {
		t.Fatalf("reply: %+v %v", reply, err)
	}

	for _, in := range []UserCommentInput{
		{Body: " "},
		{Path: "../x", Line: 1, Body: "x"},
		{Path: "cart.go", Line: 0, Body: "x"},
		{ReplyTo: 999, Body: "x"},
		{Body: strings.Repeat("x", maxCommentBytes+1)},
	} {
		if _, err := e.svc.Comment(ctx, e.owner, e.pid(), mr.ID, in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%.40v: expected ErrInvalidInput, got %v", in, err)
		}
	}
	if _, err := e.svc.Comment(ctx, e.stranger, e.pid(), mr.ID, UserCommentInput{Body: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user must not comment: %v", err)
	}

	// Resolving through a reply resolves its thread, once.
	root, err := e.svc.ResolveThread(ctx, e.owner, e.pid(), mr.ID, reply.ID, true)
	if err != nil || root.ID != line.ID || root.ResolvedBy != AuthorUser || root.ResolvedAt == nil {
		t.Fatalf("resolve: %+v %v", root, err)
	}
	if again, err := e.svc.ResolveThread(ctx, e.owner, e.pid(), mr.ID, line.ID, true); err != nil || !again.ResolvedAt.Equal(*root.ResolvedAt) {
		t.Fatalf("resolving again should keep the first resolution: %+v %v", again, err)
	}
	if got, _ := e.svc.repo.GetComment(ctx, reply.ID); got.Resolved() {
		t.Fatal("a reply is never resolved itself")
	}
	d, _ := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	if n := openThreads(d.Comments); n != 1 {
		t.Fatalf("expected the general thread still open, got %d open", n)
	}
	if reopened, err := e.svc.ResolveThread(ctx, e.owner, e.pid(), mr.ID, line.ID, false); err != nil || reopened.Resolved() || reopened.ResolvedAt != nil {
		t.Fatalf("reopen: %+v %v", reopened, err)
	}
	if _, err := e.svc.ResolveThread(ctx, e.stranger, e.pid(), mr.ID, line.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user must not resolve: %v", err)
	}
	if _, err := e.svc.ResolveThread(ctx, e.owner, e.pid(), mr.ID, 999, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected an unknown comment refused: %v", err)
	}

	if _, err := e.svc.Close(ctx, e.owner, e.pid(), mr.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := e.svc.Comment(ctx, e.owner, e.pid(), mr.ID, UserCommentInput{Body: "x"}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected a closed merge request refused: %v", err)
	}
	if _, err := e.svc.ResolveThread(ctx, e.owner, e.pid(), mr.ID, general.ID, true); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected a closed merge request refused: %v", err)
	}
}

func TestReviewAgentResolvesAddressedThreads(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{
		Verdict: VerdictRequestChanges,
		Comments: []CommentInput{
			{Path: "cart.go", Line: 12, Body: "This panics on nil."},
			{Path: "cart.go", Line: 30, Body: "Needs a test."},
		},
	}); err != nil {
		t.Fatalf("review: %v", err)
	}
	d, _ := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	nilCheck, test := d.Comments[0], d.Comments[1]
	answer, err := e.svc.AssistantComment(ctx, e.owner, e.pid(), mr.ID, AgentCommentInput{ReplyTo: nilCheck.ID, Body: "Added a nil check."})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	rv := e.startAgentReview(t, mr, AgentReviewInput{})
	prompt := e.runs.started()[0].Turns[0].Text()
	for _, want := range []string{"2 comment threads on it are still open", resolveTool, mrCommentTool} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}

	read, _ := e.tool(e.owner, rv.WorkspaceID, readCommentsTool, `{}`)
	if read.IsError || !strings.Contains(read.Content, `"open_threads":2`) || !strings.Contains(read.Content, `"resolved":false`) {
		t.Fatalf("the agent should see the open threads: %+v", read)
	}

	// The thread is resolved through any of its comments, with a note.
	result, _ := e.tool(e.owner, rv.WorkspaceID, resolveTool, fmt.Sprintf(`{"comment_id":%d,"reply":"The nil check covers it."}`, answer.ID))
	if result.IsError || !strings.Contains(result.Content, fmt.Sprintf("Resolved thread %d.", nilCheck.ID)) {
		t.Fatalf("resolve: %+v", result)
	}
	if again, _ := e.tool(e.owner, rv.WorkspaceID, resolveTool, fmt.Sprintf(`{"comment_id":%d}`, nilCheck.ID)); !again.IsError || !strings.Contains(again.Content, "already resolved") {
		t.Fatalf("expected a resolved thread refused: %+v", again)
	}
	if unknown, _ := e.tool(e.owner, rv.WorkspaceID, resolveTool, `{"comment_id":999}`); !unknown.IsError {
		t.Fatalf("expected an unknown comment refused: %+v", unknown)
	}
	// The open thread gets a reply from the review instead, and a new
	// problem a thread of its own.
	if r, _ := e.tool(e.owner, rv.WorkspaceID, mrCommentTool, fmt.Sprintf(`{"reply_to":%d,"body":"Still no test."}`, test.ID)); r.IsError {
		t.Fatalf("reply: %+v", r)
	}
	if r, _ := e.tool(e.owner, rv.WorkspaceID, mrCommentTool, `{"body":"The README is out of date.","blocking":false}`); r.IsError {
		t.Fatalf("comment: %+v", r)
	}

	d, _ = e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	byID := map[int64]*Comment{}
	for _, c := range d.Comments {
		byID[c.ID] = c
	}
	if got := byID[nilCheck.ID]; got.ResolvedBy != AuthorAgent || got.ResolvedAt == nil {
		t.Fatalf("expected the thread resolved by the review agent: %+v", got)
	}
	if byID[test.ID].Resolved() {
		t.Fatal("the other thread should stay open")
	}
	var note, still, readme *Comment
	for _, c := range d.Comments[3:] {
		switch c.Body {
		case "The nil check covers it.":
			note = c
		case "Still no test.":
			still = c
		case "The README is out of date.":
			readme = c
		}
	}
	if note == nil || note.ReplyTo != nilCheck.ID || note.ReviewID != rv.ID || note.Author != AuthorAgent || note.Line != 12 {
		t.Fatalf("the resolving note should be a reply in the thread: %+v", note)
	}
	if still == nil || still.ReplyTo != test.ID || still.ReviewID != rv.ID || still.Path != "cart.go" {
		t.Fatalf("the reply should sit in its thread: %+v", still)
	}
	if readme == nil || readme.ReplyTo != 0 || readme.Path != "" || readme.ReviewID != rv.ID || readme.Resolved() {
		t.Fatalf("the general comment should start an open thread: %+v", readme)
	}

	if r, _ := e.tool(e.owner, rv.WorkspaceID, submitTool, `{"verdict":"request_changes","body":"One thread left, one new."}`); r.IsError {
		t.Fatalf("submit: %+v", r)
	}
	reqs := e.tasks.changeRequests()
	last := reqs[len(reqs)-1].feedback
	for _, want := range []string{fmt.Sprintf("in reply to comment %d: Still no test.", test.ID), "The README is out of date."} {
		if !strings.Contains(last, want) {
			t.Errorf("feedback missing %q:\n%s", want, last)
		}
	}
	if r, _ := e.tool(e.owner, rv.WorkspaceID, resolveTool, fmt.Sprintf(`{"comment_id":%d}`, test.ID)); !r.IsError {
		t.Fatalf("a submitted review must not resolve threads: %+v", r)
	}
}

func TestSendBackLeavesOutResolvedThreads(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	if _, err := e.svc.Review(ctx, e.owner, e.pid(), mr.ID, ReviewInput{
		Verdict: VerdictComment,
		Comments: []CommentInput{
			{Path: "cart.go", Line: 4, Body: "Call it basket."},
			{Path: "cart.go", Line: 9, Body: "Spelling."},
		},
	}); err != nil {
		t.Fatalf("review: %v", err)
	}
	d, _ := e.svc.Get(ctx, e.owner, e.pid(), mr.ID)
	if _, err := e.svc.ResolveThread(ctx, e.owner, e.pid(), mr.ID, d.Comments[1].ID, true); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := e.svc.SendBack(ctx, e.owner, e.pid(), mr.ID, SendBackInput{}); err != nil {
		t.Fatalf("send back: %v", err)
	}
	msg := e.tasks.started()[0].Message
	if !strings.Contains(msg, "Call it basket.") || strings.Contains(msg, "Spelling.") || !strings.Contains(msg, "1 comment thread on the merge request is still open") {
		t.Fatalf("expected only the open thread sent back:\n%s", msg)
	}
}

func TestSendBackWithOnlyAnOpenThreadFromAnEarlierRound(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	if _, err := e.svc.Comment(ctx, e.owner, e.pid(), mr.ID, UserCommentInput{Path: "cart.go", Line: 4, Body: "Call it basket."}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	e.svc.BranchPublished(ctx, e.owner, e.pid(), "task/1", "head2")
	if _, err := e.svc.SendBack(ctx, e.owner, e.pid(), mr.ID, SendBackInput{}); err != nil {
		t.Fatalf("an open thread is feedback to send back: %v", err)
	}
}
