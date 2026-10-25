package task

import (
	"context"
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
	"github.com/devpad-org/devpad/internal/auth"
	appdb "github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

// memRepo is an in-memory task Repository.
type memRepo struct {
	mu     sync.Mutex
	nextID int64
	tasks  map[int64]Task
}

func newMemRepo() *memRepo { return &memRepo{tasks: map[int64]Task{}} }

func (r *memRepo) Create(_ context.Context, t *Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	t.ID = r.nextID
	r.tasks[t.ID] = *t
	return nil
}

func (r *memRepo) GetByID(_ context.Context, id int64) (*Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[id]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

func (r *memRepo) GetByRunID(_ context.Context, runID int64) (*Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tasks {
		if t.RunID == runID {
			return &t, nil
		}
	}
	return nil, nil
}

func (r *memRepo) ListByProjectID(_ context.Context, projectID int64) ([]*Task, error) {
	return r.filter(func(t Task) bool { return t.ProjectID == projectID }), nil
}

func (r *memRepo) ListByStatus(_ context.Context, status Status) ([]*Task, error) {
	return r.filter(func(t Task) bool { return t.Status == status }), nil
}

func (r *memRepo) filter(keep func(Task) bool) []*Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*Task
	for id := int64(1); id <= r.nextID; id++ {
		if t, ok := r.tasks[id]; ok && keep(t) {
			out = append(out, &t)
		}
	}
	return out
}

func (r *memRepo) Update(_ context.Context, t *Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[t.ID] = *t
	return nil
}

func (r *memRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, id)
	return nil
}

// fakeSandboxes plays both the project service and the workspace lifecycle.
type fakeSandboxes struct {
	mu            sync.Mutex
	owner         int64
	project       project.Project
	nextID        int64
	byID          map[int64]*workspace.Workspace
	created       []project.SandboxInput
	started       []int64
	stopped       []int64
	deleted       []int64
	published     []int64
	synced        []int64
	ahead         int
	limit         int // running sandboxes allowed; 0 means unlimited
	createErr     error
	publishErr    error
	hiddenSandbox *workspace.Workspace // exists for its branch but CreateSandbox reports it in use
	branches      []string             // canonical branches besides the default
	projectWS     *workspace.Workspace // the project workspace, if opened
	// aheadOf overrides ahead for a compare against the given base, and
	// compared records each compare's base.
	aheadOf  map[string]int
	compared []string
	// setup is what AwaitSetup reports; while setupGate is open (non-nil
	// and not closed) it waits. awaited records each wait's sandbox.
	setup     *project.Setup
	setupGate chan struct{}
	awaited   []int64
	// environment is what AwaitEnvironment reports; while environmentGate
	// is open it waits.
	environment     *project.Environment
	environmentGate chan struct{}
}

func (f *fakeSandboxes) ProjectEnvironment(context.Context, int64, int64) (*project.Environment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.environment, nil
}

func (f *fakeSandboxes) AwaitEnvironment(ctx context.Context, _, _ int64) (*project.Environment, error) {
	f.mu.Lock()
	gate := f.environmentGate
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
	return f.environment, nil
}

func newFakeSandboxes(owner int64) *fakeSandboxes {
	return &fakeSandboxes{
		owner:   owner,
		project: project.Project{ID: 1, UserID: owner, Name: "shop", DefaultBranch: "main", Status: project.StatusReady},
		nextID:  100,
		byID:    map[int64]*workspace.Workspace{},
		ahead:   1,
	}
}

func (f *fakeSandboxes) Get(_ context.Context, userID, projectID int64) (*project.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if projectID != f.project.ID {
		return nil, project.ErrNotFound
	}
	if userID != f.owner {
		return nil, project.ErrForbidden
	}
	p := f.project
	return &p, nil
}

func (f *fakeSandboxes) Branches(_ context.Context, _, _ int64) (*agent.RepoRefs, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	refs := &agent.RepoRefs{DefaultBranch: f.project.DefaultBranch, Branches: []agent.RepoRef{{Name: f.project.DefaultBranch, SHA: "0001"}}}
	for _, b := range f.branches {
		refs.Branches = append(refs.Branches, agent.RepoRef{Name: b, SHA: "0002"})
	}
	return refs, nil
}

func (f *fakeSandboxes) Workspace(_ context.Context, userID, projectID int64) (*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if projectID != f.project.ID || userID != f.owner {
		return nil, project.ErrNotFound
	}
	if f.projectWS == nil {
		return nil, project.ErrNoWorkspace
	}
	clone := *f.projectWS
	return &clone, nil
}

func (f *fakeSandboxes) ListSandboxes(_ context.Context, _, _ int64) ([]*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*workspace.Workspace
	for _, ws := range f.byID {
		clone := *ws
		out = append(out, &clone)
	}
	if f.hiddenSandbox != nil {
		clone := *f.hiddenSandbox
		out = append(out, &clone)
	}
	return out, nil
}

func (f *fakeSandboxes) CreateSandbox(_ context.Context, userID, projectID int64, in project.SandboxInput) (*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, in)
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.full() {
		return nil, project.ErrSandboxLimit
	}
	f.nextID++
	ws := &workspace.Workspace{ID: f.nextID, UserID: userID, ProjectID: projectID, Kind: workspace.Kind(in.Kind), Branch: in.Branch, Name: in.Name, Status: workspace.StatusRunning}
	f.byID[ws.ID] = ws
	clone := *ws
	return &clone, nil
}

func (f *fakeSandboxes) StartSandbox(_ context.Context, _, _, workspaceID int64) (*workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, workspaceID)
	ws := f.lookup(workspaceID)
	if ws.Status == workspace.StatusStopped && f.full() {
		return nil, project.ErrSandboxLimit
	}
	ws.Status = workspace.StatusRunning
	clone := *ws
	return &clone, nil
}

func (f *fakeSandboxes) AwaitSetup(ctx context.Context, _, _, workspaceID int64) (*project.Setup, error) {
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
	if f.lookup(workspaceID) == nil {
		return nil, project.ErrNotFound
	}
	return f.setup, nil
}

func (f *fakeSandboxes) full() bool {
	if f.limit == 0 {
		return false
	}
	running := 0
	for _, ws := range f.byID {
		if ws.Status != workspace.StatusStopped {
			running++
		}
	}
	return running >= f.limit
}

func (f *fakeSandboxes) status(id int64) workspace.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookup(id).Status
}

func (f *fakeSandboxes) lookup(id int64) *workspace.Workspace {
	if f.hiddenSandbox != nil && f.hiddenSandbox.ID == id {
		return f.hiddenSandbox
	}
	return f.byID[id]
}

func (f *fakeSandboxes) PublishSandbox(_ context.Context, _, _, workspaceID int64) (*agent.RepoBranch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, workspaceID)
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	return &agent.RepoBranch{Branch: f.lookup(workspaceID).Branch, SHA: "f00d"}, nil
}

func (f *fakeSandboxes) SyncSandboxBase(_ context.Context, _, _, workspaceID int64) (*agent.RepoBranch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.synced = append(f.synced, workspaceID)
	return &agent.RepoBranch{Branch: "devpad/main"}, nil
}

func (f *fakeSandboxes) Compare(_ context.Context, _, _ int64, base, head string) (*agent.RepoComparison, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compared = append(f.compared, base)
	if ahead, ok := f.aheadOf[base]; ok {
		return &agent.RepoComparison{Base: base, Head: head, Ahead: ahead}, nil
	}
	return &agent.RepoComparison{Base: "main", Head: head, Ahead: f.ahead}, nil
}

func (f *fakeSandboxes) set(fn func(f *fakeSandboxes)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

type sandboxCalls struct {
	created                                               []project.SandboxInput
	started, stopped, deleted, published, synced, awaited []int64
}

func (f *fakeSandboxes) snapshot() sandboxCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return sandboxCalls{
		created:   append([]project.SandboxInput(nil), f.created...),
		started:   append([]int64(nil), f.started...),
		stopped:   append([]int64(nil), f.stopped...),
		deleted:   append([]int64(nil), f.deleted...),
		published: append([]int64(nil), f.published...),
		synced:    append([]int64(nil), f.synced...),
		awaited:   append([]int64(nil), f.awaited...),
	}
}

// workspaceLifecycle adapts fakeSandboxes to the Workspaces interface.
type workspaceLifecycle struct{ *fakeSandboxes }

func (w workspaceLifecycle) Get(_ context.Context, userID, workspaceID int64) (*workspace.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ws := w.lookup(workspaceID)
	if ws == nil {
		return nil, workspace.ErrNotFound
	}
	if ws.UserID != userID {
		return nil, workspace.ErrForbidden
	}
	clone := *ws
	return &clone, nil
}

func (w workspaceLifecycle) Stop(_ context.Context, _, workspaceID int64) (*workspace.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = append(w.stopped, workspaceID)
	ws := w.lookup(workspaceID)
	ws.Status = workspace.StatusStopped
	clone := *ws
	return &clone, nil
}

func (w workspaceLifecycle) Delete(_ context.Context, _, workspaceID int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lookup(workspaceID) == nil {
		return workspace.ErrNotFound
	}
	delete(w.byID, workspaceID)
	w.deleted = append(w.deleted, workspaceID)
	return nil
}

// fakeRuns records started runs and lets tests finish them.
type fakeRuns struct {
	mu        sync.Mutex
	nextID    int64
	runs      map[int64]*domain.AgentRun
	requests  []app.StartAgentRunRequest
	cancelled []int64
	onFinish  func(domain.AgentRun)
	// startStatus is the status new runs start in.
	startStatus domain.AgentRunStatus
	startErr    error
}

func newFakeRuns() *fakeRuns {
	return &fakeRuns{runs: map[int64]*domain.AgentRun{}, startStatus: domain.AgentRunRunning}
}

func (f *fakeRuns) StartRun(_ context.Context, req app.StartAgentRunRequest) (*domain.AgentRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return nil, f.startErr
	}
	f.nextID++
	run := &domain.AgentRun{ID: f.nextID, UserID: req.UserID, WorkspaceID: req.WorkspaceID, ConversationID: req.ConversationID, Model: req.Model, Status: f.startStatus}
	f.runs[run.ID] = run
	f.requests = append(f.requests, req)
	clone := *run
	return &clone, nil
}

func (f *fakeRuns) GetRun(_ context.Context, userID, runID int64) (*domain.AgentRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run, ok := f.runs[runID]
	if !ok || run.UserID != userID {
		return nil, domain.ErrAgentRunNotFound
	}
	clone := *run
	return &clone, nil
}

func (f *fakeRuns) CancelRun(_ context.Context, userID, runID int64) error {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, runID)
	f.mu.Unlock()
	f.finish(runID, domain.AgentRunCancelled, "agent run cancelled")
	return nil
}

func (f *fakeRuns) finish(runID int64, status domain.AgentRunStatus, msg string) {
	f.mu.Lock()
	run := f.runs[runID]
	run.Status, run.Error = status, msg
	clone := *run
	onFinish := f.onFinish
	f.mu.Unlock()
	if onFinish != nil {
		onFinish(clone)
	}
}

func (f *fakeRuns) set(fn func(f *fakeRuns)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

type runCalls struct{ cancelled []int64 }

func (f *fakeRuns) snapshot() runCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return runCalls{cancelled: append([]int64(nil), f.cancelled...)}
}

func (f *fakeRuns) startedRequests() []app.StartAgentRunRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]app.StartAgentRunRequest(nil), f.requests...)
}

type fakeConversations struct {
	mu     sync.Mutex
	nextID int64
	turns  map[int64][]domain.Turn
	convs  map[int64]domain.Conversation
	// getErr fails GetConversation, as a database error would.
	getErr error
}

func (f *fakeConversations) CreateConversation(_ context.Context, userID, workspaceID int64, model string) (*domain.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	return &domain.Conversation{ID: f.nextID, UserID: userID, WorkspaceID: workspaceID, Model: model}, nil
}

func (f *fakeConversations) GetConversation(_ context.Context, id, userID int64) (*domain.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	conv, ok := f.convs[id]
	if !ok || conv.UserID != userID {
		return nil, domain.ErrConversationNotFound
	}
	return &conv, nil
}

func (f *fakeConversations) GetTurns(_ context.Context, conversationID, _ int64) ([]domain.Turn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Turn(nil), f.turns[conversationID]...), nil
}

func (f *fakeConversations) save(id int64, turns ...domain.Turn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turns[id] = turns
}

type env struct {
	svc       *service
	repo      *memRepo
	sandboxes *fakeSandboxes
	runs      *fakeRuns
	convs     *fakeConversations
	defaults  fakeDefaults
}

// fakeDefaults is the admin's agent defaults, keyed by agent ID.
type fakeDefaults map[string]domain.AgentDefaults

func (f fakeDefaults) DefaultsFor(_ context.Context, agentID string) (*domain.AgentDefaults, error) {
	if agentID == "" {
		agentID = domain.DefaultAgentID
	}
	if d, ok := f[agentID]; ok {
		return &d, nil
	}
	return nil, nil
}

const owner, stranger = int64(1), int64(2)

func setup(t *testing.T) *env {
	t.Helper()
	e := &env{
		repo:      newMemRepo(),
		sandboxes: newFakeSandboxes(owner),
		runs:      newFakeRuns(),
		convs:     &fakeConversations{turns: map[int64][]domain.Turn{}, convs: map[int64]domain.Conversation{}},
		defaults:  fakeDefaults{},
	}
	e.svc = NewService(e.repo, e.sandboxes, workspaceLifecycle{e.sandboxes}, e.runs, e.convs, e.defaults).(*service)
	e.runs.onFinish = e.svc.RunFinished
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.svc.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return e
}

func (e *env) createTask(t *testing.T) *Task {
	t.Helper()
	task, err := e.svc.Create(context.Background(), owner, 1, CreateInput{
		Title: "Add a cart", Body: "Users need a cart.", AgentSettings: AgentSettings{Model: "claude-opus-5-5", Effort: "high"},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

func (e *env) start(t *testing.T, id int64, in StartInput) {
	t.Helper()
	if _, err := e.svc.Start(context.Background(), owner, 1, id, in); err != nil {
		t.Fatalf("start task: %v", err)
	}
}

// waitTask polls until the task satisfies cond.
func (e *env) waitTask(t *testing.T, id int64, cond func(*Task) bool) *Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		task, _ := e.repo.GetByID(context.Background(), id)
		if task != nil && cond(task) {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %d never reached the expected state; last: %+v", id, task)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func hasRun(task *Task) bool { return task.RunID != 0 }

func status(s Status) func(*Task) bool { return func(t *Task) bool { return t.Status == s } }

func lastText(turns []domain.Turn) string {
	last := turns[len(turns)-1]
	return last.Parts[len(last.Parts)-1].Text
}

func TestStartRunsAgentInSandboxThenCollectsBranchForReview(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	if task.Branch != "task/1" || task.Status != StatusTodo {
		t.Fatalf("unexpected new task: %+v", task)
	}

	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)
	if running.Status != StatusInProgress || running.WorkspaceID == 0 || running.ConversationID == 0 {
		t.Fatalf("unexpected running task: %+v", running)
	}
	if got := e.sandboxes.snapshot().created; len(got) != 1 || got[0].Branch != "task/1" || got[0].Kind != "task" {
		t.Fatalf("unexpected sandbox requests: %+v", got)
	}
	reqs := e.runs.startedRequests()
	if len(reqs) != 1 {
		t.Fatalf("expected one run, got %d", len(reqs))
	}
	req := reqs[0]
	if req.WorkspaceID != running.WorkspaceID || req.ConversationID != running.ConversationID || req.Model != "claude-opus-5-5" || req.Thinking == nil || req.Thinking.Effort != "high" {
		t.Fatalf("unexpected run request: %+v", req)
	}
	prompt := req.Turns[0].Text()
	for _, want := range []string{"task #1", `"shop"`, "## Add a cart", "Users need a cart.", "`task/1`", "`main`", "no remote"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}

	e.sandboxes.set(func(f *fakeSandboxes) { f.ahead = 2 })
	e.runs.finish(running.RunID, domain.AgentRunCompleted, "")
	done := e.waitTask(t, task.ID, status(StatusInReview))
	if done.HeadSHA != "f00d" || done.Error != "" {
		t.Fatalf("unexpected reviewed task: %+v", done)
	}
	if got := e.sandboxes.snapshot().stopped; len(got) != 1 || got[0] != running.WorkspaceID {
		t.Fatalf("expected the sandbox stopped after collection, got %v", got)
	}
}

type recordingHook struct {
	mu      sync.Mutex
	tasks   []Task
	summary []string
}

func (h *recordingHook) TaskReadyForReview(_ context.Context, t *Task, summary string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tasks = append(h.tasks, *t)
	h.summary = append(h.summary, summary)
}

func (h *recordingHook) calls() ([]Task, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Task(nil), h.tasks...), append([]string(nil), h.summary...)
}

func TestReviewHookGetsTheAgentSummary(t *testing.T) {
	e := setup(t)
	hook := &recordingHook{}
	e.svc.SetReviewHook(hook)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)
	if got := e.sandboxes.snapshot().synced; len(got) != 1 || got[0] != running.WorkspaceID {
		t.Fatalf("expected devpad/main refreshed before the run, got %v", got)
	}
	e.convs.save(running.ConversationID,
		domain.NewTextTurn(domain.RoleUser, "implement"),
		domain.NewTextTurn(domain.RoleAssistant, "Added the cart."),
		domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{{Kind: domain.PartToolResult, ToolResult: &domain.ToolResultPart{ToolCallID: "1"}}}},
	)
	e.runs.finish(running.RunID, domain.AgentRunCompleted, "")
	e.waitTask(t, task.ID, status(StatusInReview))

	deadline := time.Now().Add(2 * time.Second)
	for {
		tasks, summaries := hook.calls()
		if len(tasks) == 1 {
			if tasks[0].HeadSHA != "f00d" || tasks[0].Status != StatusInReview || summaries[0] != "Added the cart." {
				t.Fatalf("unexpected hook call: %+v %q", tasks[0], summaries[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("review hook was not called")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if prompt := e.runs.startedRequests()[0].Turns[0].Text(); !strings.Contains(prompt, "`devpad/main`") {
		t.Fatalf("prompt should point the agent at devpad/main:\n%s", prompt)
	}
}

func TestRequestChangesQueuesOrStartsFeedback(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)

	if _, err := e.svc.RequestChanges(ctx, owner, 1, task.ID, "Rename it.", false); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState while in progress, got %v", err)
	}
	e.convs.save(running.ConversationID, domain.NewTextTurn(domain.RoleUser, "implement"), domain.NewTextTurn(domain.RoleAssistant, "done"))
	e.runs.finish(running.RunID, domain.AgentRunCompleted, "")
	e.waitTask(t, task.ID, status(StatusInReview))

	if _, err := e.svc.RequestChanges(ctx, owner, 1, task.ID, "  ", false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty feedback, got %v", err)
	}
	held, err := e.svc.RequestChanges(ctx, owner, 1, task.ID, "Rename it.", false)
	if err != nil || held.Status != StatusChangesRequested || held.PendingMessage != "Rename it." {
		t.Fatalf("held feedback: %+v %v", held, err)
	}
	// A later plain Start delivers the held feedback.
	e.start(t, task.ID, StartInput{})
	second := e.waitTask(t, task.ID, func(t *Task) bool { return t.RunID != running.RunID && t.RunID != 0 })
	if text := lastText(e.runs.startedRequests()[1].Turns); !strings.Contains(text, "Rename it.") {
		t.Fatalf("held feedback not delivered: %q", text)
	}

	e.convs.save(second.ConversationID, domain.NewTextTurn(domain.RoleUser, "implement"), domain.NewTextTurn(domain.RoleAssistant, "renamed"))
	e.runs.finish(second.RunID, domain.AgentRunCompleted, "")
	e.waitTask(t, task.ID, status(StatusInReview))
	if _, err := e.svc.RequestChanges(ctx, owner, 1, task.ID, "Add a test.", true); err != nil {
		t.Fatalf("request changes with start: %v", err)
	}
	e.waitTask(t, task.ID, func(t *Task) bool { return t.RunID != second.RunID && t.RunID != 0 })
	if text := lastText(e.runs.startedRequests()[2].Turns); !strings.Contains(text, "Add a test.") {
		t.Fatalf("immediate feedback not delivered: %q", text)
	}
}

func TestMergedClosesTaskAndRemovesSandbox(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)
	e.runs.finish(running.RunID, domain.AgentRunCompleted, "")
	e.waitTask(t, task.ID, status(StatusInReview))

	merged, err := e.svc.Merged(ctx, owner, 1, task.ID)
	if err != nil || merged.Status != StatusMerged || merged.WorkspaceID != 0 {
		t.Fatalf("merged: %+v %v", merged, err)
	}
	if got := e.sandboxes.snapshot().deleted; len(got) != 1 || got[0] != running.WorkspaceID {
		t.Fatalf("expected the sandbox removed, got %v", got)
	}
	if _, err := e.svc.Start(ctx, owner, 1, task.ID, StartInput{}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("a merged task cannot start again, got %v", err)
	}
	if _, err := e.svc.Merged(ctx, owner, 1, task.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("merging twice should fail, got %v", err)
	}
}

func TestRunWithoutCommitsFailsTask(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)

	e.sandboxes.set(func(f *fakeSandboxes) { f.ahead = 0 })
	e.runs.finish(running.RunID, domain.AgentRunCompleted, "")
	failed := e.waitTask(t, task.ID, status(StatusFailed))
	if !strings.Contains(failed.Error, "without committing") {
		t.Fatalf("unexpected error: %q", failed.Error)
	}
	if got := e.sandboxes.snapshot().stopped; len(got) != 1 || got[0] != running.WorkspaceID {
		t.Fatalf("a failed round should stop its sandbox to free the slot, got %v", got)
	}
}

func TestFailedRunCanBeRetriedInTheSameConversation(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	first := e.waitTask(t, task.ID, hasRun)

	e.convs.save(first.ConversationID,
		domain.NewTextTurn(domain.RoleUser, "implement"),
		domain.NewTextTurn(domain.RoleAssistant, "working"),
	)
	e.runs.finish(first.RunID, domain.AgentRunFailed, "provider overloaded")
	failed := e.waitTask(t, task.ID, status(StatusFailed))
	if failed.Error != "provider overloaded" {
		t.Fatalf("unexpected error: %q", failed.Error)
	}

	e.start(t, task.ID, StartInput{})
	second := e.waitTask(t, task.ID, func(t *Task) bool { return t.RunID != first.RunID && t.RunID != 0 })
	if second.WorkspaceID != first.WorkspaceID || second.ConversationID != first.ConversationID || second.Error != "" {
		t.Fatalf("retry should reuse sandbox and conversation: first=%+v second=%+v", first, second)
	}
	if len(e.sandboxes.snapshot().created) != 1 {
		t.Fatalf("retry created another sandbox: %+v", e.sandboxes.snapshot().created)
	}
	turns := e.runs.startedRequests()[1].Turns
	if len(turns) != 3 || !strings.Contains(lastText(turns), "previous attempt stopped: provider overloaded") {
		t.Fatalf("unexpected retry turns: %+v", turns)
	}
}

func TestFeedbackRestartsStoppedSandbox(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	first := e.waitTask(t, task.ID, hasRun)
	e.convs.save(first.ConversationID, domain.NewTextTurn(domain.RoleUser, "implement"), domain.NewTextTurn(domain.RoleAssistant, "done"))
	e.runs.finish(first.RunID, domain.AgentRunCompleted, "")
	e.waitTask(t, task.ID, status(StatusInReview))

	e.start(t, task.ID, StartInput{Message: "Handle an empty cart.", AgentSettings: AgentSettings{Model: "claude-sonnet-5-5"}})
	second := e.waitTask(t, task.ID, func(t *Task) bool { return t.RunID != first.RunID && t.RunID != 0 })
	if got := e.sandboxes.snapshot().started; len(got) != 1 || got[0] != first.WorkspaceID {
		t.Fatalf("expected the stopped sandbox restarted through the project, got %v", got)
	}
	req := e.runs.startedRequests()[1]
	if req.Model != "claude-sonnet-5-5" || second.Model != "claude-sonnet-5-5" {
		t.Fatalf("model override not applied: req=%s task=%s", req.Model, second.Model)
	}
	if text := lastText(req.Turns); !strings.Contains(text, "Feedback on your work") || !strings.Contains(text, "Handle an empty cart.") {
		t.Fatalf("unexpected feedback turn: %q", text)
	}
}

func TestRetryAfterSandboxDeletedStartsFreshConversation(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	first := e.waitTask(t, task.ID, hasRun)
	e.runs.finish(first.RunID, domain.AgentRunFailed, "boom")
	e.waitTask(t, task.ID, status(StatusFailed))
	if err := (workspaceLifecycle{e.sandboxes}).Delete(context.Background(), owner, first.WorkspaceID); err != nil {
		t.Fatalf("delete sandbox: %v", err)
	}

	e.start(t, task.ID, StartInput{Message: "Try again."})
	second := e.waitTask(t, task.ID, func(t *Task) bool { return t.RunID != first.RunID && t.RunID != 0 })
	if second.WorkspaceID == first.WorkspaceID || second.ConversationID == first.ConversationID {
		t.Fatalf("expected a new sandbox and conversation: first=%+v second=%+v", first, second)
	}
	turns := e.runs.startedRequests()[1].Turns
	if len(turns) != 1 || !strings.Contains(turns[0].Text(), "## Add a cart") || !strings.Contains(turns[0].Text(), "Try again.") {
		t.Fatalf("expected a fresh implement prompt with the message, got %+v", turns)
	}
}

func TestAdoptsSandboxThatOwnsTheTaskBranch(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.sandboxes.set(func(f *fakeSandboxes) {
		f.hiddenSandbox = &workspace.Workspace{ID: 555, UserID: owner, ProjectID: 1, Kind: workspace.KindTask, Branch: task.Branch, Status: workspace.StatusStopped}
	})
	e.sandboxes.set(func(f *fakeSandboxes) { f.createErr = project.ErrBranchInUse })

	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)
	if running.WorkspaceID != 555 || len(e.sandboxes.snapshot().started) != 1 {
		t.Fatalf("expected the orphaned sandbox adopted and started: %+v started=%v", running, e.sandboxes.snapshot().started)
	}
}

func TestTaskWaitsWhileItsBranchIsBeingPublished(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.sandboxes.set(func(f *fakeSandboxes) { f.createErr = project.ErrBranchPublishing })

	e.start(t, task.ID, StartInput{})
	time.Sleep(50 * time.Millisecond)
	if got, _ := e.repo.GetByID(context.Background(), task.ID); got.Status != StatusQueued || got.Error != "" {
		t.Fatalf("expected the task to stay queued while its branch is published, got %+v", got)
	}

	e.sandboxes.set(func(f *fakeSandboxes) { f.createErr = nil })
	e.svc.kick()
	e.waitTask(t, task.ID, hasRun)
}

func TestLaunchFailuresAreRecorded(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.sandboxes.set(func(f *fakeSandboxes) { f.createErr = errors.New("docker unavailable") })
	e.start(t, task.ID, StartInput{})
	failed := e.waitTask(t, task.ID, status(StatusFailed))
	if !strings.Contains(failed.Error, "docker unavailable") {
		t.Fatalf("unexpected error: %q", failed.Error)
	}

	e.sandboxes.set(func(f *fakeSandboxes) { f.createErr = nil })
	e.runs.set(func(f *fakeRuns) { f.startErr = errors.New("no API key") })
	e.start(t, task.ID, StartInput{})
	failed = e.waitTask(t, task.ID, func(t *Task) bool { return t.Status == StatusFailed && strings.Contains(t.Error, "no API key") })
	if failed.WorkspaceID == 0 {
		t.Fatal("the sandbox created before the run failed should stay recorded on the task")
	}
}

func TestRunThatEndsBeforeItsIDIsSavedIsSettled(t *testing.T) {
	e := setup(t)
	e.runs.set(func(f *fakeRuns) { f.startStatus = domain.AgentRunCompleted })
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	e.waitTask(t, task.ID, status(StatusInReview))
	if len(e.sandboxes.snapshot().published) != 1 {
		t.Fatalf("expected one publish, got %v", e.sandboxes.snapshot().published)
	}
}

func TestPublishFailureFailsTask(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)
	e.sandboxes.set(func(f *fakeSandboxes) { f.publishErr = errors.New("sandbox branch changed") })
	e.runs.finish(running.RunID, domain.AgentRunCompleted, "")
	failed := e.waitTask(t, task.ID, status(StatusFailed))
	if !strings.Contains(failed.Error, "sandbox branch changed") {
		t.Fatalf("unexpected error: %q", failed.Error)
	}
}

func TestQueuedTasksLaunchWhenASlotFrees(t *testing.T) {
	e := setup(t)
	e.sandboxes.set(func(f *fakeSandboxes) { f.limit = 1 })
	first := e.createTask(t)
	second := e.createTask(t)

	e.start(t, first.ID, StartInput{})
	running := e.waitTask(t, first.ID, hasRun)
	if _, err := e.svc.Start(context.Background(), owner, 1, second.ID, StartInput{Message: "Start with the API."}); err != nil {
		t.Fatalf("start second: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got, _ := e.repo.GetByID(context.Background(), second.ID); got.Status != StatusQueued || got.PendingMessage != "Start with the API." {
		t.Fatalf("second task should wait in the queue with its message, got %+v", got)
	}

	e.runs.finish(running.RunID, domain.AgentRunCompleted, "")
	e.waitTask(t, first.ID, status(StatusInReview))
	if got := e.sandboxes.status(running.WorkspaceID); got != workspace.StatusStopped {
		t.Fatalf("the reviewed task's sandbox should be stopped, got %s", got)
	}
	launched := e.waitTask(t, second.ID, hasRun)
	if launched.Status != StatusInProgress || launched.PendingMessage != "" {
		t.Fatalf("second task should launch once the slot frees: %+v", launched)
	}
	reqs := e.runs.startedRequests()
	if prompt := reqs[len(reqs)-1].Turns[0].Text(); !strings.Contains(prompt, "Start with the API.") {
		t.Fatalf("queued message not passed to the agent:\n%s", prompt)
	}
}

func TestQueuedTaskCanBeCancelled(t *testing.T) {
	e := setup(t)
	e.sandboxes.set(func(f *fakeSandboxes) { f.limit = 1 })
	first, second := e.createTask(t), e.createTask(t)
	e.start(t, first.ID, StartInput{})
	e.waitTask(t, first.ID, hasRun)
	e.start(t, second.ID, StartInput{})

	cancelled, err := e.svc.Cancel(context.Background(), owner, 1, second.ID)
	if err != nil || cancelled.Status != StatusCancelled {
		t.Fatalf("cancel queued task: %+v %v", cancelled, err)
	}
	if len(e.runs.snapshot().cancelled) != 0 {
		t.Fatal("cancelling a queued task must not touch other runs")
	}
}

func TestStartUsesTheAgentsDefaultsUnlessAModelIsPicked(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.defaults[domain.DefaultAgentID] = domain.AgentDefaults{AgentID: domain.DefaultAgentID, Model: "admin-model", Effort: "low"}

	defaulted, err := e.svc.Create(ctx, owner, 1, CreateInput{Title: "defaulted", Start: true})
	if err != nil {
		t.Fatalf("create with the default: %v", err)
	}
	if defaulted.Model != "admin-model" || defaulted.Effort != "low" {
		t.Fatalf("expected the admin's model and effort, got %q/%q", defaulted.Model, defaulted.Effort)
	}
	picked, err := e.svc.Create(ctx, owner, 1, CreateInput{Title: "picked", Start: true, AgentSettings: AgentSettings{Model: "picked-model"}})
	if err != nil {
		t.Fatalf("create with a picked model: %v", err)
	}
	if picked.Model != "picked-model" || picked.Effort != "" {
		t.Fatalf("expected the picked model alone, got %q/%q", picked.Model, picked.Effort)
	}

	later, err := e.svc.Create(ctx, owner, 1, CreateInput{Title: "later"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if later.Model != "" {
		t.Fatalf("a task not started should keep no model, got %q", later.Model)
	}
	started, err := e.svc.Start(ctx, owner, 1, later.ID, StartInput{AgentSettings: AgentSettings{Effort: "high"}})
	if err != nil {
		t.Fatalf("start with the default: %v", err)
	}
	if started.Model != "admin-model" || started.Effort != "high" {
		t.Fatalf("expected the admin's model with the picked effort, got %q/%q", started.Model, started.Effort)
	}

	// A custom agent has no defaults, so it still needs a model.
	custom, err := e.svc.Create(ctx, owner, 1, CreateInput{Title: "custom", AgentSettings: AgentSettings{AgentID: "7"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := e.svc.Start(ctx, owner, 1, custom.ID, StartInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for a custom agent without a model, got %v", err)
	}
}

func TestStartRejections(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	noModel, err := e.svc.Create(ctx, owner, 1, CreateInput{Title: "no model"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := e.svc.Start(ctx, owner, 1, noModel.ID, StartInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput without a model, got %v", err)
	}

	task := e.createTask(t)
	if _, err := e.svc.Start(ctx, stranger, 1, task.ID, StartInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for another user, got %v", err)
	}
	if _, err := e.svc.Start(ctx, owner, 2, task.ID, StartInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for another project, got %v", err)
	}

	e.sandboxes.set(func(f *fakeSandboxes) { f.project.Status = project.StatusError })
	if _, err := e.svc.Start(ctx, owner, 1, task.ID, StartInput{}); !errors.Is(err, project.ErrNotReady) {
		t.Fatalf("expected ErrNotReady, got %v", err)
	}
	e.sandboxes.set(func(f *fakeSandboxes) { f.project.Status = project.StatusReady })

	e.start(t, task.ID, StartInput{})
	if _, err := e.svc.Start(ctx, owner, 1, task.ID, StartInput{}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState while in progress, got %v", err)
	}
	e.waitTask(t, task.ID, hasRun)
	if _, err := e.svc.Cancel(ctx, owner, 1, task.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := e.svc.Start(ctx, owner, 1, task.ID, StartInput{}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState once cancelled, got %v", err)
	}
	if _, err := e.svc.Create(ctx, owner, 1, CreateInput{Title: "  "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for a blank title, got %v", err)
	}
	if _, err := e.svc.Create(ctx, stranger, 1, CreateInput{Title: "x"}); !errors.Is(err, project.ErrForbidden) {
		t.Fatalf("expected ErrForbidden creating in another user's project, got %v", err)
	}
}

func TestCancelStopsRunAndRemovesSandbox(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)

	cancelled, err := e.svc.Cancel(context.Background(), owner, 1, task.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled || cancelled.WorkspaceID != 0 {
		t.Fatalf("unexpected cancelled task: %+v", cancelled)
	}
	if got := e.runs.snapshot().cancelled; len(got) != 1 || got[0] != running.RunID {
		t.Fatalf("expected run %d cancelled, got %v", running.RunID, got)
	}
	if got := e.sandboxes.snapshot().deleted; len(got) != 1 || got[0] != running.WorkspaceID {
		t.Fatalf("expected sandbox %d deleted, got %v", running.WorkspaceID, got)
	}

	// The runner's completion notice for the cancelled run must not reopen it.
	time.Sleep(50 * time.Millisecond)
	if got, _ := e.repo.GetByID(context.Background(), task.ID); got.Status != StatusCancelled {
		t.Fatalf("task status changed after cancel: %s", got.Status)
	}
}

func TestRunCancelledOutsideTheTaskFailsIt(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)

	// For example from the IDE's agent runs panel.
	e.runs.finish(running.RunID, domain.AgentRunCancelled, "agent run cancelled")
	failed := e.waitTask(t, task.ID, status(StatusFailed))
	if failed.Error != "the agent run was cancelled" || failed.WorkspaceID == 0 {
		t.Fatalf("unexpected task: %+v", failed)
	}
}

func TestDeleteRemovesTaskAndSandbox(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)

	if err := e.svc.Delete(context.Background(), owner, 1, task.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := e.repo.GetByID(context.Background(), task.ID); got != nil {
		t.Fatalf("task still exists: %+v", got)
	}
	if len(e.runs.snapshot().cancelled) != 1 || len(e.sandboxes.snapshot().deleted) != 1 || e.sandboxes.snapshot().deleted[0] != running.WorkspaceID {
		t.Fatalf("expected run cancelled and sandbox removed: cancelled=%v deleted=%v", e.runs.snapshot().cancelled, e.sandboxes.snapshot().deleted)
	}
}

func TestReconcileSettlesInterruptedTasks(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	add := func(runID int64) int64 {
		task := &Task{ProjectID: 1, UserID: owner, Title: "t", Status: StatusInProgress, Branch: "task/x", WorkspaceID: 101, RunID: runID, Model: "m"}
		e.repo.Create(ctx, task)
		return task.ID
	}
	e.sandboxes.byID[101] = &workspace.Workspace{ID: 101, UserID: owner, ProjectID: 1, Branch: "task/x", Status: workspace.StatusRunning}
	e.runs.runs[7] = &domain.AgentRun{ID: 7, UserID: owner, Status: domain.AgentRunCompleted}
	e.runs.runs[8] = &domain.AgentRun{ID: 8, UserID: owner, Status: domain.AgentRunFailed, Error: domain.AgentRunInterruptedError}
	e.runs.runs[10] = &domain.AgentRun{ID: 10, UserID: owner, Status: domain.AgentRunFailed, Error: "provider overloaded"}

	neverStarted := add(0)
	completed := add(7)
	interrupted := add(8)
	missing := add(9)
	broken := add(10)
	// The interrupted run's transcript was saved by the agent runner.
	it, _ := e.repo.GetByID(ctx, interrupted)
	it.ConversationID = 77
	e.repo.Update(ctx, it)
	e.convs.save(77, domain.NewTextTurn(domain.RoleUser, "implement"), domain.NewTextTurn(domain.RoleAssistant, "halfway"))

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// A task whose run never started is queued again and relaunched.
	if got := e.waitTask(t, neverStarted, hasRun); got.Status != StatusInProgress {
		t.Fatalf("expected the interrupted launch relaunched, got %+v", got)
	}
	e.waitTask(t, completed, status(StatusInReview))
	// A task whose run the restart interrupted resumes its conversation.
	resumed := e.waitTask(t, interrupted, func(t *Task) bool { return t.RunID != 8 && t.RunID != 0 })
	if resumed.Status != StatusInProgress || resumed.ConversationID != 77 {
		t.Fatalf("expected the interrupted task resumed in its conversation, got %+v", resumed)
	}
	var resumeTurns []domain.Turn
	for _, req := range e.runs.startedRequests() {
		if req.ConversationID == 77 {
			resumeTurns = req.Turns
		}
	}
	if len(resumeTurns) != 3 || !strings.Contains(lastText(resumeTurns), "the server restarted while the agent was working") {
		t.Fatalf("unexpected resume turns: %+v", resumeTurns)
	}
	e.waitTask(t, missing, status(StatusFailed))
	if got := e.waitTask(t, broken, status(StatusFailed)); got.Error != "provider overloaded" {
		t.Fatalf("a real failure must still fail the task, got %q", got.Error)
	}
}

func TestShutdownLeavesInterruptedLaunchesForReconcile(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.svc.rootCancel()
	e.sandboxes.set(func(f *fakeSandboxes) { f.createErr = context.Canceled })
	e.start(t, task.ID, StartInput{})
	time.Sleep(50 * time.Millisecond)
	if got, _ := e.repo.GetByID(context.Background(), task.ID); got.Status != StatusQueued {
		t.Fatalf("a launch cut short by shutdown should stay queued, got %s (%s)", got.Status, got.Error)
	}
}

func TestAppendUserTextJoinsTrailingUserTurn(t *testing.T) {
	toolResult := domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{{Kind: domain.PartToolResult, ToolResult: &domain.ToolResultPart{ToolCallID: "1"}}}}
	turns := appendUserText([]domain.Turn{domain.NewTextTurn(domain.RoleAssistant, "a"), toolResult}, "continue")
	if len(turns) != 2 || len(turns[1].Parts) != 2 || turns[1].Parts[1].Text != "continue" {
		t.Fatalf("expected the text joined to the tool result turn: %+v", turns)
	}
	turns = appendUserText([]domain.Turn{domain.NewTextTurn(domain.RoleAssistant, "a")}, "continue")
	if len(turns) != 2 || turns[1].Role != domain.RoleUser {
		t.Fatalf("expected a new user turn: %+v", turns)
	}
}

func TestHandler(t *testing.T) {
	e := setup(t)
	h := NewHandler(e.svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{id}/tasks", h.HandleCreate)
	mux.HandleFunc("GET /api/projects/{id}/tasks", h.HandleList)
	mux.HandleFunc("POST /api/projects/{id}/tasks/{taskId}/start", h.HandleStart)
	mux.HandleFunc("GET /api/projects/{id}/tasks/{taskId}", h.HandleGet)
	mux.HandleFunc("POST /api/projects/{id}/tasks/handoff", h.HandleHandoff)

	do := func(userID int64, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: userID}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := do(owner, http.MethodPost, "/api/projects/1/tasks", `{"title":"Add a cart","model":"claude-opus-5-5","start":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Task map[string]any `json:"task"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Task["status"] != "queued" || created.Task["branch"] != "task/1" {
		t.Fatalf("unexpected task: %v", created.Task)
	}

	tests := []struct {
		name   string
		userID int64
		method string
		path   string
		body   string
		want   int
	}{
		{"list", owner, http.MethodGet, "/api/projects/1/tasks", "", http.StatusOK},
		{"get", owner, http.MethodGet, "/api/projects/1/tasks/1", "", http.StatusOK},
		{"already running", owner, http.MethodPost, "/api/projects/1/tasks/1/start", "", http.StatusConflict},
		{"other user's task", stranger, http.MethodGet, "/api/projects/1/tasks/1", "", http.StatusNotFound},
		{"other user's project", stranger, http.MethodGet, "/api/projects/1/tasks", "", http.StatusForbidden},
		{"unknown project", owner, http.MethodGet, "/api/projects/9/tasks", "", http.StatusNotFound},
		{"blank title", owner, http.MethodPost, "/api/projects/1/tasks", `{"title":""}`, http.StatusBadRequest},
		{"bad id", owner, http.MethodGet, "/api/projects/1/tasks/x", "", http.StatusBadRequest},
		{"unpublished base", owner, http.MethodPost, "/api/projects/1/tasks", `{"title":"x","baseBranch":"nope"}`, http.StatusBadRequest},
		{"handoff without a project workspace", owner, http.MethodPost, "/api/projects/1/tasks/handoff", `{"conversationId":1,"title":"x"}`, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := do(tt.userID, tt.method, tt.path, tt.body); rec.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, rec.Code, rec.Body)
			}
		})
	}

	// Once the project workspace exists, a handoff of one of its
	// conversations starts a task; anything else is refused.
	e.sandboxes.set(func(f *fakeSandboxes) {
		f.projectWS = &workspace.Workspace{ID: 50, UserID: owner, ProjectID: 1, Kind: workspace.KindInteractive}
	})
	e.convs.mu.Lock()
	e.convs.convs[7] = domain.Conversation{ID: 7, UserID: owner, WorkspaceID: 50, Title: "Cart"}
	e.convs.convs[8] = domain.Conversation{ID: 8, UserID: owner, WorkspaceID: 101, Title: "Sandbox"}
	e.convs.turns[7] = []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Build the cart.")}
	e.convs.mu.Unlock()
	for _, tt := range []struct {
		name string
		body string
		want int
	}{
		{"handoff", `{"conversationId":7,"title":"Cart","model":"claude-opus-5-5"}`, http.StatusCreated},
		{"handoff without a model", `{"conversationId":7,"title":"Cart"}`, http.StatusBadRequest},
		{"handoff of another workspace's conversation", `{"conversationId":8,"title":"x","model":"m"}`, http.StatusBadRequest},
		{"handoff from an unpublished base", `{"conversationId":7,"title":"x","model":"m","baseBranch":"nope"}`, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(owner, http.MethodPost, "/api/projects/1/tasks/handoff", tt.body)
			if rec.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, rec.Code, rec.Body)
			}
			if tt.want == http.StatusCreated {
				var res struct {
					Task map[string]any `json:"task"`
				}
				json.Unmarshal(rec.Body.Bytes(), &res)
				if res.Task["status"] != "queued" || res.Task["handedOff"] != true {
					t.Fatalf("unexpected task: %v", res.Task)
				}
			}
		})
	}
}

func TestRepositoryAgainstMigratedSchema(t *testing.T) {
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
	mustExec := func(query string, args ...any) int64 {
		t.Helper()
		res, err := conn.Exec(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	userID := mustExec(`INSERT INTO users (username, email, password) VALUES ('u', 'u@example.com', 'x')`)
	projectID := mustExec(`INSERT INTO projects (user_id, name, status) VALUES (?, 'p', 'ready')`, userID)
	wsID := mustExec(`INSERT INTO workspaces (user_id, name, status, project_id, kind, branch) VALUES (?, 'sb', 'running', ?, 'task', 'task/1')`, userID, projectID)
	convID := mustExec(`INSERT INTO ai_conversations (user_id, workspace_id) VALUES (?, ?)`, userID, wsID)
	runID := mustExec(`INSERT INTO ai_agent_runs (user_id, workspace_id, conversation_id, model, status) VALUES (?, ?, ?, 'm', 'running')`, userID, wsID, convID)

	repo := NewRepository(conn)
	task := &Task{ProjectID: projectID, UserID: userID, Title: "t", Body: "b", Status: StatusTodo}
	if err := repo.Create(ctx, task); err != nil {
		t.Fatalf("create: %v", err)
	}
	task.Branch, task.Status = BranchName(task.ID), StatusInProgress
	task.WorkspaceID, task.ConversationID, task.RunID = wsID, convID, runID
	task.AgentID, task.Model, task.Effort, task.HeadSHA, task.Error = "default", "m", "high", "abc", "e"
	if err := repo.Update(ctx, task); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.GetByRunID(ctx, runID)
	if err != nil || got == nil {
		t.Fatalf("get by run: %v %v", got, err)
	}
	if got.Branch != "task/1" || got.WorkspaceID != wsID || got.ConversationID != convID || got.Effort != "high" || got.HeadSHA != "abc" || got.Error != "e" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if list, _ := repo.ListByStatus(ctx, StatusInProgress); len(list) != 1 {
		t.Fatalf("expected one in-progress task, got %d", len(list))
	}
	if missing, err := repo.GetByID(ctx, 999); missing != nil || err != nil {
		t.Fatalf("expected nil for a missing task, got %v %v", missing, err)
	}
	if _, err := conn.Exec(`UPDATE tasks SET status = 'bogus' WHERE id = ?`, task.ID); err == nil {
		t.Fatal("expected the status CHECK constraint to reject an unknown status")
	}

	// Deleting the sandbox clears the links but keeps the task.
	mustExec(`DELETE FROM workspaces WHERE id = ?`, wsID)
	got, _ = repo.GetByID(ctx, task.ID)
	if got == nil || got.WorkspaceID != 0 || got.ConversationID != 0 || got.RunID != 0 {
		t.Fatalf("expected links cleared after sandbox delete: %+v", got)
	}
	// Deleting the project removes its tasks.
	mustExec(`DELETE FROM projects WHERE id = ?`, projectID)
	if got, _ := repo.GetByID(ctx, task.ID); got != nil {
		t.Fatalf("expected task removed with its project: %+v", got)
	}
}

func TestRunStartsOnceSetupFinishesAndIsToldHowItWent(t *testing.T) {
	e := setup(t)
	gate := make(chan struct{})
	code := 1
	e.sandboxes.set(func(f *fakeSandboxes) {
		f.setupGate = gate
		f.setup = &project.Setup{Script: "npm ci", Status: project.SetupFailed, ExitCode: &code, Output: "npm ERR! lockfile out of date\n"}
	})
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})

	waiting := waitSandbox(t, e, func(c sandboxCalls) bool { return len(c.awaited) == 1 })
	queued, _ := e.repo.GetByID(context.Background(), task.ID)
	if queued.Status != StatusQueued || queued.WorkspaceID != waiting.awaited[0] || queued.RunID != 0 {
		t.Fatalf("expected the task queued on its sandbox while setup runs, got %+v", queued)
	}
	if len(e.runs.startedRequests()) != 0 {
		t.Fatal("the agent started before setup finished")
	}
	// A dispatch meanwhile must not launch the task a second time.
	e.svc.dispatch(context.Background())
	time.Sleep(20 * time.Millisecond)
	if c := e.sandboxes.snapshot(); len(c.awaited) != 1 || len(c.created) != 1 {
		t.Fatalf("the task was launched again while waiting: %+v", c)
	}

	close(gate)
	running := e.waitTask(t, task.ID, hasRun)
	if running.Status != StatusInProgress {
		t.Fatalf("unexpected running task: %+v", running)
	}
	prompt := e.runs.startedRequests()[0].Turns[0].Text()
	for _, want := range []string{"## Setup", "npm ci", "it exited with status 1", "npm ERR! lockfile out of date", "## How to work"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestCancelWhileSetupRunsDoesNotWaitForIt(t *testing.T) {
	e := setup(t)
	gate := make(chan struct{})
	e.sandboxes.set(func(f *fakeSandboxes) { f.setupGate = gate })
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	waiting := waitSandbox(t, e, func(c sandboxCalls) bool { return len(c.awaited) == 1 })

	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Cancel(context.Background(), owner, 1, task.ID)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel waited for the setup script")
	}
	if c := e.sandboxes.snapshot(); len(c.deleted) != 1 || c.deleted[0] != waiting.awaited[0] {
		t.Fatalf("expected the sandbox removed, got %v", c.deleted)
	}

	close(gate)
	time.Sleep(20 * time.Millisecond)
	if len(e.runs.startedRequests()) != 0 {
		t.Fatal("an agent started for a cancelled task")
	}
	if got, _ := e.repo.GetByID(context.Background(), task.ID); got.Status != StatusCancelled {
		t.Fatalf("expected the task to stay cancelled, got %+v", got)
	}
}

func TestPromptWithoutSetupHasNoSetupSection(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	e.waitTask(t, task.ID, hasRun)
	if prompt := e.runs.startedRequests()[0].Turns[0].Text(); strings.Contains(prompt, "## Setup") {
		t.Fatalf("unexpected setup section:\n%s", prompt)
	}
}

// waitSandbox polls the fake's calls until cond holds.
func waitSandbox(t *testing.T, e *env, cond func(sandboxCalls) bool) sandboxCalls {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c := e.sandboxes.snapshot()
		if cond(c) {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("sandbox calls never reached the expected state: %+v", c)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSandboxStoppedDuringSetupIsRestartedForTheRun(t *testing.T) {
	e := setup(t)
	gate := make(chan struct{})
	e.sandboxes.set(func(f *fakeSandboxes) { f.setupGate = gate })
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	waiting := waitSandbox(t, e, func(c sandboxCalls) bool { return len(c.awaited) == 1 })

	if _, err := (workspaceLifecycle{e.sandboxes}).Stop(context.Background(), owner, waiting.awaited[0]); err != nil {
		t.Fatalf("stop: %v", err)
	}
	close(gate)
	running := e.waitTask(t, task.ID, hasRun)
	if running.Status != StatusInProgress {
		t.Fatalf("unexpected task: %+v", running)
	}
	if got := e.sandboxes.snapshot().started; len(got) != 1 || got[0] != waiting.awaited[0] {
		t.Fatalf("expected the stopped sandbox restarted, got %v", got)
	}
}

func TestNewSandboxWaitsForTheEnvironmentAndIsToldItFailed(t *testing.T) {
	e := setup(t)
	gate := make(chan struct{})
	e.sandboxes.set(func(f *fakeSandboxes) {
		f.environmentGate = gate
		f.environment = &project.Environment{Build: &project.Build{Status: project.BuildFailed, Error: "returned a non-zero code: 100"}}
	})
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	time.Sleep(30 * time.Millisecond)
	if c := e.sandboxes.snapshot(); len(c.created) != 0 {
		t.Fatalf("a sandbox was made before the environment build ended: %+v", c.created)
	}
	// The task is not locked meanwhile.
	if _, err := e.svc.Update(context.Background(), owner, 1, task.ID, UpdateInput{Title: "Add a cart", Body: "Users need a cart."}); err != nil {
		t.Fatalf("update while waiting: %v", err)
	}

	close(gate)
	e.waitTask(t, task.ID, hasRun)
	prompt := e.runs.startedRequests()[0].Turns[0].Text()
	for _, want := range []string{"## Environment", "failed to build", "non-zero code: 100", "devpad's own image"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestTaskWithASandboxDoesNotWaitForTheEnvironment(t *testing.T) {
	e := setup(t)
	task := e.createTask(t)
	e.start(t, task.ID, StartInput{})
	running := e.waitTask(t, task.ID, hasRun)
	e.runs.finish(running.RunID, domain.AgentRunCompleted, "")
	e.waitTask(t, task.ID, status(StatusInReview))

	// A build in progress only holds back new sandboxes.
	gate := make(chan struct{})
	defer close(gate)
	e.sandboxes.set(func(f *fakeSandboxes) { f.environmentGate = gate })
	if _, err := e.svc.RequestChanges(context.Background(), owner, 1, task.ID, "Handle an empty cart.", true); err != nil {
		t.Fatalf("request changes: %v", err)
	}
	e.waitTask(t, task.ID, func(t *Task) bool { return t.RunID != 0 && t.RunID != running.RunID })
}
