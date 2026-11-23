package pipeline

import (
	"slices"
	"sync"
	"time"
)

// The queue of automatic runs holds what devpad is to start by itself, by
// what it would be of, so that however many events tell of one branch or
// merge request, a worker reads it at most once while the events come and
// once after: a run queued for a key already queued joins it, and one for
// a key a worker is reading waits until that read is done, so the worker
// that comes to it reads what changed since.
//
// Three rules bound what the events can make devpad do:
//   - a key is handed out at most once per interval: a run that comes
//     sooner waits for the interval to end, then reads the newest state,
//     so a branch pushed to in a loop starts at most one pipeline an
//     interval, each of its newest commits then. Only what the user just did,
//     ticking a branch or changing the setting, goes at once.
//   - a project's runs are handed out one at a time, so a slow project
//     holds one worker, never waits on its own start lock, and never keeps
//     another project's runs waiting while a worker is free;
//   - at most maxWorkers workers, and maxPending runs waiting.

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
	// readyAt is when a worker may take it.
	readyAt time.Time
}

// join folds a later run of the same key into r. The first trigger stays,
// as the cause that made r's subject stale first, unless the later one is
// the user's own doing, which is why the run goes at once; the merge
// requests left out add up.
func (r *autoRun) join(later *autoRun) {
	if later.trigger.byUser() && !r.trigger.byUser() {
		r.trigger = later.trigger
	}
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
	// interval is the least time between two hand-outs of a key.
	interval time.Duration
	// now and after are the clock and its timers, which tests replace;
	// after returns what stops its timer.
	now   func() time.Time
	after func(d time.Duration, f func()) (stop func() bool)
	// startWorker starts a worker the queue has counted, when a timer finds
	// runs ready that no worker will come to.
	startWorker func()

	mu sync.Mutex
	// pending holds the runs waiting for a worker, by key, and order their
	// keys in the order they were queued.
	pending map[autoKey]*autoRun
	order   []autoKey
	// working holds the keys a worker is reading, each with the run of it
	// queued meanwhile, or nil; busy the projects of those keys.
	working map[autoKey]*autoRun
	busy    map[int64]bool
	// handed holds when each key was last handed out, while it still
	// holds back the key's next run.
	handed  map[autoKey]time.Time
	workers int
	// open says workers may start; until then runs only wait. closed says
	// devpad is stopping: no worker starts and no timer is set again.
	open, closed bool
	// stopTimer stops the timer set for timerAt, the soonest a run that
	// waits for its interval is ready, or is nil.
	stopTimer func() bool
	timerAt   time.Time
}

func newAutoQueue(maxWorkers, maxPending int, interval time.Duration) *autoQueue {
	return &autoQueue{
		maxWorkers: maxWorkers,
		maxPending: maxPending,
		interval:   interval,
		now:        time.Now,
		after: func(d time.Duration, f func()) func() bool {
			return time.AfterFunc(d, f).Stop
		},
		startWorker: func() {},
		pending:     make(map[autoKey]*autoRun),
		working:     make(map[autoKey]*autoRun),
		busy:        make(map[int64]bool),
		handed:      make(map[autoKey]time.Time),
	}
}

// add queues r. It reports whether the caller is to start a worker, which
// it counts as started, and whether r was dropped, because maxPending runs
// already wait.
func (q *autoQueue) add(r *autoRun) (startWorker, dropped bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
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
		if have.trigger.byUser() && have.readyAt.After(now) {
			have.readyAt = now
		}
	} else {
		if len(q.pending) >= q.maxPending {
			return false, true
		}
		r.readyAt = q.readyAt(r, now)
		q.pending[r.key] = r
		q.order = append(q.order, r.key)
	}
	if q.claimWorker(now) {
		return true, false
	}
	q.arm(now)
	return false, false
}

// readyAt is when a worker may take r: at once for the user's own doing,
// else once the interval since its key was last handed out has passed. The
// caller holds mu.
func (q *autoQueue) readyAt(r *autoRun, now time.Time) time.Time {
	if r.trigger.byUser() {
		return now
	}
	if at, ok := q.handed[r.key]; ok && at.Add(q.interval).After(now) {
		return at.Add(q.interval)
	}
	return now
}

// claimWorker counts a worker as started when the queue is open, has room
// for another, and has runs ready, of more projects than there are workers
// free to come to them: a worker is free from when it is counted until it
// is handed a run, and again once it is done with it. The caller holds mu.
func (q *autoQueue) claimWorker(now time.Time) bool {
	if !q.open || q.closed || q.workers >= q.maxWorkers {
		return false
	}
	free := q.workers - len(q.working)
	if q.readyProjects(now, free+1) <= free {
		return false
	}
	q.workers++
	return true
}

// readyProjects counts, up to enough, the projects no worker is reading
// that have a run ready. The caller holds mu.
func (q *autoQueue) readyProjects(now time.Time, enough int) int {
	seen := make(map[int64]bool)
	for _, k := range q.order {
		if len(seen) >= enough {
			break
		}
		if !q.busy[k.projectID] && !q.pending[k].readyAt.After(now) {
			seen[k.projectID] = true
		}
	}
	return len(seen)
}

// begin opens the queue and returns how many workers the caller is to
// start, which it counts as started.
func (q *autoQueue) begin() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.open = true
	now := q.now()
	n := 0
	for q.claimWorker(now) {
		n++
	}
	q.arm(now)
	return n
}

// next hands a worker the first run queued that is ready and of a project
// no other worker is reading, marking its key worked on. A worker that is
// to stop (stop), or finds nothing to take, is told false and no longer
// counted.
func (q *autoQueue) next(stop bool) (*autoRun, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	if !stop {
		for i, k := range q.order {
			r := q.pending[k]
			if q.busy[k.projectID] || r.readyAt.After(now) {
				continue
			}
			q.order = slices.Delete(q.order, i, i+1)
			delete(q.pending, k)
			q.working[k] = nil
			q.busy[k.projectID] = true
			q.handed[k] = now
			return r, true
		}
	}
	q.workers--
	q.arm(now)
	return nil, false
}

// done ends a worker's run of key, queueing the run of it that waited
// meanwhile, if any, for when its interval allows: the worker that handed
// it in comes to it, or a timer does.
func (q *autoQueue) done(k autoKey) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	waiting := q.working[k]
	delete(q.working, k)
	delete(q.busy, k.projectID)
	if waiting != nil {
		waiting.readyAt = q.readyAt(waiting, now)
		q.pending[k] = waiting
		q.order = append(q.order, k)
	}
	q.forgetHandedOut(now)
}

// forgetHandedOut drops, once there are many, the hand-outs that no longer
// hold a key back. The caller holds mu.
func (q *autoQueue) forgetHandedOut(now time.Time) {
	if len(q.handed) <= q.maxPending {
		return
	}
	for k, at := range q.handed {
		if !at.Add(q.interval).After(now) {
			delete(q.handed, k)
		}
	}
}

// arm sets a timer for the soonest a run waiting for its interval is
// ready, unless one already fires sooner. The caller holds mu.
func (q *autoQueue) arm(now time.Time) {
	if q.closed || !q.open {
		return
	}
	var soonest time.Time
	for _, r := range q.pending {
		if r.readyAt.After(now) && (soonest.IsZero() || r.readyAt.Before(soonest)) {
			soonest = r.readyAt
		}
	}
	if soonest.IsZero() || (q.stopTimer != nil && !q.timerAt.After(soonest)) {
		return
	}
	if q.stopTimer != nil {
		q.stopTimer()
	}
	q.timerAt = soonest
	q.stopTimer = q.after(soonest.Sub(now), q.fire)
}

// fire starts the workers the runs that became ready need, and sets the
// timer for the next.
func (q *autoQueue) fire() {
	q.mu.Lock()
	q.stopTimer, q.timerAt = nil, time.Time{}
	now := q.now()
	n := 0
	for q.claimWorker(now) {
		n++
	}
	q.arm(now)
	q.mu.Unlock()
	for range n {
		q.startWorker()
	}
}

// quit stops counting a worker that could not start.
func (q *autoQueue) quit() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.workers--
}

// close stops the queue for good: no worker starts, and the timer stops.
func (q *autoQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	if q.stopTimer != nil {
		q.stopTimer()
		q.stopTimer = nil
	}
}
