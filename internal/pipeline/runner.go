package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/pipeline/jobrun"
)

// A pipeline runs in a goroutine of its own, the only one that records its
// status once it is queued; cancelling or superseding it only ends its
// context, with a cause saying why, and the run records what that means.
//
// It waits, still queued, for a build of the project's environment and then
// for a job slot. With one it is running: the repo container pins what it
// tests, devpad bundles that once, and its jobs run, the first on the slot
// it holds and each other on a slot of its own, every one whatever the
// others do. Then the bundle and the pin go, and anything its jobs left in
// Docker, and only then is it recorded as finished, so that nothing removing
// what finished pipelines left can race a job still running.

// run runs a queued pipeline to its end.
func (s *service) run(ctx context.Context, a *activePipeline, p *Pipeline) {
	image, ok := s.awaitEnvironment(ctx, p)
	if !ok {
		return
	}
	release, err := s.slots.acquire(ctx, p.ProjectID, p.ID, 0)
	if err != nil {
		s.stopped(ctx, p)
		return
	}
	started, err := s.begin(ctx, a, p, image)
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
// progress, as a new sandbox does, and returns the image the jobs run on.
// It reports false when the pipeline ended meanwhile.
func (s *service) awaitEnvironment(ctx context.Context, p *Pipeline) (string, bool) {
	env, err := s.projects.AwaitEnvironment(ctx, p.UserID, p.ProjectID)
	if ctx.Err() != nil {
		s.stopped(ctx, p)
		return "", false
	}
	if err != nil {
		s.conclude(ctx, p, StatusError, fmt.Sprintf("waiting for the project's environment: %v", err))
		return "", false
	}
	if env != nil && env.Image != "" {
		return env.Image, true
	}
	return container.WorkspaceImage, true
}

// begin takes a pipeline out of the queue, unless it was cancelled or
// superseded first, and records it as running on image. It reports false,
// with no error, when the pipeline ended first.
func (s *service) begin(ctx context.Context, a *activePipeline, p *Pipeline, image string) (bool, error) {
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
	tested, conflict, err := s.pin(ctx, p)
	switch {
	case err != nil:
		return jobrun.Bundle{}, StatusError, fmt.Sprintf("pinning what the pipeline tests: %v", err)
	case conflict != nil:
		p.ConflictPaths = conflict.Paths
		return jobrun.Bundle{}, StatusConflict, conflict.Message
	}
	p.TestedSHA = tested
	if err := s.repo.SetTested(ctx, p.ID, tested); err != nil {
		return jobrun.Bundle{}, StatusError, err.Error()
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
func (s *service) pin(ctx context.Context, p *Pipeline) (string, *agent.MergeConflict, error) {
	if err := s.repo.SetRefPinned(ctx, p.ID, true); err != nil {
		return "", nil, err
	}
	p.RefPinned = true
	pin := agent.PipelinePin{ID: p.UID}
	if p.Kind == KindMergeRequest {
		pin.Source, pin.Target = p.HeadSHA, p.TargetSHA
	} else {
		pin.Commit = p.HeadSHA
	}
	res, err := s.projects.PinPipeline(ctx, p.UserID, p.ProjectID, pin)
	if err != nil {
		return "", nil, err
	}
	return res.SHA, res.Conflict, nil
}

// bundle writes a bundle of what the pipeline tests to a file of its own,
// which every job's container is given a copy of.
func (s *service) bundle(ctx context.Context, p *Pipeline) (jobrun.Bundle, error) {
	f, err := os.CreateTemp(s.bundleDir, bundlePattern)
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
				if release, err = s.slots.acquire(ctx, p.ProjectID, p.ID, j.Position); err != nil {
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
// devpad is stopping keeps only its output, and its status for Reconcile.
func (s *service) runJob(ctx context.Context, p *Pipeline, j *JobRun, bundle jobrun.Bundle) {
	if ctx.Err() != nil {
		return
	}
	out := newOutputBuffer(MaxJobOutput)
	s.mu.Lock()
	s.live[j.ID] = out
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
	}, out)

	kept, total := out.snapshot()
	recordCtx, cancel := s.recordContext(ctx)
	defer cancel()
	status, ok := jobStatus(res.Outcome, context.Cause(ctx))
	if !ok {
		if err := s.repo.SaveJobOutput(recordCtx, j.ID, kept, total); err != nil {
			log.Printf("pipeline %d job %s: keeping its output: %v", p.ID, j.Name, err)
		}
		return
	}
	finished := s.now()
	j.Status, j.ExitCode, j.Error, j.OutputBytes, j.FinishedAt = status, res.ExitCode, res.Error, total, &finished
	if status == JobCancelled {
		j.Error = stopReason(context.Cause(ctx))
	}
	if err := s.repo.FinishJob(recordCtx, j, kept); err != nil {
		log.Printf("pipeline %d job %s: recording how it ended: %v", p.ID, j.Name, err)
	}
}

// jobStatus is the status a job's outcome gives it, or false when the job
// was stopped because devpad is stopping, which Reconcile settles.
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
		case j.Status.failed():
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
