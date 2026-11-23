package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/pipeline/jobrun"
)

// A pipeline runs in a goroutine of its own, which records how it went;
// cancelling, superseding or deleting its project only ends its context,
// with a cause saying why, and the run records what that means. Only what
// no run holds is finished otherwise: Cancel finishes an unfinished pipeline
// whose run ended without recording so, and Reconcile one a devpad that
// stopped left.
//
// It waits, still queued, for a build of the project's environment and then
// for a job slot. With one it is running, on the image new workspaces of
// the project get then: the repo container pins what it tests, devpad
// bundles that once into this devpad's bundle directory, and its jobs run,
// the first on the slot it holds and each other on a slot of its own, every
// one whatever the others do. Then the bundle and the pin go, and anything
// its jobs left in Docker, and only then is it recorded as finished, so that
// nothing removing what finished pipelines left can race a job still
// running.

// run runs a queued pipeline to its end.
func (s *service) run(ctx context.Context, a *activePipeline, p *Pipeline) {
	if !s.awaitEnvironment(ctx, p) {
		return
	}
	release, err := s.slots.acquire(ctx, claimOf(p, 0))
	if err != nil {
		s.stopped(ctx, p)
		return
	}
	started, err := s.begin(ctx, a, p)
	if !started {
		release()
		if err != nil {
			s.conclude(ctx, p, StatusError, err.Error())
		} else {
			s.stopped(ctx, p)
		}
		return
	}
	s.runStarted(ctx, p, release)
}

// awaitEnvironment waits for a build of the project's environment in
// progress, and for its services' images, as a new sandbox does. It reports
// false when the pipeline ended meanwhile.
func (s *service) awaitEnvironment(ctx context.Context, p *Pipeline) bool {
	_, err := s.projects.AwaitEnvironment(ctx, p.UserID, p.ProjectID)
	if ctx.Err() != nil {
		s.stopped(ctx, p)
		return false
	}
	if err != nil {
		s.conclude(ctx, p, StatusError, fmt.Sprintf("waiting for the project's environment: %v", err))
		return false
	}
	return true
}

// begin takes a pipeline that holds a job slot out of the queue, unless it
// was cancelled or superseded first, or has nothing left to test (stale),
// and records it as running on the image new workspaces of its project get
// now: read once the slot is held, since builds while it waited may have
// replaced, and pruned, the one it would have had. It reports false, with
// no error, when the pipeline ended first.
func (s *service) begin(ctx context.Context, a *activePipeline, p *Pipeline) (bool, error) {
	if why := s.stale(ctx, p); why != "" {
		s.mu.Lock()
		a.stop(why)
		s.mu.Unlock()
		return false, nil
	}
	image, err := s.image(ctx, p)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil
		}
		return false, err
	}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return false, nil
	}
	a.started = true
	s.mu.Unlock()
	started := s.now()
	ok, err := s.repo.MarkStarted(ctx, p.ID, image, started)
	if err != nil || !ok {
		return false, err
	}
	p.Status, p.Image, p.StartedAt = StatusRunning, image, &started
	return true, nil
}

// stale says why a pipeline about to start has nothing left to test, or
// is "": whoever started it, its merge request is no longer open, or its
// branch or its merge request's target is no longer protected; and what
// devpad started by itself no longer runs by itself, that branch no longer
// ticked. It reads the merge request as stored, taking no lock of the
// merge request service's, and when it cannot read something it lets the
// pipeline run.
func (s *service) stale(ctx context.Context, p *Pipeline) staleCause {
	branch := p.Branch
	if p.Kind == KindMergeRequest {
		mr, err := s.mrs.PipelineMergeRequest(ctx, p.UserID, p.ProjectID, p.MergeRequestID)
		if err != nil {
			return ""
		}
		if mr == nil || !mr.Open {
			return mergeRequestEnded(mr != nil && mr.Merged)
		}
		branch = mr.TargetBranch
	}
	proj, err := s.projects.Get(ctx, p.UserID, p.ProjectID)
	if err != nil {
		return ""
	}
	switch {
	case !proj.IsProtected(branch):
		return staleCause(branch + " is no longer protected")
	case p.Trigger.Automatic() && !branchRunsAutomatically(proj, branch):
		return noLongerAutomatic(branch)
	}
	return ""
}

// image is the image new workspaces of the pipeline's project get now.
func (s *service) image(ctx context.Context, p *Pipeline) (string, error) {
	env, err := s.projects.ProjectEnvironment(ctx, p.UserID, p.ProjectID)
	if err != nil {
		return "", fmt.Errorf("reading the project's environment: %w", err)
	}
	if env != nil && env.Image != "" {
		return env.Image, nil
	}
	return container.WorkspaceImage, nil
}

// runStarted runs a pipeline that holds its first job slot, which release
// frees.
func (s *service) runStarted(ctx context.Context, p *Pipeline, release func()) {
	bundle, outcome, reason := s.prepare(ctx, p)
	if outcome != "" {
		release()
		s.cleanUp(ctx, p, "")
		s.conclude(ctx, p, outcome, reason)
		return
	}
	s.runJobs(ctx, p, bundle, release)
	s.cleanUp(ctx, p, bundle.Path)
	s.conclude(ctx, p, jobsOutcome(p.Jobs), "")
}

// prepare pins what the pipeline tests and bundles it. When it cannot, it
// returns how the pipeline ends and why.
func (s *service) prepare(ctx context.Context, p *Pipeline) (jobrun.Bundle, Status, string) {
	pinned, err := s.pin(ctx, p)
	switch {
	case err != nil:
		return jobrun.Bundle{}, StatusError, fmt.Sprintf("pinning what the pipeline tests: %v", err)
	case pinned.Conflict != nil:
		// The client keeps the first agent.MaxConflictPaths files.
		p.ConflictPaths, p.ConflictMore = pinned.Conflict.Paths, pinned.Conflict.More
		reason := pinned.Conflict.Message
		if p.ConflictMore > 0 {
			reason += fmt.Sprintf(" (%d files conflict; the first %d are listed)", len(p.ConflictPaths)+p.ConflictMore, len(p.ConflictPaths))
		}
		return jobrun.Bundle{}, StatusConflict, reason
	}
	p.TestedSHA, p.TestedTree = pinned.SHA, pinned.Tree
	if err := s.repo.SetTested(ctx, p.ID, pinned.SHA, pinned.Tree); err != nil {
		return jobrun.Bundle{}, StatusError, err.Error()
	}
	// A proposed pipeline's jobs were read from the trial merge, and run
	// only on it.
	if p.Purpose == PurposeProposed && pinned.SHA != p.ConfigSHA {
		return jobrun.Bundle{}, StatusError, fmt.Sprintf("the trial merge pinned, %s, is not %s, the one the proposed jobs were read from", pinned.SHA, p.ConfigSHA)
	}
	bundle, err := s.bundle(ctx, p)
	if err != nil {
		if errors.Is(err, agent.ErrBundleTooLarge) {
			return jobrun.Bundle{}, StatusError, fmt.Sprintf("what the pipeline tests is too large to bundle: %v", err)
		}
		return jobrun.Bundle{}, StatusError, fmt.Sprintf("bundling what the pipeline tests: %v", err)
	}
	return bundle, "", ""
}

// pin has the repo container pin what the pipeline tests, recording first
// that its ref may exist, so that the ref is found and deleted whatever
// happens next. A source that does not merge comes back as the conflict.
func (s *service) pin(ctx context.Context, p *Pipeline) (*agent.PipelinePinResult, error) {
	if err := s.repo.SetRefPinned(ctx, p.ID, true); err != nil {
		return nil, err
	}
	p.RefPinned = true
	pin := agent.PipelinePin{ID: p.UID}
	if p.Kind == KindMergeRequest {
		pin.Source, pin.Target = p.HeadSHA, p.TargetSHA
	} else {
		pin.Commit = p.HeadSHA
	}
	return s.projects.PinPipeline(ctx, p.UserID, p.ProjectID, pin)
}

// bundle writes a bundle of what the pipeline tests to a file of its own,
// which every job's container is given a copy of.
func (s *service) bundle(ctx context.Context, p *Pipeline) (jobrun.Bundle, error) {
	if s.ownsBundleDir {
		// Made again, should something that clears caches have removed it.
		if err := os.MkdirAll(s.bundleDir, 0o700); err != nil {
			return jobrun.Bundle{}, fmt.Errorf("making the bundles' directory: %w", err)
		}
	}
	f, err := os.OpenFile(filepath.Join(s.bundleDir, bundleName(p.UID)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return jobrun.Bundle{}, fmt.Errorf("creating the bundle's file: %w", err)
	}
	size, err := s.projects.PipelineBundle(ctx, p.UserID, p.ProjectID, p.UID, f)
	if closeErr := f.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("writing the bundle's file: %w", closeErr)
	}
	if err != nil {
		os.Remove(f.Name())
		return jobrun.Bundle{}, err
	}
	return jobrun.Bundle{Path: f.Name(), Size: size}, nil
}

// runJobs runs every job of the pipeline at once, as slots allow, and waits
// for them all. The first runs on the slot the pipeline holds, which first
// frees.
func (s *service) runJobs(ctx context.Context, p *Pipeline, bundle jobrun.Bundle, first func()) {
	if len(p.Jobs) == 0 {
		first()
		return
	}
	var wg sync.WaitGroup
	for i, j := range p.Jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := first
			if i > 0 {
				var err error
				if release, err = s.slots.acquire(ctx, claimOf(p, j.Position)); err != nil {
					// Its pipeline ended first: it stays queued, and is
					// skipped when the pipeline finishes.
					return
				}
			}
			defer release()
			s.runJob(ctx, p, j, bundle)
		}()
	}
	wg.Wait()
}

// runJob runs one job and records how it ended. A job stopped because
// devpad is stopping keeps only what it told, and its status for Reconcile.
func (s *service) runJob(ctx context.Context, p *Pipeline, j *JobRun, bundle jobrun.Bundle) {
	if ctx.Err() != nil {
		return
	}
	live := newJobLog(MaxJobOutput, s.now)
	s.mu.Lock()
	s.live[j.ID] = live
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.live, j.ID)
		s.mu.Unlock()
	}()
	started := s.now()
	if err := s.repo.StartJob(ctx, j.ID, started); err != nil {
		log.Printf("pipeline %d job %s: recording that it started: %v", p.ID, j.Name, err)
	}
	j.Status, j.StartedAt = JobRunning, &started

	res := s.jobs.Run(ctx, jobrun.Spec{
		Job:       container.Job{Pipeline: p.UID, Name: j.Name},
		ProjectID: p.ProjectID,
		Image:     p.Image,
		Run:       j.Run,
		Timeout:   j.Timeout,
		Services:  j.Services,
		Bundle:    bundle,
	}, live)

	told := live.snapshot()
	recordCtx, cancel := s.recordContext(ctx)
	defer cancel()
	status, ok := jobStatus(res.Outcome, context.Cause(ctx))
	if !ok {
		if err := s.repo.SaveJobLog(recordCtx, j.ID, told); err != nil {
			log.Printf("pipeline %d job %s: keeping what it told: %v", p.ID, j.Name, err)
		}
		return
	}
	finished := s.now()
	j.Status, j.ExitCode, j.Error, j.FinishedAt = status, res.ExitCode, res.Error, &finished
	if status == JobCancelled {
		j.Error = stopReason(context.Cause(ctx))
	}
	if err := s.repo.FinishJob(recordCtx, j, told); err != nil {
		log.Printf("pipeline %d job %s: recording how it ended: %v", p.ID, j.Name, err)
		// Its record still has it running, which its pipeline's finish
		// records as this, so the pipeline's outcome counts it so too.
		j.Status, j.Error = JobError, lostJob
	}
}

// jobStatus is the status a job's outcome gives it, or false when the job
// was stopped because devpad is stopping, which Reconcile deals with.
func jobStatus(outcome jobrun.Outcome, cause error) (JobStatus, bool) {
	switch outcome {
	case jobrun.Passed:
		return JobPassed, true
	case jobrun.Failed:
		return JobFailed, true
	case jobrun.TimedOut:
		return JobTimedOut, true
	case jobrun.OutOfMemory:
		return JobOutOfMemory, true
	case jobrun.Stopped:
		if errors.Is(cause, errShutdown) {
			return "", false
		}
		return JobCancelled, true
	default:
		return JobError, true
	}
}

// jobsOutcome is how a pipeline whose jobs all ended went: failed if any
// job's code failed, else an error if any job could not run, else passed.
func jobsOutcome(jobs []*JobRun) Status {
	outcome := StatusPassed
	for _, j := range jobs {
		switch {
		case j.Status.Failed():
			return StatusFailed
		case j.Status != JobPassed:
			outcome = StatusError
		}
	}
	return outcome
}

// cleanUp removes what a started pipeline made: its bundle, its ref in the
// canonical repo, and anything its jobs left in Docker. It runs whatever
// ended the pipeline, on a context of its own.
func (s *service) cleanUp(ctx context.Context, p *Pipeline, bundlePath string) {
	cleanupCtx, cancel := s.recordContext(ctx)
	defer cancel()
	if bundlePath != "" {
		if err := os.Remove(bundlePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("pipeline %d: removing its bundle: %v", p.ID, err)
		}
	}
	s.unpin(cleanupCtx, p)
	if err := s.docker.RemoveJobResources(cleanupCtx, container.Job{Pipeline: p.UID}); err != nil {
		log.Printf("pipeline %d: removing what its jobs left: %v", p.ID, err)
	}
}

// unpin deletes the pipeline's ref in the canonical repo, if it may have
// one, and records that it has none. One that cannot be deleted now is left
// for the sweep.
func (s *service) unpin(ctx context.Context, p *Pipeline) {
	if !p.RefPinned {
		return
	}
	if err := s.projects.DeletePipelineRef(ctx, p.UserID, p.ProjectID, p.UID); err != nil {
		log.Printf("pipeline %d: deleting its ref: %v", p.ID, err)
		return
	}
	if err := s.repo.SetRefPinned(ctx, p.ID, false); err != nil {
		log.Printf("pipeline %d: recording its ref deleted: %v", p.ID, err)
		return
	}
	p.RefPinned = false
}

// conclude records how a pipeline ended: outcome, unless its context ended
// first, whose cause then decides. A pipeline stopped because devpad is
// stopping is left as it stands, for Reconcile.
func (s *service) conclude(ctx context.Context, p *Pipeline, outcome Status, reason string) {
	if cause := context.Cause(ctx); ctx.Err() != nil {
		status, ok := stoppedStatus(cause)
		if !ok {
			return
		}
		outcome, reason = status, stopReason(cause)
	}
	recordCtx, cancel := s.recordContext(ctx)
	defer cancel()
	s.finish(recordCtx, p, outcome, reason)
}

// stopped records how a pipeline that had not started ended, by why its
// context ended.
func (s *service) stopped(ctx context.Context, p *Pipeline) {
	s.conclude(ctx, p, StatusCancelled, "")
}

// stoppedStatus is the status of a pipeline stopped for cause, or false
// when devpad is stopping and the pipeline is left for Reconcile.
func stoppedStatus(cause error) (Status, bool) {
	switch {
	case errors.Is(cause, errShutdown):
		return "", false
	case errors.Is(cause, errSuperseded):
		return StatusSuperseded, true
	default:
		return StatusCancelled, true
	}
}

// stopReason is what a stopped pipeline or job records as why, beyond its
// status: nothing when the user cancelled it, and for one superseded the
// pipeline that replaced it.
func stopReason(cause error) string {
	if errors.Is(cause, errCancelled) {
		return ""
	}
	return cause.Error()
}

// recordContext is a context for recording how something ended, which
// outlives ctx, since its ending is often what is recorded.
func (s *service) recordContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}
