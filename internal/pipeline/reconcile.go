package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/devpad-org/devpad/internal/container"
)

// A devpad that stops, whether it shuts down or crashes, leaves its
// pipelines queued or running in the database; Shutdown records nothing, so
// both end the same way. The next devpad's Reconcile marks them interrupted
// and runs each branch's or merge request's pipeline again, at its commits
// then, and removes what they left: their jobs' containers and networks, by
// their labels, and their refs in the canonical repo.
//
// Removing is driven by the database, never by what Docker has alone:
// another devpad sharing the Docker daemon may be running pipelines of its
// own, whose UIDs this one does not know and so leaves alone. A pipeline is
// recorded as finished only once its jobs have removed what they made, so
// what a finished pipeline still has in Docker was made late, by a create
// that outlived being cancelled, or left by a devpad that stopped; the sweep
// runs again every sweepInterval to remove it.

// interruptedReason is why a pipeline a previous devpad left unfinished
// ended.
const interruptedReason = "devpad stopped before it finished"

// bundlePattern names the files pipelines' bundles are kept in.
const bundlePattern = "devpad-pipeline-*.bundle"

func (s *service) Reconcile(ctx context.Context) error {
	interrupted, err := s.repo.InterruptUnfinished(ctx, interruptedReason, s.now())
	if err != nil {
		return fmt.Errorf("interrupting unfinished pipelines: %w", err)
	}
	s.removeLeftBundles()
	for _, p := range interrupted {
		s.announce(p.ID)
	}
	reruns := latestBySubject(interrupted)
	s.spawn(func(ctx context.Context) {
		s.sweep(ctx)
		for _, p := range reruns {
			s.rerun(ctx, p)
		}
		ticker := time.NewTicker(s.sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweep(ctx)
			}
		}
	})
	return nil
}

// latestBySubject keeps the newest of pipelines of each branch or merge
// request, in the order they came.
func latestBySubject(pipelines []*Pipeline) []*Pipeline {
	newest := make(map[subject]*Pipeline)
	var order []subject
	for _, p := range pipelines {
		sub := p.subject()
		if have, ok := newest[sub]; !ok {
			order = append(order, sub)
		} else if have.ID > p.ID {
			continue
		}
		newest[sub] = p
	}
	out := make([]*Pipeline, 0, len(order))
	for _, sub := range order {
		out = append(out, newest[sub])
	}
	return out
}

// rerun starts again the pipeline of the branch or merge request an
// interrupted pipeline was of, unless it no longer runs one.
func (s *service) rerun(ctx context.Context, p *Pipeline) {
	in := StartInput{UserID: p.UserID, ProjectID: p.ProjectID, Trigger: TriggerRestart}
	if p.Kind == KindMergeRequest {
		in.MergeRequestID = p.MergeRequestID
	} else {
		in.Branch = p.Branch
	}
	next, _, err := s.start(ctx, in, p.ID)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("pipeline %d: not running it again after it was interrupted: %v", p.ID, err)
		}
		return
	}
	log.Printf("pipeline %d: interrupted, running it again as pipeline %d", p.ID, next.ID)
}

// removeLeftBundles removes the bundles a devpad that stopped left in its
// own directory, which no pipeline of this one uses yet. In a directory it
// may share, such as the system's temporary one, it removes nothing.
func (s *service) removeLeftBundles() {
	if !s.ownsBundleDir {
		return
	}
	left, err := filepath.Glob(filepath.Join(s.bundleDir, bundlePattern))
	if err != nil {
		log.Printf("pipelines: listing bundles left behind: %v", err)
		return
	}
	for _, path := range left {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("pipelines: removing a bundle left behind: %v", err)
		}
	}
}

// sweep removes what finished pipelines left behind.
func (s *service) sweep(ctx context.Context) {
	s.sweepDocker(ctx)
	s.sweepRefs(ctx)
}

// sweepDocker removes the containers and networks of jobs whose pipeline
// the database has and shows finished.
func (s *service) sweepDocker(ctx context.Context) {
	resources, err := s.docker.ListJobResources(ctx, container.Job{})
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("pipelines: listing what jobs left in Docker: %v", err)
		}
		return
	}
	seen := make(map[string]bool)
	var uids []string
	for _, r := range resources {
		if !seen[r.Job.Pipeline] {
			seen[r.Job.Pipeline] = true
			uids = append(uids, r.Job.Pipeline)
		}
	}
	if len(uids) == 0 {
		return
	}
	statuses, err := s.repo.StatusesByUID(ctx, uids)
	if err != nil {
		log.Printf("pipelines: reading which pipelines left something in Docker: %v", err)
		return
	}
	for _, uid := range uids {
		status, ours := statuses[uid]
		if !ours || !status.Finished() {
			continue
		}
		cleanupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
		if err := s.docker.RemoveJobResources(cleanupCtx, container.Job{Pipeline: uid}); err != nil {
			log.Printf("pipelines: removing what pipeline %s left in Docker: %v", uid, err)
		}
		cancel()
	}
}

// sweepRefs deletes the refs finished pipelines may have left in their
// canonical repos.
func (s *service) sweepRefs(ctx context.Context) {
	pinned, err := s.repo.ListPinnedFinished(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("pipelines: listing pipelines that may hold a ref: %v", err)
		}
		return
	}
	for _, p := range pinned {
		cleanupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
		s.unpin(cleanupCtx, p)
		cancel()
	}
}

// ProjectDeleting stops every pipeline of the project, and waits a while
// for their jobs to be removed.
func (s *service) ProjectDeleting(ctx context.Context, projectID int64) {
	s.mu.Lock()
	var stopping []*activePipeline
	for _, a := range s.active {
		if a.subject.projectID == projectID {
			a.stop(errProjectDeleted)
			stopping = append(stopping, a)
		}
	}
	s.mu.Unlock()
	timer := time.NewTimer(s.cancelWait)
	defer timer.Stop()
	for _, a := range stopping {
		select {
		case <-a.done:
		case <-timer.C:
			log.Printf("project %d: its pipelines are still stopping after %s", projectID, s.cancelWait)
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.rootCancel(errShutdown)

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutting down pipelines: %w", ctx.Err())
	}
}
