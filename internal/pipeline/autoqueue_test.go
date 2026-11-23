package pipeline

import (
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
)

// size reports how many runs wait, those waiting on a worker's read
// included, and how many workers there are.
func (q *autoQueue) size() (waiting, workers int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	waiting = len(q.pending)
	for _, r := range q.working {
		if r != nil {
			waiting++
		}
	}
	return waiting, q.workers
}

// idle reports whether the queue is open, has no worker, and has no run
// ready: those left wait for their interval.
func (q *autoQueue) idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.open || q.workers != 0 || len(q.working) != 0 {
		return false
	}
	now := q.now()
	for _, r := range q.pending {
		if !r.readyAt.After(now) {
			return false
		}
	}
	return true
}

// fakeClock is a clock whose time moves only when advanced, firing the
// timers due then.
type fakeClock struct {
	mu     sync.Mutex
	at     time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{at: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) after(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.at.Add(d), f: f}
	c.timers = append(c.timers, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := !t.stopped
		t.stopped = true
		return was
	}
}

// advance moves the clock on by d and fires, in order, the timers due.
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	var due []*fakeTimer
	kept := c.timers[:0]
	for _, t := range c.timers {
		switch {
		case t.stopped:
		case !t.at.After(c.at):
			t.stopped = true
			due = append(due, t)
		default:
			kept = append(kept, t)
		}
	}
	c.timers = kept
	c.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
	for _, t := range due {
		t.f()
	}
}

// clocked is a queue on a fake clock, recording the workers its timers
// start.
func clocked(maxWorkers int, interval time.Duration) (*autoQueue, *fakeClock, *int) {
	q := newAutoQueue(maxWorkers, 10, interval)
	c := newFakeClock()
	q.now, q.after = c.now, c.after
	started := new(int)
	q.startWorker = func() { *started++ }
	return q, c, started
}

func branchRun(branch string, trigger Trigger, except ...string) *autoRun {
	return &autoRun{key: autoKey{projectID: 1, kind: autoInto, branch: branch}, userID: 7, trigger: trigger, except: except}
}

// Runs of one key queued before a worker comes to them are one run, with
// the first trigger and every merge request left out.
func TestAutoQueueJoinsRunsOfAKey(t *testing.T) {
	q := newAutoQueue(2, 10, 0)
	q.begin()
	if start, dropped := q.add(branchRun("develop", TriggerMerged, "a")); !start || dropped {
		t.Fatalf("the first run should start a worker: %v %v", start, dropped)
	}
	for _, r := range []*autoRun{branchRun("develop", TriggerUpstream, "b"), branchRun("develop", TriggerMerged, "a")} {
		if start, dropped := q.add(r); start || dropped {
			t.Fatalf("a joined run started a worker or was dropped: %v %v", start, dropped)
		}
	}
	r, ok := q.next(false)
	if !ok || r.trigger != TriggerMerged || !slices.Equal(r.except, []string{"a", "b"}) {
		t.Fatalf("got %+v %v", r, ok)
	}
	q.done(r.key)
	if _, ok := q.next(false); ok {
		t.Fatal("expected nothing left")
	}
}

// A run of a key a worker is reading waits until it is done, then is
// queued, so that what changed while it read is read again, once.
func TestAutoQueueHoldsARunOfAKeyBeingRead(t *testing.T) {
	q := newAutoQueue(4, 10, 0)
	q.begin()
	q.add(branchRun("develop", TriggerMerged))
	r, _ := q.next(false)
	for i := 0; i < 5; i++ {
		if start, _ := q.add(branchRun("develop", TriggerUpstream, fmt.Sprint(i))); start {
			t.Fatal("a run waiting on a worker's read started another worker")
		}
	}
	if waiting, workers := q.size(); waiting != 1 || workers != 1 {
		t.Fatalf("waiting %d, workers %d", waiting, workers)
	}
	q.done(r.key)
	again, ok := q.next(false)
	if !ok || again.trigger != TriggerUpstream || len(again.except) != 5 {
		t.Fatalf("got %+v %v", again, ok)
	}
	q.done(again.key)
	if _, ok := q.next(false); ok {
		t.Fatal("expected nothing left")
	}
	if waiting, workers := q.size(); waiting != 0 || workers != 0 {
		t.Fatalf("waiting %d, workers %d", waiting, workers)
	}
}

func projectRun(projectID int64, branch string, trigger Trigger) *autoRun {
	return &autoRun{key: autoKey{projectID: projectID, kind: autoBranch, branch: branch}, userID: 7, trigger: trigger}
}

// Workers start only once the queue is open, never more than its bound,
// and never more than the projects with runs that no free worker will come
// to.
func TestAutoQueueBoundsItsWorkers(t *testing.T) {
	q := newAutoQueue(2, 10, 0)
	for i := int64(1); i <= 5; i++ {
		if start, _ := q.add(projectRun(i, "main", TriggerMerged)); start {
			t.Fatal("a worker started before the queue opened")
		}
	}
	if n := q.begin(); n != 2 {
		t.Fatalf("begin started %d workers, want 2", n)
	}
	if start, _ := q.add(projectRun(9, "main", TriggerMerged)); start {
		t.Fatal("a third worker started")
	}
	// One worker finishes for good: another may start.
	q.next(true)
	if start, _ := q.add(projectRun(10, "main", TriggerMerged)); !start {
		t.Fatal("expected a worker to start again")
	}

	idle := newAutoQueue(4, 10, 0)
	idle.begin()
	if start, _ := idle.add(projectRun(1, "main", TriggerMerged)); !start {
		t.Fatal("expected a worker")
	}
	// The worker counted has not come to its run yet; another project's
	// run gets one of its own.
	if start, _ := idle.add(projectRun(2, "main", TriggerMerged)); !start {
		t.Fatal("two projects' runs and one free worker: expected a second worker")
	}
	r, _ := idle.next(false)
	idle.next(false)
	// Both workers are reading: a new run needs a new worker.
	if start, _ := idle.add(projectRun(3, "main", TriggerMerged)); !start {
		t.Fatal("expected a third worker while both read")
	}
	idle.done(r.key)
	// The done worker is free for the next run.
	if start, _ := idle.add(projectRun(4, "main", TriggerMerged)); start {
		t.Fatal("a free worker was left idle for a new one")
	}
}

// A project's runs go one at a time, so one slow project holds one worker
// and never keeps another project's runs waiting.
func TestAutoQueueHandsOutOneRunOfAProjectAtATime(t *testing.T) {
	q := newAutoQueue(4, 10, 0)
	q.add(projectRun(1, "main", TriggerMerged))
	q.add(projectRun(1, "develop", TriggerMerged))
	q.add(projectRun(2, "main", TriggerMerged))
	if n := q.begin(); n != 2 {
		t.Fatalf("begin started %d workers for two projects", n)
	}
	first, _ := q.next(false)
	second, _ := q.next(false)
	if first.key.projectID != 1 || second.key.projectID != 2 {
		t.Fatalf("handed out %+v then %+v", first.key, second.key)
	}
	// Another run of a busy project starts no worker.
	if start, _ := q.add(projectRun(1, "release", TriggerMerged)); start {
		t.Fatal("a worker started for a busy project")
	}
	// A third worker, with only the busy projects' runs left, takes none.
	q.mu.Lock()
	q.workers++
	q.mu.Unlock()
	if r, ok := q.next(false); ok {
		t.Fatalf("handed out %+v while its project's other run is read", r.key)
	}
	q.done(first.key)
	if r, ok := q.next(false); !ok || r.key.branch != "develop" {
		t.Fatalf("after its first, the project's next run: %+v %v", r, ok)
	}
}

// A key is handed out at most once an interval: a run that comes sooner
// waits for it, joined by those after, and a timer starts a worker for it
// when it is up. What the user just did goes at once.
func TestAutoQueueSpacesAKeysRuns(t *testing.T) {
	q, clock, started := clocked(4, 30*time.Second)
	q.begin()
	q.add(projectRun(1, "main", TriggerMerged))
	r, _ := q.next(false)
	q.done(r.key)
	if _, ok := q.next(false); ok {
		t.Fatal("expected nothing more")
	}
	for i := 0; i < 9; i++ {
		clock.advance(3 * time.Second)
		if start, _ := q.add(projectRun(1, "main", TriggerUpstream)); start {
			t.Fatalf("a run %ds after the last started a worker", 3*(i+1))
		}
	}
	if !q.idle() || *started != 0 {
		t.Fatalf("idle %v, %d workers started by the timer", q.idle(), *started)
	}
	clock.advance(3 * time.Second)
	if *started != 1 {
		t.Fatalf("the timer started %d workers when the interval was up", *started)
	}
	if r, ok := q.next(false); !ok || r.trigger != TriggerUpstream {
		t.Fatalf("the waiting run: %+v %v", r, ok)
	}
	q.done(r.key)
	q.next(false)

	// Ticking the branch goes at once, and takes over a run that waits.
	clock.advance(time.Second)
	if start, _ := q.add(projectRun(1, "main", TriggerMerged)); start {
		t.Fatal("expected the merge to wait")
	}
	if start, _ := q.add(projectRun(1, "main", TriggerTicked)); !start {
		t.Fatal("expected the tick to go at once")
	}
	if r, ok := q.next(false); !ok || r.trigger != TriggerTicked {
		t.Fatalf("the ticked run: %+v %v", r, ok)
	}
}

// When the interval of several projects' runs is up at once, the timer
// starts a worker for each.
func TestAutoQueueTimerStartsAWorkerPerProject(t *testing.T) {
	q, clock, started := clocked(4, 30*time.Second)
	q.begin()
	for _, p := range []int64{1, 2} {
		q.add(projectRun(p, "main", TriggerMerged))
		r, _ := q.next(false)
		q.done(r.key)
	}
	q.next(false)
	q.next(false)
	q.add(projectRun(1, "main", TriggerMerged))
	q.add(projectRun(2, "main", TriggerMerged))
	q.add(projectRun(1, "develop", TriggerMerged))
	// develop's run was ready at once: a worker for it.
	if _, workers := q.size(); workers != 1 {
		t.Fatalf("%d workers for develop", workers)
	}
	r, _ := q.next(false)
	q.done(r.key)
	q.next(false)
	clock.advance(30 * time.Second)
	if *started != 2 {
		t.Fatalf("the timer started %d workers for two projects", *started)
	}
}

// Closing the queue stops its timer and starts no worker after.
func TestAutoQueueCloses(t *testing.T) {
	q, clock, started := clocked(4, 30*time.Second)
	q.begin()
	q.add(projectRun(1, "main", TriggerMerged))
	r, _ := q.next(false)
	q.done(r.key)
	q.next(false)
	q.add(projectRun(1, "main", TriggerMerged))
	q.close()
	clock.advance(time.Minute)
	if *started != 0 {
		t.Fatalf("%d workers started after close", *started)
	}
	if start, _ := q.add(projectRun(2, "main", TriggerTicked)); start {
		t.Fatal("a worker started after close")
	}
}

// Past its bound the queue drops new keys, though runs of keys it holds
// still join them.
func TestAutoQueueDropsPastItsBound(t *testing.T) {
	q := newAutoQueue(1, 2, 0)
	q.add(branchRun("a", TriggerMerged))
	q.add(branchRun("b", TriggerMerged))
	if _, dropped := q.add(branchRun("c", TriggerMerged)); !dropped {
		t.Fatal("expected the third key dropped")
	}
	if _, dropped := q.add(branchRun("a", TriggerUpstream)); dropped {
		t.Fatal("a run of a held key was dropped")
	}
}
