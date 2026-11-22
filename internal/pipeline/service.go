package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/pipeline/jobrun"
	"github.com/devpad-org/devpad/internal/project"
)

// Service runs a project's pipelines and keeps their record.
type Service interface {
	// Start queues a pipeline of a protected branch's current commit, or of
	// an open merge request's head merged onto its target's current commit.
	// A pipeline already queued or running that tests the same commits with
	// the same jobs is returned instead, with created false. Any pipeline of
	// the same branch or merge request still queued that tests something
	// else is superseded.
	Start(ctx context.Context, in StartInput) (p *Pipeline, created bool, err error)
	// Cancel stops a queued or running pipeline, and waits a while for its
	// jobs to be stopped and removed.
	Cancel(ctx context.Context, userID, projectID, pipelineID int64) (*Pipeline, error)
	// List lists a project's pipelines, newest first, with their jobs but
	// not their scripts.
	List(ctx context.Context, userID, projectID int64, f ListFilter) ([]*Pipeline, error)
	// Get returns a pipeline of the project with its jobs and their scripts.
	Get(ctx context.Context, userID, projectID, pipelineID int64) (*Pipeline, error)
	// JobOutput reads a job's output from offset on: live while the job
	// runs, and what is kept of it after.
	JobOutput(ctx context.Context, userID, projectID, pipelineID, jobID, offset int64) (*Output, error)
	// LatestForMergeRequest returns the newest pipeline of the merge request
	// that tested head merged onto target, or nil when none has.
	LatestForMergeRequest(ctx context.Context, userID, projectID, mergeRequestID int64, head, target string) (*Pipeline, error)
	// AddListener adds one of those told each time a pipeline finishes.
	AddListener(l Listener)
	// ProjectDeleting stops the project's pipelines before the project goes.
	project.DeleteHook
	// Reconcile settles the pipelines a previous devpad left unfinished and
	// runs them again, and removes what finished pipelines left behind.
	Reconcile(ctx context.Context) error
	// Shutdown stops every pipeline, leaving them to Reconcile, and waits
	// for their jobs to be removed.
	Shutdown(ctx context.Context) error
}

// StartInput says what a pipeline tests: Branch, a protected branch's
// current commit, or MergeRequestID, an open merge request's head merged
// onto its target's current commit.
type StartInput struct {
	UserID    int64
	ProjectID int64
	Branch    string
	// MergeRequestID is the merge request, when Branch is empty.
	MergeRequestID int64
	Trigger        Trigger
	// ReuseResult returns the newest pipeline that tested the same commits
	// with the same jobs and has a result (Status.HasResult), rather than
	// running them again.
	ReuseResult bool
}

// Listener is told each time a pipeline finishes, however it ended. It is
// told from a goroutine of its own, never from inside a call to the
// Service, so it may call the Service, or anything that does.
type Listener interface {
	PipelineFinished(ctx context.Context, p *Pipeline)
}

// Projects is what pipelines need of the project service: the project, its
// canonical repo, and its environment.
type Projects interface {
	RepoReader
	Get(ctx context.Context, userID, projectID int64) (*project.Project, error)
	Branches(ctx context.Context, userID, projectID int64) (*agent.RepoRefs, error)
	// AwaitEnvironment waits for a build of the project's environment in
	// progress, and pulls its services' images.
	AwaitEnvironment(ctx context.Context, userID, projectID int64) (*project.Environment, error)
	PinPipeline(ctx context.Context, userID, projectID int64, pin agent.PipelinePin) (*agent.PipelinePinResult, error)
	PipelineBundle(ctx context.Context, userID, projectID int64, pipelineID string, w io.Writer) (int64, error)
	DeletePipelineRef(ctx context.Context, userID, projectID int64, pipelineID string) error
}

// MergeRequests looks up the merge requests pipelines test.
type MergeRequests interface {
	// PipelineMergeRequest returns the user's merge request of the
	// project, or nil when the project has no such merge request of the
	// user's.
	PipelineMergeRequest(ctx context.Context, userID, projectID, mergeRequestID int64) (*MergeRequest, error)
}

// MergeRequest is what a pipeline needs to know of a merge request.
type MergeRequest struct {
	ID           int64
	SourceBranch string
	TargetBranch string
	// HeadSHA is the source commit the merge request proposes.
	HeadSHA string
	// Open says it can still be merged.
	Open bool
}

// JobRunner runs one job in a container of its own; jobrun.Runner is one.
type JobRunner interface {
	Run(ctx context.Context, spec jobrun.Spec, out io.Writer) jobrun.Result
}

// JobResources lists and removes what pipelines' jobs made in Docker;
// container.JobRuntime is one.
type JobResources interface {
	ListJobResources(ctx context.Context, sel container.Job) ([]container.JobResource, error)
	RemoveJobResources(ctx context.Context, sel container.Job) error
}

// Deps are what a pipeline Service is made from.
type Deps struct {
	Repo          Repository
	Projects      Projects
	MergeRequests MergeRequests
	Jobs          JobRunner
	Docker        JobResources
	// BundleDir holds each running pipeline's bundle. It is to be this
	// devpad's own and the same across its restarts: Reconcile removes the
	// bundles a devpad that stopped left there. Empty means the system's
	// temporary directory, which may be shared, so nothing is removed.
	BundleDir string
}

const (
	// cancelWait bounds how long Cancel, and a project's deletion, wait for
	// a pipeline's jobs to be stopped and removed.
	cancelWait = 30 * time.Second
	// cleanupTimeout bounds the steps that finish a pipeline, which run
	// even when its context has ended.
	cleanupTimeout = container.JobCleanupTimeout
	// sweepInterval is how often devpad removes what finished pipelines
	// left behind.
	sweepInterval = 10 * time.Minute
)

// Why a pipeline's context ends.
var (
	errCancelled      = errors.New("cancelled")
	errSuperseded     = errors.New("replaced by a pipeline of newer commits")
	errShutdown       = errors.New("devpad is stopping")
	errProjectDeleted = errors.New("the project is being deleted")
)

type service struct {
	repo     Repository
	projects Projects
	mrs      MergeRequests
	jobs     JobRunner
	docker   JobResources
	// bundleDir holds the bundles, and ownsBundleDir says whether the
	// directory is this devpad's alone.
	bundleDir     string
	ownsBundleDir bool
	slots         *jobSlots
	now           func() time.Time

	cancelWait    time.Duration
	sweepInterval time.Duration

	rootCtx    context.Context
	rootCancel context.CancelCauseFunc
	wg         sync.WaitGroup

	mu     sync.Mutex
	closed bool
	// active holds each pipeline this process is running or has queued.
	active map[int64]*activePipeline
	// live holds the output of each job running now.
	live map[int64]*outputBuffer
	// startLocks serialize starting each project's pipelines.
	startLocks map[int64]*sync.Mutex
	listeners  []Listener
}

// activePipeline is a pipeline this process is running or has queued.
type activePipeline struct {
	id      int64
	subject subject
	key     key
	cancel  context.CancelCauseFunc
	done    chan struct{}
	// started says it has left the queue, so it can no longer be
	// superseded; stopping that it has been told to stop.
	started  bool
	stopping bool
}

// stop ends the pipeline's context with cause, which its run records the
// meaning of. The caller holds the service's mu.
func (a *activePipeline) stop(cause error) {
	a.stopping = true
	a.cancel(cause)
}

// NewService creates a pipeline Service. Background work stops on Shutdown.
func NewService(d Deps) Service {
	ctx, cancel := context.WithCancelCause(context.Background())
	bundleDir := d.BundleDir
	if bundleDir == "" {
		bundleDir = os.TempDir()
	}
	return &service{
		repo:          d.Repo,
		projects:      d.Projects,
		mrs:           d.MergeRequests,
		jobs:          d.Jobs,
		docker:        d.Docker,
		bundleDir:     bundleDir,
		ownsBundleDir: d.BundleDir != "",
		slots:         newJobSlots(MaxRunningJobs),
		now:           func() time.Time { return time.Now().UTC() },
		cancelWait:    cancelWait,
		sweepInterval: sweepInterval,
		rootCtx:       ctx,
		rootCancel:    cancel,
		active:        make(map[int64]*activePipeline),
		live:          make(map[int64]*outputBuffer),
		startLocks:    make(map[int64]*sync.Mutex),
	}
}

func (s *service) AddListener(l Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, l)
}

// spawn runs fn in the background unless the service is shutting down.
func (s *service) spawn(fn func(ctx context.Context)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn(s.rootCtx)
	}()
	return true
}

// lockStarts serializes starting the project's pipelines.
func (s *service) lockStarts(projectID int64) func() {
	s.mu.Lock()
	l, ok := s.startLocks[projectID]
	if !ok {
		l = &sync.Mutex{}
		s.startLocks[projectID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// get returns a pipeline of the user's project, with its jobs.
func (s *service) get(ctx context.Context, userID, projectID, pipelineID int64) (*Pipeline, error) {
	if _, err := s.projects.Get(ctx, userID, projectID); err != nil {
		return nil, err
	}
	p, err := s.repo.Get(ctx, pipelineID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.ProjectID != projectID || p.UserID != userID {
		return nil, ErrNotFound
	}
	return p, nil
}

func (s *service) Get(ctx context.Context, userID, projectID, pipelineID int64) (*Pipeline, error) {
	return s.get(ctx, userID, projectID, pipelineID)
}

func (s *service) List(ctx context.Context, userID, projectID int64, f ListFilter) ([]*Pipeline, error) {
	if _, err := s.projects.Get(ctx, userID, projectID); err != nil {
		return nil, err
	}
	if f.Limit < 0 || f.Before < 0 || f.MergeRequestID < 0 {
		return nil, fmt.Errorf("%w: limit, before and mergeRequestId must not be negative", ErrInvalidInput)
	}
	return s.repo.List(ctx, projectID, f)
}

func (s *service) LatestForMergeRequest(ctx context.Context, userID, projectID, mergeRequestID int64, head, target string) (*Pipeline, error) {
	if _, err := s.projects.Get(ctx, userID, projectID); err != nil {
		return nil, err
	}
	p, err := s.repo.LatestForMergeRequest(ctx, mergeRequestID, head, target)
	if err != nil || p == nil || p.ProjectID != projectID || p.UserID != userID {
		return nil, err
	}
	return p, nil
}

func (s *service) JobOutput(ctx context.Context, userID, projectID, pipelineID, jobID, offset int64) (*Output, error) {
	p, err := s.get(ctx, userID, projectID, pipelineID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	buf := s.live[jobID]
	s.mu.Unlock()
	// A job's live buffer is recorded before it leaves the map, so a job
	// not in it has either not started or has all its output stored.
	if buf != nil && jobOf(p, jobID) {
		kept, total := buf.snapshot()
		return readOutput(kept, total, offset, false), nil
	}
	j, output, err := s.repo.JobOutput(ctx, p.ID, jobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrJobNotFound
	}
	return readOutput(output, j.OutputBytes, offset, j.Status.Finished()), nil
}

// jobOf reports whether a job is one of p's.
func jobOf(p *Pipeline, jobID int64) bool {
	for _, j := range p.Jobs {
		if j.ID == jobID {
			return true
		}
	}
	return false
}

func (s *service) Cancel(ctx context.Context, userID, projectID, pipelineID int64) (*Pipeline, error) {
	p, err := s.get(ctx, userID, projectID, pipelineID)
	if err != nil {
		return nil, err
	}
	if p.Status.Finished() {
		return nil, fmt.Errorf("%w: it has already %s", ErrInvalidState, finishedWord(p.Status))
	}
	s.mu.Lock()
	a := s.active[p.ID]
	if a != nil {
		a.stop(errCancelled)
	}
	s.mu.Unlock()
	if a == nil {
		// Unfinished but run by no one here: it ended as it was read, or
		// devpad is stopping. Finish records nothing over an end.
		s.finish(ctx, p, StatusCancelled, "")
		return s.get(ctx, userID, projectID, pipelineID)
	}
	s.awaitEnd(ctx, a)
	return s.get(ctx, userID, projectID, pipelineID)
}

// awaitEnd waits a while for an active pipeline to end.
func (s *service) awaitEnd(ctx context.Context, a *activePipeline) {
	timer := time.NewTimer(s.cancelWait)
	defer timer.Stop()
	select {
	case <-a.done:
	case <-timer.C:
		log.Printf("pipeline %d: still stopping after %s", a.id, s.cancelWait)
	case <-ctx.Done():
	}
}

func finishedWord(s Status) string {
	switch s {
	case StatusCancelled:
		return "been cancelled"
	case StatusSuperseded:
		return "been replaced by a newer pipeline"
	case StatusInterrupted:
		return "been interrupted"
	default:
		return "finished"
	}
}

// finish records how a pipeline ended, unless it already had, and tells
// the listeners.
func (s *service) finish(ctx context.Context, p *Pipeline, status Status, reason string) {
	finished := s.now()
	p.Status, p.Error, p.FinishedAt = status, reason, &finished
	ok, err := s.repo.Finish(ctx, p)
	if err != nil {
		log.Printf("pipeline %d: recording that it %s: %v", p.ID, status, err)
		return
	}
	if !ok {
		return
	}
	if err := s.repo.PruneOutputs(ctx, p.ProjectID, KeptOutputs); err != nil {
		log.Printf("project %d: pruning old pipeline output: %v", p.ProjectID, err)
	}
	s.announce(p.ID)
}

// announce tells the listeners that a pipeline finished, from a goroutine
// of its own, with the pipeline as recorded.
func (s *service) announce(pipelineID int64) {
	s.mu.Lock()
	listeners := append([]Listener(nil), s.listeners...)
	s.mu.Unlock()
	if len(listeners) == 0 {
		return
	}
	s.spawn(func(ctx context.Context) {
		for _, l := range listeners {
			// Each listener gets its own copy, so none sees what another
			// changes.
			p, err := s.repo.Get(ctx, pipelineID)
			if err != nil || p == nil {
				if err != nil && ctx.Err() == nil {
					log.Printf("pipeline %d: reading it to announce it finished: %v", pipelineID, err)
				}
				return
			}
			l.PipelineFinished(ctx, p)
		}
	})
}
