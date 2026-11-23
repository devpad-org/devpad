package mergerequest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/project"
)

// recordingHeadListener records each head move it is told of, as "!<id>
// opened" or "!<id> moved". It takes the moved merge request's lock, the
// task's lock and the project's sources lock as it is told, so a move told
// while any of them is held never returns, and the test times out.
type recordingHeadListener struct {
	svc   *service
	mu    sync.Mutex
	moves []string
}

func (l *recordingHeadListener) MergeRequestHeadMoved(_ context.Context, m HeadMove) {
	l.svc.lock("mr", m.MergeRequestID)()
	l.svc.lock("sources", m.ProjectID)()
	if mr, err := l.svc.repo.GetByID(context.Background(), m.MergeRequestID); err == nil && mr != nil && mr.TaskID != 0 {
		l.svc.lock("task", mr.TaskID)()
	}
	what := "moved"
	if m.Opened {
		what = "opened"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.moves = append(l.moves, fmt.Sprintf("!%d %s", m.MergeRequestID, what))
}

// told returns the moves told so far, and forgets them.
func (l *recordingHeadListener) told() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := strings.Join(l.moves, ", ")
	l.moves = nil
	return out
}

func (e *env) listenHeads() *recordingHeadListener {
	l := &recordingHeadListener{svc: e.svc}
	e.svc.AddHeadListener(l)
	return l
}

// within fails the test unless fn returns in time: a head move told while a
// lock it takes is held never does.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never returned: a head move was told under a lock", what)
	}
}

// A task's merge request is told when it opens and each time a round moves
// its head, never for a round that moved nothing, and only once the task's
// and the merge request's locks are free.
func TestTaskRoundsTellHeadListeners(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	l := e.listenHeads()
	var mr *MergeRequest
	within(t, "opening the task's merge request", func() { mr = e.openTaskMR(t) })
	if got := l.told(); got != fmt.Sprintf("!%d opened", mr.ID) {
		t.Fatalf("told %q", got)
	}

	tk, _ := e.tasks.Get(ctx, e.owner, e.pid(), e.taskID)
	tk.HeadSHA = "head2"
	within(t, "a round", func() { e.svc.TaskReadyForReview(ctx, tk, "") })
	within(t, "a round that moved nothing", func() { e.svc.TaskReadyForReview(ctx, tk, "Still a cart.") })
	if got := l.told(); got != fmt.Sprintf("!%d moved", mr.ID) {
		t.Fatalf("told %q", got)
	}
}

// Opening a merge request by hand or through the assistant tells the
// listeners; one refused tells nobody.
func TestCreateTellsHeadListeners(t *testing.T) {
	e := setup(t)
	e.gitFlow()
	ctx := context.Background()
	l := e.listenHeads()
	var feature, release *MergeRequest
	within(t, "create", func() {
		var err error
		if feature, err = e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"}); err != nil {
			t.Errorf("create: %v", err)
		}
		if release, err = e.svc.AssistantCreate(ctx, e.owner, e.pid(), CreateInput{Title: "Release", SourceBranch: "develop", TargetBranch: "main"}); err != nil {
			t.Errorf("assistant create: %v", err)
		}
	})
	if t.Failed() {
		return
	}
	if _, err := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Again", SourceBranch: "feature"}); err == nil {
		t.Fatal("expected a second open merge request from feature refused")
	}
	if got, want := l.told(), fmt.Sprintf("!%d opened, !%d opened", feature.ID, release.ID); got != want {
		t.Fatalf("told %q, want %q", got, want)
	}
}

// A publish or a fast-forward that moves an open merge request's branch
// tells the listeners, once the merge request's lock is free; one that
// moves nothing, or another user's, tells nobody.
func TestFollowedBranchesTellHeadListeners(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mr := e.openTaskMR(t)
	l := e.listenHeads()

	within(t, "following a publish", func() {
		e.svc.BranchMoved(ctx, published(e.owner, e.pid(), "task/1", "head2"))
		e.svc.BranchMoved(ctx, published(e.owner, e.pid(), "task/1", "head2"))
		e.svc.BranchMoved(ctx, published(e.stranger, e.pid(), "task/1", "evil"))
	})
	e.projects.branches = append(e.projects.branches, agent.RepoRef{Name: "task/1", SHA: "upstream1"})
	within(t, "following a fast-forward", func() {
		e.svc.BranchMoved(ctx, fastForwarded(e.owner, e.pid(), "task/1", "upstream1"))
	})
	if got, want := l.told(), fmt.Sprintf("!%d moved, !%d moved", mr.ID, mr.ID); got != want {
		t.Fatalf("told %q, want %q", got, want)
	}
}

// A merge into a protected branch moves the merge request from it, such as
// a release, and tells the listeners of that one once the merging merge
// request's lock is free. The merge's own move of its target, which the
// project tells while that lock is held, is left to followMerge.
func TestMergesTellHeadListenersOfTheReleaseTheyMove(t *testing.T) {
	e := setup(t)
	e.gitFlow()
	ctx := context.Background()
	feature, _ := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	release, _ := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Release", SourceBranch: "develop", TargetBranch: "main"})
	l := e.listenHeads()

	// Told during the merge, under the lock: nothing follows it there.
	e.projects.onMerge = func() {
		e.svc.BranchMoved(ctx, project.BranchMove{UserID: e.owner, ProjectID: e.pid(), Branch: "develop", SHA: "elsewhere", Cause: project.MoveMerged, Source: "feature"})
	}
	within(t, "merge", func() {
		if _, err := e.svc.Merge(ctx, e.owner, e.pid(), feature.ID, MergeInput{}); err != nil {
			t.Errorf("merge: %v", err)
		}
	})
	if got, want := l.told(), fmt.Sprintf("!%d moved", release.ID); got != want {
		t.Fatalf("told %q, want %q", got, want)
	}
	if got, _ := e.svc.repo.GetByID(ctx, release.ID); got.HeadSHA != "mergecommit" {
		t.Fatalf("expected the release to follow develop's merge commit, got %q", got.HeadSHA)
	}
}

// The pipeline package reads which open merge requests go into a branch.
func TestListOpenInto(t *testing.T) {
	e := setup(t)
	e.gitFlow()
	ctx := context.Background()
	feature, _ := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Feature", SourceBranch: "feature"})
	staging, _ := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Staging", SourceBranch: "staging"})
	release, _ := e.svc.Create(ctx, e.owner, e.pid(), CreateInput{Title: "Release", SourceBranch: "develop", TargetBranch: "main"})
	if _, err := e.svc.Close(ctx, e.owner, e.pid(), staging.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	ids := func(mrs []*MergeRequest) []int64 {
		var out []int64
		for _, mr := range mrs {
			out = append(out, mr.ID)
		}
		return out
	}
	if got, err := e.svc.ListOpenInto(ctx, e.owner, e.pid(), "develop"); err != nil || !slices.Equal(ids(got), []int64{feature.ID}) {
		t.Fatalf("into develop: %v (%v)", ids(got), err)
	}
	if got, err := e.svc.ListOpenInto(ctx, e.owner, e.pid(), "main"); err != nil || !slices.Equal(ids(got), []int64{release.ID}) {
		t.Fatalf("into main: %v (%v)", ids(got), err)
	}
	if _, err := e.svc.ListOpenInto(ctx, e.stranger, e.pid(), "develop"); err == nil {
		t.Fatal("expected another user refused")
	}
}
