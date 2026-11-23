package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/container"
)

// A devpad that stops, whether it shuts down or crashes, leaves its
// pipelines queued or running in the database; Shutdown records nothing, so
// both end the same way. The next devpad's Reconcile marks them interrupted
// and runs each branch's or merge request's pipeline again, at its commits
// then, unless those have a result already, and removes what they left:
// their jobs' containers and networks, by their labels, and their refs in
// the canonical repo. It also tells the listeners of every pipeline that
// finished without their all being told, which is how a pipeline that
// finished as devpad stopped is announced at least once. A pipeline this
// process runs is never taken for one left behind: its run is known from
// before its record is made. Once the interrupted pipelines have run
// again, automatic runs begin, with a catch-up on every ticked branch and
// merge request into one (auto.go), for what moved while devpad was down.
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

// A pipeline's bundle is kept in a file named by its UID, so that what
// removes the bundles a devpad that stopped left knows a running
// pipeline's.
const (
	bundlePrefix  = "devpad-pipeline-"
	bundleSuffix  = ".bundle"
	bundlePattern = bundlePrefix + "*" + bundleSuffix
)

func bundleName(uid string) string {
	return bundlePrefix + uid + bundleSuffix
}

func (s *service) Reconcile(ctx context.Context) error {
	interrupted, err := s.interruptLeft(ctx)
	if err != nil {
		return fmt.Errorf("interrupting unfinished pipelines: %w", err)
	}
	s.removeLeftBundles()
	if unannounced, err := s.repo.ListUnannounced(ctx); err != nil {
		log.Printf("pipelines: listing those to announce: %v", err)
	} else {
		s.announce(unannounced...)
	}
	reruns := latestBySubject(interrupted)
	s.spawn(func(ctx context.Context) {
		s.sweep(ctx)
		for _, p := range reruns {
			s.rerun(ctx, p)
		}
		// Automatic runs start once every interrupted pipeline has run
		// again, so none is run twice. The catch-up on what changed while
		// devpad was down queues behind the runs it was told of since it
		// started, and joins those of the same branches and merge requests.
		s.catchUp(ctx)
		s.beginAutomatic()
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

// interruptLeft marks interrupted every pipeline a devpad that stopped left
// queued or running, and returns them as they now are. A record no run of
// this process holds was left so: a run holds its record from before it is
// made.
func (s *service) interruptLeft(ctx context.Context) ([]*Pipeline, error) {
	unfinished, err := s.repo.ListUnfinished(ctx)
	if err != nil {
		return nil, err
	}
	var left []*Pipeline
	s.mu.Lock()
	for _, p := range unfinished {
		if s.runs[p.UID] == nil {
			left = append(left, p)
		}
	}
	s.mu.Unlock()
	return s.repo.Interrupt(ctx, left, interruptedReason, s.now())
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
// interrupted pipeline was of, unless it no longer runs one, or its commits
// and jobs now are those of a pipeline that counts and is newer than the
// interrupted one, such as a newer commit's that finished with a result
// before devpad stopped. An older result is the one an interrupted Run
// again was to repeat, so it runs again.
func (s *service) rerun(ctx context.Context, p *Pipeline) {
	in := StartInput{UserID: p.UserID, ProjectID: p.ProjectID, Trigger: TriggerRestart, ReuseResult: true}
	if p.Kind == KindMergeRequest {
		in.MergeRequestID = p.MergeRequestID
	} else {
		in.Branch = p.Branch
	}
	next, created, err := s.start(ctx, in, p.ID)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			log.Printf("pipeline %d: not running it again after it was interrupted: %v", p.ID, err)
		}
	case created:
		log.Printf("pipeline %d: interrupted, running it again as pipeline %d", p.ID, next.ID)
	default:
		log.Printf("pipeline %d: interrupted; pipeline %d tests its subject as it is now", p.ID, next.ID)
	}
}

// removeLeftBundles removes the bundles a devpad that stopped left in its
// own directory, never one of a pipeline this one runs. In a directory it
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
		uid := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), bundlePrefix), bundleSuffix)
		if s.hasRun(uid) {
			continue
		}
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
// the database has and shows finished, and no run of this process holds.
func (s *service) sweepDocker(ctx context.Context) {
	s.removeJobResources(ctx, func(uid string, rec UIDRecord) bool {
		return rec.Status.Finished() && !s.hasRun(uid)
	})
}

// removeJobResources removes what pipelines' jobs have in Docker, for each
// pipeline the database has that remove picks. A pipeline it does not have,
// such as another devpad's, it leaves alone.
func (s *service) removeJobResources(ctx context.Context, remove func(uid string, rec UIDRecord) bool) {
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
	records, err := s.repo.RecordsByUID(ctx, uids)
	if err != nil {
		log.Printf("pipelines: reading which pipelines left something in Docker: %v", err)
		return
	}
	for _, uid := range uids {
		rec, ours := records[uid]
		if !ours || !remove(uid, rec) {
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
		if s.hasRun(p.UID) {
			continue
		}
		cleanupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
		s.unpin(cleanupCtx, p)
		cancel()
	}
}

// ProjectDeleting stops every pipeline of the project and starts none until
// the deletion ends, waits a while for their jobs to be removed, then
// removes whatever the project's pipelines still have in Docker, such as
// what a create cut short made late: once the project is gone, so are its
// pipelines' records, and the sweep leaves alone what it finds no record of.
func (s *service) ProjectDeleting(ctx context.Context, projectID int64) {
	s.mu.Lock()
	s.deleting[projectID] = true
	var stopping []*activePipeline
	for _, a := range s.runs {
		if a.subject.projectID == projectID {
			a.stop(errProjectDeleted)
			stopping = append(stopping, a)
		}
	}
	s.mu.Unlock()
	if !s.awaitRuns(ctx, stopping) && ctx.Err() == nil {
		log.Printf("project %d: its pipelines are still stopping after %s", projectID, s.cancelWait)
	}
	s.removeJobResources(ctx, func(_ string, rec UIDRecord) bool { return rec.ProjectID == projectID })
}

// ProjectDeleteEnded starts the project's pipelines again, should it stay.
func (s *service) ProjectDeleteEnded(projectID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.deleting, projectID)
}

// awaitRuns waits, at most cancelWait in all, for runs to end, reporting
// false when some had not.
func (s *service) awaitRuns(ctx context.Context, runs []*activePipeline) bool {
	timer := time.NewTimer(s.cancelWait)
	defer timer.Stop()
	for _, a := range runs {
		select {
		case <-a.done:
		case <-timer.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
	return true
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
