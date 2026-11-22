package pipeline

import (
	"context"
	"sort"
	"sync"
)

// jobSlots bound how many jobs of each project run at once, across all its
// pipelines. A slot that frees goes to the job waiting that belongs to the
// oldest pipeline, the first in its file of that pipeline's, so a project's
// pipelines finish in the order they were started rather than all slowly
// together. A pipeline holds a slot from when it starts until its last job
// ends, which also bounds how many of a project's pipelines run at once.
type jobSlots struct {
	capacity int

	mu       sync.Mutex
	projects map[int64]*projectSlots
}

type projectSlots struct {
	used    int
	waiting []*slotWaiter
}

// slotWaiter is a job waiting for a slot, by its pipeline and its place in
// it.
type slotWaiter struct {
	pipelineID int64
	position   int
	granted    chan struct{}
}

func (w *slotWaiter) before(o *slotWaiter) bool {
	if w.pipelineID != o.pipelineID {
		return w.pipelineID < o.pipelineID
	}
	return w.position < o.position
}

func newJobSlots(capacity int) *jobSlots {
	return &jobSlots{capacity: capacity, projects: make(map[int64]*projectSlots)}
}

// acquire waits for a slot of the project for the job at position of a
// pipeline, and returns the function that frees it, which must be called
// once. It fails with ctx's cause if ctx ends first.
func (s *jobSlots) acquire(ctx context.Context, projectID, pipelineID int64, position int) (func(), error) {
	s.mu.Lock()
	p := s.projects[projectID]
	if p == nil {
		p = &projectSlots{}
		s.projects[projectID] = p
	}
	if p.used < s.capacity && len(p.waiting) == 0 {
		p.used++
		s.mu.Unlock()
		return s.releaser(projectID), nil
	}
	w := &slotWaiter{pipelineID: pipelineID, position: position, granted: make(chan struct{})}
	i := sort.Search(len(p.waiting), func(i int) bool { return w.before(p.waiting[i]) })
	p.waiting = append(p.waiting, nil)
	copy(p.waiting[i+1:], p.waiting[i:])
	p.waiting[i] = w
	s.mu.Unlock()

	select {
	case <-w.granted:
		return s.releaser(projectID), nil
	case <-ctx.Done():
	}
	s.mu.Lock()
	select {
	case <-w.granted:
		// Granted as ctx ended: hand it on.
		s.mu.Unlock()
		s.release(projectID)
	default:
		for i, other := range p.waiting {
			if other == w {
				p.waiting = append(p.waiting[:i], p.waiting[i+1:]...)
				break
			}
		}
		s.forgetIdle(projectID, p)
		s.mu.Unlock()
	}
	return nil, context.Cause(ctx)
}

// releaser frees a slot of the project once, however often it is called.
func (s *jobSlots) releaser(projectID int64) func() {
	var once sync.Once
	return func() { once.Do(func() { s.release(projectID) }) }
}

// release frees a slot, giving it to the first job waiting.
func (s *jobSlots) release(projectID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projects[projectID]
	if p == nil {
		return
	}
	if len(p.waiting) > 0 {
		w := p.waiting[0]
		p.waiting = p.waiting[1:]
		close(w.granted)
		return
	}
	p.used--
	s.forgetIdle(projectID, p)
}

// forgetIdle drops a project's entry once nothing uses or waits for its
// slots. The caller holds mu.
func (s *jobSlots) forgetIdle(projectID int64, p *projectSlots) {
	if p.used == 0 && len(p.waiting) == 0 {
		delete(s.projects, projectID)
	}
}

// running reports how many of the project's slots are in use.
func (s *jobSlots) running(projectID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.projects[projectID]; p != nil {
		return p.used
	}
	return 0
}
