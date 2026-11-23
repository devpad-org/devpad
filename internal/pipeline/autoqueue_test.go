package pipeline

import (
	"fmt"
	"slices"
	"testing"
)

func branchRun(branch string, trigger Trigger, except ...string) *autoRun {
	return &autoRun{key: autoKey{projectID: 1, kind: autoInto, branch: branch}, userID: 7, trigger: trigger, except: except}
}

// Runs of one key queued before a worker comes to them are one run, with
// the first trigger and every merge request left out.
func TestAutoQueueJoinsRunsOfAKey(t *testing.T) {
	q := newAutoQueue(2, 10)
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
	q := newAutoQueue(4, 10)
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

// Workers start only once the queue is open, never more than its bound,
// and never more than the runs that no free worker will come to.
func TestAutoQueueBoundsItsWorkers(t *testing.T) {
	q := newAutoQueue(2, 10)
	for i := 0; i < 5; i++ {
		if start, _ := q.add(branchRun(fmt.Sprint("b", i), TriggerTicked)); start {
			t.Fatal("a worker started before the queue opened")
		}
	}
	if n := q.begin(); n != 2 {
		t.Fatalf("begin started %d workers, want 2", n)
	}
	if start, _ := q.add(branchRun("b9", TriggerTicked)); start {
		t.Fatal("a third worker started")
	}
	// One worker finishes for good: another may start.
	q.next(true)
	if start, _ := q.add(branchRun("b10", TriggerTicked)); !start {
		t.Fatal("expected a worker to start again")
	}

	idle := newAutoQueue(4, 10)
	idle.begin()
	if start, _ := idle.add(branchRun("a", TriggerTicked)); !start {
		t.Fatal("expected a worker")
	}
	// The worker counted has not come to its run yet, so it takes this one
	// too.
	if start, _ := idle.add(branchRun("b", TriggerTicked)); !start {
		t.Fatal("two runs and one free worker: expected a second worker")
	}
	r, _ := idle.next(false)
	idle.next(false)
	// Both workers are reading: a new run needs a new worker.
	if start, _ := idle.add(branchRun("c", TriggerTicked)); !start {
		t.Fatal("expected a third worker while both read")
	}
	idle.done(r.key)
	// The done worker is free for the next run.
	if start, _ := idle.add(branchRun("d", TriggerTicked)); start {
		t.Fatal("a free worker was left idle for a new one")
	}
}

// Past its bound the queue drops new keys, though runs of keys it holds
// still join them.
func TestAutoQueueDropsPastItsBound(t *testing.T) {
	q := newAutoQueue(1, 2)
	q.add(branchRun("a", TriggerTicked))
	q.add(branchRun("b", TriggerTicked))
	if _, dropped := q.add(branchRun("c", TriggerTicked)); !dropped {
		t.Fatal("expected the third key dropped")
	}
	if _, dropped := q.add(branchRun("a", TriggerSetting)); dropped {
		t.Fatal("a run of a held key was dropped")
	}
}
