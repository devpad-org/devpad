package pipeline

import (
	"context"
	"sort"
	"sync"
)

// jobSlots bound how many jobs of each project run at once, across all its
// pipelines. A slot that frees goes to the job waiting first: every
// official pipeline's job before any proposed one's, since a proposed
// pipeline's jobs come from the change under review and must never hold
// back the results that count; then the job of the oldest pipeline, the
// first in its file of that pipeline's, so a project's pipelines finish in
// the order they were started rather than all slowly together. Proposed
// pipelines' jobs hold at most proposedCapacity of a project's slots at
// once, so official ones always have the rest, and wait for at most one of
// them to end. A pipeline starts only with a slot, and its jobs wait
// behind only older pipelines' of its rank, so a project's running
// pipelines outnumber its slots only when one ahead of it, late to the
// queue, takes a slot ahead of a running pipeline's remaining jobs.
type jobSlots struct {
	capacity         int
	proposedCapacity int

	mu       sync.Mutex
	projects map[int64]*projectSlots
}

type projectSlots struct {
	used, proposedUsed int
	waiting            []*slotWaiter
}

// slotClaim is a job asking for a slot: its project, its pipeline and its
// place in it, and whether that pipeline is proposed.
type slotClaim struct {
	projectID  int64
	pipelineID int64
	position   int
	proposed   bool
}

// claimOf is the claim of the job at position of pipeline p.
func claimOf(p *Pipeline, position int) slotClaim {
	return slotClaim{projectID: p.ProjectID, pipelineID: p.ID, position: position, proposed: p.Purpose == PurposeProposed}
}

// slotWaiter is a job waiting for a slot.
type slotWaiter struct {
	slotClaim
	granted chan struct{}
}

func (w *slotWaiter) before(o *slotWaiter) bool {
	if w.proposed != o.proposed {
		return !w.proposed
	}
	if w.pipelineID != o.pipelineID {
		return w.pipelineID < o.pipelineID
	}
	return w.position < o.position
}

// maxProposedSlots is how many of a project's slots proposed pipelines'
// jobs hold at once.
const maxProposedSlots = 1

func newJobSlots(capacity int) *jobSlots {
	return &jobSlots{capacity: capacity, proposedCapacity: min(capacity, maxProposedSlots), projects: make(map[int64]*projectSlots)}
}

// acquire waits for a slot for the job c claims, and returns the function
// that frees it, which must be called once. It fails with ctx's cause if
// ctx ends first.
func (s *jobSlots) acquire(ctx context.Context, c slotClaim) (func(), error) {
	s.mu.Lock()
	p := s.projects[c.projectID]
	if p == nil {
		p = &projectSlots{}
		s.projects[c.projectID] = p
	}
	w := &slotWaiter{slotClaim: c, granted: make(chan struct{})}
	i := sort.Search(len(p.waiting), func(i int) bool { return w.before(p.waiting[i]) })
	p.waiting = append(p.waiting, nil)
	copy(p.waiting[i+1:], p.waiting[i:])
	p.waiting[i] = w
	s.grant(p)
	s.mu.Unlock()

	select {
	case <-w.granted:
		return s.releaser(c), nil
	case <-ctx.Done():
	}
	s.mu.Lock()
	select {
	case <-w.granted:
		// Granted as ctx ended: hand it on.
		s.mu.Unlock()
		s.release(c)
	default:
		for i, other := range p.waiting {
			if other == w {
				p.waiting = append(p.waiting[:i], p.waiting[i+1:]...)
				break
			}
		}
		s.forgetIdle(c.projectID, p)
		s.mu.Unlock()
	}
	return nil, context.Cause(ctx)
}

// grant gives the project's free slots to the jobs waiting, first first,
// passing over proposed pipelines' jobs while those hold all theirs. The
// caller holds mu.
func (s *jobSlots) grant(p *projectSlots) {
	for i := 0; i < len(p.waiting) && p.used < s.capacity; {
		w := p.waiting[i]
		if w.proposed && p.proposedUsed >= s.proposedCapacity {
			i++
			continue
		}
		p.waiting = append(p.waiting[:i], p.waiting[i+1:]...)
		p.used++
		if w.proposed {
			p.proposedUsed++
		}
		close(w.granted)
	}
}

// releaser frees c's slot once, however often it is called.
func (s *jobSlots) releaser(c slotClaim) func() {
	var once sync.Once
	return func() { once.Do(func() { s.release(c) }) }
}

// release frees c's slot, giving it to the first job waiting that may
// have it.
func (s *jobSlots) release(c slotClaim) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projects[c.projectID]
	if p == nil {
		return
	}
	p.used--
	if c.proposed {
		p.proposedUsed--
	}
	s.grant(p)
	s.forgetIdle(c.projectID, p)
}

// forgetIdle drops a project's entry once nothing uses or waits for its
// slots. The caller holds mu.
func (s *jobSlots) forgetIdle(projectID int64, p *projectSlots) {
	if p.used == 0 && len(p.waiting) == 0 {
		delete(s.projects, projectID)
	}
}
