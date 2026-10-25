package task

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Service manages tasks and orchestrates the agents that implement them.
type Service interface {
	List(ctx context.Context, userID, projectID int64) ([]*Task, error)
	Create(ctx context.Context, userID, projectID int64, in CreateInput) (*Task, error)
	Get(ctx context.Context, userID, projectID, taskID int64) (*Task, error)
	Update(ctx context.Context, userID, projectID, taskID int64, in UpdateInput) (*Task, error)
	Delete(ctx context.Context, userID, projectID, taskID int64) error
	Start(ctx context.Context, userID, projectID, taskID int64, in StartInput) (*Task, error)
	Cancel(ctx context.Context, userID, projectID, taskID int64) (*Task, error)
	// RequestChanges hands review feedback to the task's agent, starting it
	// now or leaving it for the next Start.
	RequestChanges(ctx context.Context, userID, projectID, taskID int64, feedback string, start bool) (*Task, error)
	// Merged closes a task whose branch has been merged and removes its sandbox.
	Merged(ctx context.Context, userID, projectID, taskID int64) (*Task, error)
	// SetReviewHook registers who is told when a task is ready for review.
	SetReviewHook(h ReviewHook)

	// RunFinished is the agent runner's completion hook.
	RunFinished(run domain.AgentRun)
	// Reconcile settles tasks left in progress by a previous server process
	// and resumes the queue.
	Reconcile(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

// ReviewHook is told each time a task's branch is published for review,
// with the agent's closing summary.
type ReviewHook interface {
	TaskReadyForReview(ctx context.Context, t *Task, summary string)
}

// Projects is the project service surface tasks need.
type Projects interface {
	Get(ctx context.Context, userID, projectID int64) (*project.Project, error)
	ListSandboxes(ctx context.Context, userID, projectID int64) ([]*workspace.Workspace, error)
	CreateSandbox(ctx context.Context, userID, projectID int64, in project.SandboxInput) (*workspace.Workspace, error)
	StartSandbox(ctx context.Context, userID, projectID, workspaceID int64) (*workspace.Workspace, error)
	PublishSandbox(ctx context.Context, userID, projectID, workspaceID int64) (*agent.RepoBranch, error)
	SyncSandboxBase(ctx context.Context, userID, projectID, workspaceID int64) (*agent.RepoBranch, error)
	Compare(ctx context.Context, userID, projectID int64, base, head string) (*agent.RepoComparison, error)
}

// Workspaces is the workspace lifecycle surface tasks need.
type Workspaces interface {
	Get(ctx context.Context, userID, workspaceID int64) (*workspace.Workspace, error)
	Stop(ctx context.Context, userID, workspaceID int64) (*workspace.Workspace, error)
	Delete(ctx context.Context, userID, workspaceID int64) error
}

// Runs is the agent runner surface tasks need.
type Runs interface {
	StartRun(ctx context.Context, req app.StartAgentRunRequest) (*domain.AgentRun, error)
	GetRun(ctx context.Context, userID, runID int64) (*domain.AgentRun, error)
	CancelRun(ctx context.Context, userID, runID int64) error
}

// Conversations is the conversation surface tasks need to keep one agent
// transcript per task.
type Conversations interface {
	CreateConversation(ctx context.Context, userID, workspaceID int64, model string) (*domain.Conversation, error)
	GetTurns(ctx context.Context, conversationID, userID int64) ([]domain.Turn, error)
}

const (
	// cancelWait bounds how long Cancel and Delete wait for a cancelled run
	// to stop before removing its sandbox.
	cancelWait = 15 * time.Second

	// dispatchInterval is how often queued tasks retry for a sandbox slot,
	// in case one was freed outside the task service.
	dispatchInterval = 30 * time.Second
)

type service struct {
	repo          Repository
	projects      Projects
	workspaces    Workspaces
	runs          Runs
	conversations Conversations

	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         sync.WaitGroup

	mu         sync.Mutex
	closed     bool
	locks      map[int64]*sync.Mutex
	reviewHook ReviewHook
}

// NewService creates a task Service. Background work stops on Shutdown.
func NewService(repo Repository, projects Projects, workspaces Workspaces, runs Runs, conversations Conversations) Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &service{
		repo:          repo,
		projects:      projects,
		workspaces:    workspaces,
		runs:          runs,
		conversations: conversations,
		rootCtx:       ctx,
		rootCancel:    cancel,
		locks:         make(map[int64]*sync.Mutex),
	}
	s.spawn(func(ctx context.Context) {
		ticker := time.NewTicker(dispatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.dispatch(ctx)
			}
		}
	})
	return s
}

func (s *service) SetReviewHook(h ReviewHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviewHook = h
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

// lock serializes everything that changes one task: requests, the launch
// that follows Start, and run completion.
func (s *service) lock(taskID int64) func() {
	s.mu.Lock()
	l, ok := s.locks[taskID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[taskID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// spawn runs fn in the background unless the service is shutting down.
func (s *service) spawn(fn func(ctx context.Context)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn(s.rootCtx)
	}()
	return true
}

func (s *service) get(ctx context.Context, userID, projectID, taskID int64) (*Task, error) {
	t, err := s.repo.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if t == nil || t.UserID != userID || t.ProjectID != projectID {
		return nil, ErrNotFound
	}
	return t, nil
}

func (s *service) List(ctx context.Context, userID, projectID int64) ([]*Task, error) {
	if _, err := s.projects.Get(ctx, userID, projectID); err != nil {
		return nil, err
	}
	return s.repo.ListByProjectID(ctx, projectID)
}

func (s *service) Create(ctx context.Context, userID, projectID int64, in CreateInput) (*Task, error) {
	if _, err := s.projects.Get(ctx, userID, projectID); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, invalid("title is required")
	}
	t := &Task{
		ProjectID: projectID,
		UserID:    userID,
		Title:     title,
		Body:      in.Body,
		Status:    StatusTodo,
		AgentID:   in.AgentID,
		Model:     in.Model,
		Effort:    in.Effort,
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, err
	}
	t.Branch = BranchName(t.ID)
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
	if in.Start {
		return s.Start(ctx, userID, projectID, t.ID, StartInput{})
	}
	return t, nil
}

func (s *service) Get(ctx context.Context, userID, projectID, taskID int64) (*Task, error) {
	return s.get(ctx, userID, projectID, taskID)
}

func (s *service) Update(ctx context.Context, userID, projectID, taskID int64, in UpdateInput) (*Task, error) {
	unlock := s.lock(taskID)
	defer unlock()
	t, err := s.get(ctx, userID, projectID, taskID)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, invalid("title is required")
	}
	t.Title, t.Body = title, in.Body
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// Start queues the task for an agent. It returns at once; the task launches
// in the background as soon as its project has a free sandbox slot, and
// failures are recorded on the task.
func (s *service) Start(ctx context.Context, userID, projectID, taskID int64, in StartInput) (*Task, error) {
	unlock := s.lock(taskID)
	defer unlock()
	t, err := s.get(ctx, userID, projectID, taskID)
	if err != nil {
		return nil, err
	}
	return s.start(ctx, t, in)
}

// start queues a task the caller has locked and loaded.
func (s *service) start(ctx context.Context, t *Task, in StartInput) (*Task, error) {
	if !t.Status.Startable() {
		return nil, fmt.Errorf("%w: task is %s", ErrInvalidState, t.Status)
	}
	p, err := s.projects.Get(ctx, t.UserID, t.ProjectID)
	if err != nil {
		return nil, err
	}
	if p.Status != project.StatusReady {
		return nil, project.ErrNotReady
	}
	if in.AgentID != "" {
		t.AgentID = in.AgentID
	}
	if in.Model != "" {
		t.Model = in.Model
	}
	if in.Effort != "" {
		t.Effort = in.Effort
	}
	if t.Model == "" {
		return nil, invalid("model is required")
	}

	// The previous round's error stays until the new run starts, so the
	// agent can be told why it is being restarted. Feedback already waiting
	// from RequestChanges is kept unless a new message replaces it.
	if msg := strings.TrimSpace(in.Message); msg != "" {
		t.PendingMessage = msg
	}
	t.Status, t.RunID = StatusQueued, 0
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
	s.kick()
	return t, nil
}

func (s *service) RequestChanges(ctx context.Context, userID, projectID, taskID int64, feedback string, start bool) (*Task, error) {
	unlock := s.lock(taskID)
	defer unlock()
	t, err := s.get(ctx, userID, projectID, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status != StatusInReview && t.Status != StatusChangesRequested {
		return nil, fmt.Errorf("%w: task is %s", ErrInvalidState, t.Status)
	}
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return nil, invalid("feedback is required")
	}
	if start {
		return s.start(ctx, t, StartInput{Message: feedback})
	}
	t.Status, t.PendingMessage = StatusChangesRequested, feedback
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *service) Merged(ctx context.Context, userID, projectID, taskID int64) (*Task, error) {
	unlock := s.lock(taskID)
	defer unlock()
	t, err := s.get(ctx, userID, projectID, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status.Closed() {
		return nil, fmt.Errorf("%w: task is %s", ErrInvalidState, t.Status)
	}
	if err := s.teardown(ctx, t); err != nil {
		return nil, err
	}
	t.Status, t.Error, t.PendingMessage = StatusMerged, "", ""
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
	s.kick()
	return t, nil
}

// kick dispatches queued tasks in the background.
func (s *service) kick() {
	s.spawn(s.dispatch)
}

// dispatch tries to launch every queued task. Launches run concurrently so
// sandboxes are created in parallel; those that find no free slot stay queued
// until the next kick.
func (s *service) dispatch(ctx context.Context) {
	tasks, err := s.repo.ListByStatus(ctx, StatusQueued)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("listing queued tasks: %v", err)
		}
		return
	}
	for _, t := range tasks {
		id := t.ID
		s.spawn(func(ctx context.Context) { s.launch(ctx, id) })
	}
}

// launch starts a queued task's sandbox and agent run.
func (s *service) launch(ctx context.Context, taskID int64) {
	unlock := s.lock(taskID)
	defer unlock()
	t, err := s.repo.GetByID(ctx, taskID)
	if err != nil || t == nil || t.Status != StatusQueued {
		return
	}
	if err := s.startRun(ctx, t); err != nil {
		// The task stays queued for the next dispatch when no sandbox slot is
		// free or its branch is being published from the project workspace.
		if errors.Is(err, project.ErrSandboxLimit) || errors.Is(err, project.ErrBranchPublishing) {
			return
		}
		s.failUnlessShuttingDown(ctx, t, err.Error())
	}
}

func (s *service) startRun(ctx context.Context, t *Task) error {
	p, err := s.projects.Get(ctx, t.UserID, t.ProjectID)
	if err != nil {
		return err
	}
	ws, err := s.ensureSandbox(ctx, t)
	if err != nil {
		if errors.Is(err, project.ErrSandboxLimit) {
			return err
		}
		return fmt.Errorf("starting sandbox: %w", err)
	}
	// Give the agent the latest default branch, as devpad/<branch>, so it can
	// merge in work that landed since its sandbox was seeded.
	if _, err := s.projects.SyncSandboxBase(ctx, t.UserID, t.ProjectID, ws.ID); err != nil {
		log.Printf("task %d: refreshing devpad/%s in sandbox %d: %v", t.ID, p.DefaultBranch, ws.ID, err)
	}
	message, previousError := t.PendingMessage, t.Error
	t.Status, t.WorkspaceID, t.PendingMessage, t.Error = StatusInProgress, ws.ID, "", ""
	turns, err := s.buildTurns(ctx, t, p, message, previousError)
	if err != nil {
		return err
	}
	if err := s.repo.Update(ctx, t); err != nil {
		return err
	}

	var thinking *domain.ThinkingConfig
	if t.Effort != "" {
		thinking = &domain.ThinkingConfig{Effort: t.Effort}
	}
	run, err := s.runs.StartRun(ctx, app.StartAgentRunRequest{
		UserID:         t.UserID,
		WorkspaceID:    ws.ID,
		ConversationID: t.ConversationID,
		AgentID:        t.AgentID,
		Model:          t.Model,
		Turns:          turns,
		Thinking:       thinking,
	})
	if err != nil {
		return fmt.Errorf("starting agent: %w", err)
	}
	t.RunID = run.ID
	if err := s.repo.Update(ctx, t); err != nil {
		return err
	}

	// A run that ended before its ID was saved was ignored by RunFinished, so
	// settle it here. The task lock keeps the two paths from both running.
	if run, err := s.runs.GetRun(ctx, t.UserID, run.ID); err == nil && domain.AgentRunStatusTerminal(run.Status) {
		s.settle(ctx, t, run)
	}
	return nil
}

// ensureSandbox returns the task's sandbox, restarting it if stopped, or
// creates one. A new sandbox resumes the task branch if it was published.
func (s *service) ensureSandbox(ctx context.Context, t *Task) (*workspace.Workspace, error) {
	if t.WorkspaceID != 0 {
		ws, err := s.workspaces.Get(ctx, t.UserID, t.WorkspaceID)
		switch {
		case err == nil:
			return s.running(ctx, t, ws)
		case !errors.Is(err, workspace.ErrNotFound):
			return nil, err
		}
		// The sandbox was deleted, and its transcript with it.
		t.WorkspaceID, t.ConversationID = 0, 0
	}
	ws, err := s.projects.CreateSandbox(ctx, t.UserID, t.ProjectID, project.SandboxInput{
		Name:   fmt.Sprintf("Task #%d: %s", t.ID, t.Title),
		Kind:   string(workspace.KindTask),
		Branch: t.Branch,
	})
	if errors.Is(err, project.ErrBranchInUse) {
		// A sandbox for the branch exists but was never recorded on the task,
		// for example after a crash mid-launch. Take it over.
		sandboxes, listErr := s.projects.ListSandboxes(ctx, t.UserID, t.ProjectID)
		if listErr != nil {
			return nil, listErr
		}
		for _, ws := range sandboxes {
			if ws.Branch == t.Branch {
				return s.running(ctx, t, ws)
			}
		}
	}
	return ws, err
}

func (s *service) running(ctx context.Context, t *Task, ws *workspace.Workspace) (*workspace.Workspace, error) {
	if ws.Status == workspace.StatusStopped {
		return s.projects.StartSandbox(ctx, t.UserID, t.ProjectID, ws.ID)
	}
	return ws, nil
}

// buildTurns continues the task's conversation when it has one, so the agent
// keeps its context across retries and review rounds. Otherwise it starts a
// new conversation with the implement prompt.
func (s *service) buildTurns(ctx context.Context, t *Task, p *project.Project, message, previousError string) ([]domain.Turn, error) {
	if t.ConversationID != 0 {
		turns, err := s.conversations.GetTurns(ctx, t.ConversationID, t.UserID)
		if err == nil && len(turns) > 0 {
			return appendUserText(turns, continuePrompt(t, message, previousError)), nil
		}
		if err != nil {
			log.Printf("task %d: loading conversation %d, starting a new one: %v", t.ID, t.ConversationID, err)
		}
	}
	conv, err := s.conversations.CreateConversation(ctx, t.UserID, t.WorkspaceID, t.Model)
	if err != nil {
		return nil, err
	}
	t.ConversationID = conv.ID
	prompt := implementPrompt(p, t)
	if message != "" {
		prompt += "\n\n## Additional instructions\n\n" + message
	}
	return []domain.Turn{domain.NewTextTurn(domain.RoleUser, prompt)}, nil
}

// appendUserText adds a user message, joining it to a trailing user turn
// (such as one carrying tool results) instead of sending two in a row.
func appendUserText(turns []domain.Turn, text string) []domain.Turn {
	part := domain.Part{Kind: domain.PartText, Text: text}
	if last := len(turns) - 1; turns[last].Role == domain.RoleUser {
		turns[last].Parts = append(turns[last].Parts, part)
		return turns
	}
	return append(turns, domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{part}})
}

func implementPrompt(p *project.Project, t *Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are implementing task #%d in the project %q.\n\n", t.ID, p.Name)
	fmt.Fprintf(&b, "## %s\n\n", t.Title)
	if body := strings.TrimSpace(t.Body); body != "" {
		b.WriteString(body + "\n\n")
	}
	fmt.Fprintf(&b, `## How to work

- /workspace is a git checkout of the project on the branch %[1]s, which started from %[2]s. If %[1]s already has commits that %[2]s lacks, they are earlier work on this task: build on them.
- The latest %[2]s is available as %[3]s, refreshed each time you start. If your branch conflicts with it, merge %[3]s into %[1]s and resolve the conflicts.
- Make the change, and run the project's tests or checks where they exist.
- Commit your work to %[1]s with clear commit messages. Do not switch branches: only commits on %[1]s are collected.
- The checkout has no remote and no credentials. Do not try to push; devpad collects the branch when you finish.
- Leave nothing uncommitted when you finish.
- When you are done, reply with a short summary of what you changed and anything a reviewer should check.`, "`"+t.Branch+"`", "`"+p.DefaultBranch+"`", "`devpad/"+p.DefaultBranch+"`")
	return b.String()
}

func continuePrompt(t *Task, message, previousError string) string {
	branch := "`" + t.Branch + "`"
	switch {
	case message != "":
		return fmt.Sprintf("Feedback on your work:\n\n%s\n\nAddress it, commit the changes to %s, and reply with a short summary of what you changed.", message, branch)
	case previousError != "":
		return fmt.Sprintf("Your previous attempt stopped: %s\n\nContinue the task. Commit your work to %s and reply with a short summary when you are done.", previousError, branch)
	default:
		return fmt.Sprintf("Continue the task. Commit your work to %s and reply with a short summary when you are done.", branch)
	}
}

func (s *service) RunFinished(run domain.AgentRun) {
	if run.ParentRunID != 0 {
		return
	}
	s.spawn(func(ctx context.Context) { s.finish(ctx, run) })
}

func (s *service) finish(ctx context.Context, run domain.AgentRun) {
	t, err := s.repo.GetByRunID(ctx, run.ID)
	if err != nil || t == nil {
		return
	}
	unlock := s.lock(t.ID)
	defer unlock()
	if t, err = s.repo.GetByID(ctx, t.ID); err != nil || t == nil {
		return
	}
	s.settle(ctx, t, &run)
}

// settle moves an in-progress task on once its run has ended: a completed
// run's branch is collected for review, anything else fails the task. The
// caller holds the task lock.
func (s *service) settle(ctx context.Context, t *Task, run *domain.AgentRun) {
	if t.Status != StatusInProgress || t.RunID != run.ID {
		return
	}
	switch {
	case run.Interrupted():
		s.requeueInterrupted(ctx, t)
	case run.Status == domain.AgentRunCompleted:
		if err := s.collect(ctx, t); err != nil {
			s.failUnlessShuttingDown(ctx, t, err.Error())
		}
	case run.Status == domain.AgentRunCancelled:
		s.fail(ctx, t, "the agent run was cancelled")
	default:
		msg := run.Error
		if msg == "" {
			msg = "the agent run failed"
		}
		s.fail(ctx, t, msg)
	}
}

// requeueInterrupted puts a task whose run was cut short by a server restart
// back in the queue. Its next run continues the same conversation, told why
// it stopped, in the same sandbox.
func (s *service) requeueInterrupted(ctx context.Context, t *Task) {
	t.Status, t.RunID, t.Error = StatusQueued, 0, "the server restarted while the agent was working"
	if err := s.repo.Update(context.Background(), t); err != nil {
		log.Printf("task %d: requeueing after an interrupted run: %v", t.ID, err)
		return
	}
	s.kick()
}

// collect publishes the task branch from the sandbox into the canonical repo
// and puts the task up for review.
func (s *service) collect(ctx context.Context, t *Task) error {
	result, err := s.projects.PublishSandbox(ctx, t.UserID, t.ProjectID, t.WorkspaceID)
	if err != nil {
		return fmt.Errorf("collecting %s from the sandbox: %w", t.Branch, err)
	}
	cmp, err := s.projects.Compare(ctx, t.UserID, t.ProjectID, "", t.Branch)
	if err != nil {
		return fmt.Errorf("comparing %s: %w", t.Branch, err)
	}
	if cmp.Ahead == 0 {
		return fmt.Errorf("the agent finished without committing any changes to %s", t.Branch)
	}
	t.HeadSHA = result.SHA
	s.endRound(ctx, t, StatusInReview, "")

	s.mu.Lock()
	hook := s.reviewHook
	s.mu.Unlock()
	if hook != nil {
		hook.TaskReadyForReview(ctx, t, s.agentSummary(ctx, t))
	}
	return nil
}

// agentSummary is the agent's closing message, which the prompt asks to be
// a summary of the change.
func (s *service) agentSummary(ctx context.Context, t *Task) string {
	if t.ConversationID == 0 {
		return ""
	}
	turns, err := s.conversations.GetTurns(ctx, t.ConversationID, t.UserID)
	if err != nil {
		return ""
	}
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == domain.RoleAssistant {
			if text := strings.TrimSpace(turns[i].Text()); text != "" {
				return text
			}
		}
	}
	return ""
}

func (s *service) fail(ctx context.Context, t *Task, msg string) {
	s.endRound(ctx, t, StatusFailed, msg)
}

// failUnlessShuttingDown leaves the task as it is when the failure came from
// shutdown, so Reconcile picks it up on the next start.
func (s *service) failUnlessShuttingDown(ctx context.Context, t *Task, msg string) {
	if s.rootCtx.Err() != nil {
		return
	}
	s.fail(ctx, t, msg)
}

// endRound stops the task's sandbox and records the outcome. Only working
// agents hold sandbox slots, so the stopped sandbox frees one for queued
// tasks; it keeps its branch and the agent's transcript for the next round.
// The sandbox stops before the status changes, so a task in review or failed
// never still holds a slot.
func (s *service) endRound(ctx context.Context, t *Task, status Status, msg string) {
	if t.WorkspaceID != 0 {
		if _, err := s.workspaces.Stop(ctx, t.UserID, t.WorkspaceID); err != nil && !errors.Is(err, workspace.ErrNotFound) {
			log.Printf("task %d: stopping sandbox %d: %v", t.ID, t.WorkspaceID, err)
		}
	}
	t.Status, t.Error = status, msg
	if err := s.repo.Update(context.Background(), t); err != nil {
		log.Printf("task %d: recording %s %q: %v", t.ID, status, msg, err)
	}
	s.kick()
}

// Cancel stops the task's agent, removes its sandbox, and closes the task.
// Any branch already published stays in the project repo.
func (s *service) Cancel(ctx context.Context, userID, projectID, taskID int64) (*Task, error) {
	unlock := s.lock(taskID)
	defer unlock()
	t, err := s.get(ctx, userID, projectID, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status.Closed() {
		return nil, fmt.Errorf("%w: task is %s", ErrInvalidState, t.Status)
	}
	if err := s.teardown(ctx, t); err != nil {
		return nil, err
	}
	t.Status, t.Error, t.PendingMessage = StatusCancelled, "", ""
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
	s.kick()
	return t, nil
}

// Delete cancels the task if needed and removes it.
func (s *service) Delete(ctx context.Context, userID, projectID, taskID int64) error {
	unlock := s.lock(taskID)
	defer unlock()
	t, err := s.get(ctx, userID, projectID, taskID)
	if err != nil {
		return err
	}
	if err := s.teardown(ctx, t); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, t.ID); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.locks, t.ID)
	s.mu.Unlock()
	s.kick()
	return nil
}

// teardown cancels the task's active run, waits for it to stop so it is not
// writing to a deleted workspace, and removes the sandbox.
func (s *service) teardown(ctx context.Context, t *Task) error {
	if t.RunID != 0 {
		run, err := s.runs.GetRun(ctx, t.UserID, t.RunID)
		if err == nil && !domain.AgentRunStatusTerminal(run.Status) {
			if err := s.runs.CancelRun(ctx, t.UserID, t.RunID); err != nil && !errors.Is(err, domain.ErrAgentRunNotActive) {
				return fmt.Errorf("cancelling agent run: %w", err)
			}
			s.waitForRun(ctx, t.UserID, t.RunID)
		}
	}
	if t.WorkspaceID != 0 {
		if err := s.workspaces.Delete(ctx, t.UserID, t.WorkspaceID); err != nil && !errors.Is(err, workspace.ErrNotFound) {
			return fmt.Errorf("removing sandbox: %w", err)
		}
		t.WorkspaceID, t.ConversationID, t.RunID = 0, 0, 0
	}
	return nil
}

func (s *service) waitForRun(ctx context.Context, userID, runID int64) {
	deadline := time.Now().Add(cancelWait)
	for time.Now().Before(deadline) {
		run, err := s.runs.GetRun(ctx, userID, runID)
		if err != nil || domain.AgentRunStatusTerminal(run.Status) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	log.Printf("agent run %d did not stop within %s", runID, cancelWait)
}

// Reconcile settles tasks that were in progress when the server last
// stopped and resumes dispatching queued ones. A task whose run had not
// started, or whose run the restart interrupted, goes back to the queue and
// picks up where it left off. Any other task collects a run that completed
// before the restart, or fails with the run's error.
func (s *service) Reconcile(ctx context.Context) error {
	tasks, err := s.repo.ListByStatus(ctx, StatusInProgress)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.RunID == 0 {
			t.Status = StatusQueued
			if err := s.repo.Update(ctx, t); err != nil {
				return err
			}
			continue
		}
		run, err := s.runs.GetRun(ctx, t.UserID, t.RunID)
		if err != nil {
			s.fail(ctx, t, "interrupted by a server restart")
			continue
		}
		if domain.AgentRunStatusTerminal(run.Status) {
			s.RunFinished(*run)
		}
	}
	s.kick()
	return nil
}

// Shutdown stops background task work and waits for it to finish.
func (s *service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.rootCancel()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutting down task orchestrator: %w", ctx.Err())
	}
}
