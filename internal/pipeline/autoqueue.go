package pipeline

import (
	"slices"
	"sync"
)

// The queue of automatic runs holds what devpad is to start by itself, by
// what it would be of, so that however many events tell of one branch or
// merge request, a worker reads it at most once while the events come and
// once after: a run queued for a key already queued joins it, and one for
// a key a worker is reading waits until that read is done, so the worker
// that comes to it reads what changed since.

// autoKind is what an automatic run is of.
type autoKind int

const (
	// autoBranch is a ticked branch's pipeline.
	autoBranch autoKind = iota
	// autoMergeRequest is a merge request's pipeline.
	autoMergeRequest
	// autoInto is the pipelines of the open merge requests into a branch,
	// each of which a worker queues as an autoMergeRequest run.
	autoInto
)

// autoKey is what an automatic run is of: a branch of a project (autoBranch,
// autoInto) or a merge request (autoMergeRequest).
type autoKey struct {
	projectID      int64
	kind           autoKind
	branch         string
	mergeRequestID int64
}

// autoRun is a run devpad is to make by itself.
type autoRun struct {
	key     autoKey
	userID  int64
	trigger Trigger
	// except are the source branches of the merge requests an autoInto run
	// leaves out: those that merged into its branch, which would only test
	// the commit their own merge made.
	except []string
}

// join folds a later run of the same key into r. The first trigger stays,
// as the cause that made r's subject stale first, and the merge requests
// left out add up.
func (r *autoRun) join(later *autoRun) {
	for _, b := range later.except {
		if !slices.Contains(r.except, b) {
			r.except = append(r.except, b)
		}
	}
}

type autoQueue struct {
	// maxWorkers bounds the workers, and maxPending the runs waiting.
	maxWorkers int
	maxPending int

	mu sync.Mutex
	// pending holds the runs waiting for a worker, by key, and order their
	// keys in the order they were queued.
	pending map[autoKey]*autoRun
	order   []autoKey
	// working holds the keys a worker is reading, each with the run of it
	// queued meanwhile, or nil.
	working map[autoKey]*autoRun
	workers int
	// open says workers may start; until then runs only wait.
	open bool
}

func newAutoQueue(maxWorkers, maxPending int) *autoQueue {
	return &autoQueue{
		maxWorkers: maxWorkers,
		maxPending: maxPending,
		pending:    make(map[autoKey]*autoRun),
		working:    make(map[autoKey]*autoRun),
	}
}

// add queues r. It reports whether the caller is to start a worker, which
// it counts as started, and whether r was dropped, because maxPending runs
// already wait.
func (q *autoQueue) add(r *autoRun) (startWorker, dropped bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if waiting, ok := q.working[r.key]; ok {
		if waiting == nil {
			q.working[r.key] = r
		} else {
			waiting.join(r)
		}
		return false, false
	}
	if have := q.pending[r.key]; have != nil {
		have.join(r)
		return false, false
	}
	if len(q.pending) >= q.maxPending {
		return false, true
	}
	q.pending[r.key] = r
	q.order = append(q.order, r.key)
	return q.claimWorker(), false
}

// claimWorker counts a worker as started when the queue is open, has room
// for another, and has more runs waiting than workers free to come to them:
// a worker is free from when it is counted until it is handed a run, and
// again once it is done with it. The caller holds mu.
func (q *autoQueue) claimWorker() bool {
	free := q.workers - len(q.working)
	if !q.open || q.workers >= q.maxWorkers || free >= len(q.pending) {
		return false
	}
	q.workers++
	return true
}

// begin opens the queue and returns how many workers the caller is to
// start, which it counts as started.
func (q *autoQueue) begin() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.open = true
	n := 0
	for q.claimWorker() {
		n++
	}
	return n
}

// next hands a worker the run queued first, marking its key worked on. A
// worker that is to stop (stop), or finds nothing left, is told false and
// no longer counted.
func (q *autoQueue) next(stop bool) (*autoRun, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if stop || len(q.order) == 0 {
		q.workers--
		return nil, false
	}
	k := q.order[0]
	q.order = q.order[1:]
	r := q.pending[k]
	delete(q.pending, k)
	q.working[k] = nil
	return r, true
}

// done ends a worker's run of key, queueing the run of it that waited
// meanwhile, if any: the worker that handed it in comes to it.
func (q *autoQueue) done(k autoKey) {
	q.mu.Lock()
	defer q.mu.Unlock()
	waiting := q.working[k]
	delete(q.working, k)
	if waiting != nil {
		q.pending[k] = waiting
		q.order = append(q.order, k)
	}
}

// opened reports whether begin has opened the queue.
func (q *autoQueue) opened() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.open
}

// quit stops counting a worker that could not start.
func (q *autoQueue) quit() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.workers--
}

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
