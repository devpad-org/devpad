package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
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
	// the same jobs is returned instead, with created false, as, for a
	// restart's or devpad's own start, is one with a result
	// (startPolicyOf). Any pipeline of the same branch or merge request and
	// purpose still queued that tests something else is superseded. It reads the merge request and the
	// canonical repo, so it is never to be called inside a database
	// transaction, and the merge request lookup it makes (MergeRequests)
	// takes no lock a caller of Start may hold.
	Start(ctx context.Context, in StartInput) (p *Pipeline, created bool, err error)
	// Cancel stops a queued or running pipeline, and waits a while for its
	// jobs to be stopped and removed. It refuses while devpad stops, which
	// leaves every pipeline for Reconcile.
	Cancel(ctx context.Context, userID, projectID, pipelineID int64) (*Pipeline, error)
	// List lists a project's pipelines, newest first, with their jobs but
	// not their scripts.
	List(ctx context.Context, userID, projectID int64, f ListFilter) ([]*Pipeline, error)
	// Latest returns the newest official pipeline that counts
	// (countingStatuses) of each branch and merge request of the project f
	// names that has one, with their jobs but not their scripts, and the
	// commit each branch named is at now: what a branch's or a merge
	// request's pipeline badge shows, and whether a branch's is of its
	// current commit. f's Purpose is PurposeOfficial or empty, which means
	// the same; a badge never shows what a change proposes.
	Latest(ctx context.Context, userID, projectID int64, f LatestFilter) (*LatestPipelines, error)
	// Get returns a pipeline of the project with its jobs and their scripts.
	Get(ctx context.Context, userID, projectID, pipelineID int64) (*Pipeline, error)
	// JobOutput reads a job's output from an offset on, with devpad's notes
	// on the job: live while the job runs, and what is kept of them after.
	JobOutput(ctx context.Context, userID, projectID, pipelineID int64, req OutputRequest) (*Output, error)
	// MergeRequestStatus says how an open merge request stands for
	// pipelines now: the key a pipeline must test to count (its head on its
	// target's current tip, with the jobs resolved there now), whether any
	// pipeline is configured, and the pipeline that counts for that key.
	// It is the one answer to whether a merge request's result is current,
	// for its page and for the merge gate.
	MergeRequestStatus(ctx context.Context, userID, projectID, mergeRequestID int64) (*MergeRequestStatus, error)
	// ProposedStatus says how the pipeline an open merge request's change
	// proposes stands (proposed.go), at the commits its MergeRequestStatus
	// found, or nil when its change leaves the pipeline file alone. It reads
	// the change, so it is never part of MergeRequestStatus, which the merge
	// gate reads on every merge.
	ProposedStatus(ctx context.Context, userID, projectID int64, official *MergeRequestStatus) (*ProposedStatus, error)
	// Report reads what a pipeline found, for the agent sent back with its
	// failures and the reviewer told its results: its jobs, the last
	// outputTail bytes of each failed job's output with devpad's notes on it,
	// and, for a merge request's pipeline, how the target's own pipeline went
	// on the commit it merged onto (TargetResult). It also does one thing
	// besides reading: while the target's result is Pending on an automatic
	// run of the target still queued, it marks that run awaited, so a run
	// that then starts no pipeline tells the BranchRunListeners. It takes
	// only leaf locks, so it may be called under a merge request's lock.
	Report(ctx context.Context, userID, projectID, pipelineID int64, outputTail int) (*Report, error)
	// BranchPipeline resolves the pipeline a merge request into a protected
	// branch would run now, at the branch's tip, for a task's agent to run
	// its jobs before it finishes.
	BranchPipeline(ctx context.Context, userID, projectID int64, branch string) (*Resolved, error)
	// AddListener adds a Listener, told each time a pipeline finishes.
	// Listeners are to be added before Reconcile, which tells them of the
	// pipelines that finished without every listener being told, such as
	// when devpad stopped first. A listener is told of each pipeline at
	// least once but maybe more often, and of a subject's pipelines in any
	// order, so it re-reads what it acts on, such as MergeRequestStatus for
	// a merge request's current key, rather than trusting the pipeline it is
	// told of to be the latest. It is never told on a context that
	// has ended.
	AddListener(l Listener)
	// BranchMoved, ProjectSettingsChanged and MergeRequestMoved are told
	// what changed, by the project and, through server.go, the merge
	// request service, so that devpad runs the pipelines of ticked branches
	// and of the open merge requests into them by itself (auto.go). Each
	// only queues the run and returns at once, so it may be told under the
	// caller's locks. MergeRequestEnded stops the pipelines of a merge
	// request that closed or merged; it only ends their contexts, so it may
	// be told under the merge request's lock.
	project.BranchListener
	project.SettingsListener
	MergeRequestMoved(ctx context.Context, m MergeRequestMove)
	MergeRequestEnded(ctx context.Context, m MergeRequestEnd)
	// ProjectDeleting stops the project's pipelines before the project goes,
	// and starts none until ProjectDeleteEnded.
	project.DeleteHook
	// Reconcile finishes the pipelines a previous devpad left unfinished and
	// runs them again, tells the listeners what they were not told, and
	// removes what finished pipelines left behind. It never touches a
	// pipeline this process runs, but it is to come before anything that
	// starts pipelines, so that each is run once. Automatic runs start only
	// after it has run those again, with a catch-up on every ticked branch
	// and merge request into one.
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
	// Purpose is what the pipeline is for: PurposeOfficial when empty, or
	// PurposeProposed for a merge request's, which runs the jobs its change
	// proposes and is refused when it proposes none.
	Purpose Purpose
	// Trigger is what starts it, one of Triggers, which also decides what
	// the start reuses rather than run again and what it stops
	// (startPolicyOf): the user's Run again always runs.
	Trigger Trigger
}

// OutputRequest asks for a pipeline's job's output from Offset on: 0, or the
// Next of the last read.
type OutputRequest struct {
	JobID  int64
	Offset int64
}

// Listener is told each time a pipeline finishes, however it ended
// (Service.AddListener says how often and in what order). It is told from a
// goroutine of its own, never from inside a call to the Service, so it may
// call the Service, or anything that does, and it is to return once it has
// acted, or when ctx ends.
type Listener interface {
	PipelineFinished(ctx context.Context, p *Pipeline)
}

// BranchRunListener is what a Listener may also be, to hear that an
// automatic run of a protected branch, which a merge request's result
// waited on to tell what fails on the branch too (Report's TargetResult
// was Pending only for that run), started no pipeline, such as when the
// branch's commit and jobs had one that was cancelled or couldn't run:
// that result is to be decided again. Only a run something waited on is
// told, so devpad's periodic catch-up tells nobody.
type BranchRunListener interface {
	BranchRunStartedNone(ctx context.Context, userID, projectID int64, branch string)
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
	// ProjectEnvironment is how the project's environment stands now: the
	// image new workspaces get, or nil when it has none.
	ProjectEnvironment(ctx context.Context, userID, projectID int64) (*project.Environment, error)
	PinPipeline(ctx context.Context, userID, projectID int64, pin agent.PipelinePin) (*agent.PipelinePinResult, error)
	// Diff lists the files head changed since it diverged from base, and
	// TrialMerge reads the commit a pin of a source onto a target tests,
	// for the pipeline a merge request's change proposes.
	Diff(ctx context.Context, userID, projectID int64, base, head string) (*agent.RepoDiff, error)
	TrialMerge(ctx context.Context, userID, projectID int64, source, target string) (*agent.TrialMerge, error)
	PipelineBundle(ctx context.Context, userID, projectID int64, pipelineID string, w io.Writer) (int64, error)
	DeletePipelineRef(ctx context.Context, userID, projectID int64, pipelineID string) error
	// ListPipelineProjects lists every user's ready projects that tick a
	// branch to run pipelines, for the catch-up as devpad starts.
	ListPipelineProjects(ctx context.Context) ([]*project.Project, error)
}

// MergeRequests looks up the merge requests pipelines test.
type MergeRequests interface {
	// PipelineMergeRequest returns the user's merge request of the
	// project, or nil when the project has no such merge request of the
	// user's.
	PipelineMergeRequest(ctx context.Context, userID, projectID, mergeRequestID int64) (*MergeRequest, error)
	// PipelineMergeRequestsInto returns the open merge requests of the
	// user's project into a branch, whose pipelines run again when it
	// moves.
	PipelineMergeRequestsInto(ctx context.Context, userID, projectID int64, target string) ([]*MergeRequest, error)
}

// MergeRequest is what a pipeline needs to know of a merge request.
type MergeRequest struct {
	ID           int64
	SourceBranch string
	TargetBranch string
	// HeadSHA is the source commit the merge request proposes.
	HeadSHA string
	// Open says it can still be merged; Merged that it was.
	Open   bool
	Merged bool
}

// JobRunner runs one job in a container of its own; jobrun.Runner is one.
type JobRunner interface {
	Run(ctx context.Context, spec jobrun.Spec, rec jobrun.Recorder) jobrun.Result
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

// Why a pipeline's context ends. A pipeline superseded ends with
// supersededBy, which names the pipeline that replaced it.
var (
	errCancelled      = errors.New("cancelled")
	errSuperseded     = errors.New("replaced")
	errShutdown       = errors.New("devpad is stopping")
	errProjectDeleted = errors.New("the project is being deleted")
)

func supersededBy(pipelineID int64) error {
	return fmt.Errorf("%w by pipeline %d", errSuperseded, pipelineID)
}

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
	resolved      resolveCache
	proposals     proposalCache
	// auto holds the automatic runs waiting for a worker (auto.go), and
	// autoStartTimeout bounds each.
	auto             *autoQueue
	autoStartTimeout time.Duration

	cancelWait    time.Duration
	sweepInterval time.Duration

	rootCtx    context.Context
	rootCancel context.CancelCauseFunc
	wg         sync.WaitGroup

	mu     sync.Mutex
	closed bool
	// runs holds each pipeline this process has queued or is running, by
	// its UID, from before its record is made until its run has ended.
	// Nothing that cleans up what a devpad that stopped left, interrupting a
	// pipeline or removing what it made, touches one of these.
	runs map[string]*activePipeline
	// deleting holds the projects being deleted, whose pipelines no longer
	// start.
	deleting map[int64]bool
	// live holds what each job running now has told.
	live map[int64]*jobLog
	// startLocks serialize starting each project's pipelines, one per
	// project: a channel of one slot, held by the start that filled it.
	startLocks map[int64]chan struct{}
	listeners  []Listener
}

// activePipeline is a pipeline this process is running or has queued.
type activePipeline struct {
	uid     string
	subject subject
	key     key
	// ctx is its run's, which cancel ends with a cause saying why, and done
	// closes once the run has ended.
	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}
	// started says it has left the queue, so it can no longer be
	// superseded but by a start that stops stale ones; stopping that it
	// has been told to stop.
	started  bool
	stopping bool
	// automatic says devpad started it by itself, and target is its merge
	// request's target, so that a branch no longer ticked stops it.
	automatic bool
	target    string
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
	s := &service{
		repo:             d.Repo,
		projects:         d.Projects,
		mrs:              d.MergeRequests,
		jobs:             d.Jobs,
		docker:           d.Docker,
		bundleDir:        bundleDir,
		ownsBundleDir:    d.BundleDir != "",
		slots:            newJobSlots(MaxRunningJobs),
		auto:             newAutoQueue(autoWorkers, maxAutoPending, autoMinInterval),
		autoStartTimeout: autoStartTimeout,
		now:              func() time.Time { return time.Now().UTC() },
		cancelWait:       cancelWait,
		sweepInterval:    sweepInterval,
		rootCtx:          ctx,
		rootCancel:       cancel,
		runs:             make(map[string]*activePipeline),
		deleting:         make(map[int64]bool),
		live:             make(map[int64]*jobLog),
		startLocks:       make(map[int64]chan struct{}),
	}
	s.auto.startWorker = s.startAutoWorker
	return s
}

func (s *service) AddListener(l Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, l)
}

// hasRun reports whether this process has queued or is running the pipeline
// uid names.
func (s *service) hasRun(uid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[uid] != nil
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

// lockStarts takes the project's start lock, which serializes starting its
// pipelines, and returns what frees it. A start holds it while it reads the
// repo, so one waiting for it gives up when ctx ends; another project's
// starts never wait for it.
func (s *service) lockStarts(ctx context.Context, projectID int64) (func(), error) {
	s.mu.Lock()
	l, ok := s.startLocks[projectID]
	if !ok {
		l = make(chan struct{}, 1)
		s.startLocks[projectID] = l
	}
	s.mu.Unlock()
	select {
	case l <- struct{}{}:
		return func() { <-l }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for the project's other pipeline starts: %w", context.Cause(ctx))
	}
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

// LatestPipelines is what Latest found.
type LatestPipelines struct {
	Pipelines []*Pipeline
	// BranchTips are the commits the branches asked for are at now, by
	// name, of those the canonical repo has. Nil when it could not be read,
	// such as while its container is down: the pipelines still count, but
	// whether a branch's is of its current commit is not known.
	BranchTips map[string]string
	// Branches says how each branch asked for stands now, by name: whether
	// it runs automatically, and, when it can be read, its pipeline that
	// ended without a result.
	Branches map[string]*BranchStanding
}

// BranchStanding is how a branch stands for pipelines now.
type BranchStanding struct {
	// Automatic says devpad runs its pipelines by itself.
	Automatic bool
	// Configured says whether a pipeline is configured at its current
	// commit, a file there or the project's setting, one that doesn't parse
	// included: with Automatic, a pipeline guards the merge requests into
	// it (Gate). Nil when its commit or pipeline could not be read.
	Configured *bool
	// NoResult is, when no pipeline of its current commit and jobs counts,
	// the newest that ended without a result on them, or nil.
	NoResult *NoResult
}

func (s *service) Latest(ctx context.Context, userID, projectID int64, f LatestFilter) (*LatestPipelines, error) {
	p, err := s.projects.Get(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if len(f.Branches)+len(f.MergeRequestIDs) > MaxLatestSubjects {
		return nil, fmt.Errorf("%w: at most %d branches and merge requests at once", ErrInvalidInput, MaxLatestSubjects)
	}
	for _, id := range f.MergeRequestIDs {
		if id <= 0 {
			return nil, fmt.Errorf("%w: invalid merge request id %d", ErrInvalidInput, id)
		}
	}
	// Badges show the branches' and merge requests' own results, never
	// what a change proposes.
	if f.Purpose != "" && f.Purpose != PurposeOfficial {
		return nil, fmt.Errorf("%w: badges show only official pipelines, not %s ones", ErrInvalidInput, f.Purpose)
	}
	f.Purpose = PurposeOfficial
	found, err := s.repo.Latest(ctx, projectID, f)
	if err != nil {
		return nil, err
	}
	out := &LatestPipelines{Pipelines: found}
	if len(f.Branches) == 0 {
		return out, nil
	}
	out.Branches = make(map[string]*BranchStanding, len(f.Branches))
	for _, b := range f.Branches {
		out.Branches[b] = &BranchStanding{Automatic: branchRunsAutomatically(p, b)}
	}
	refs, err := s.projects.Branches(ctx, p.UserID, p.ID)
	if err != nil {
		log.Printf("pipeline: reading project %d's branches for their latest pipelines: %v", p.ID, err)
		return out, nil
	}
	out.BranchTips = make(map[string]string, len(f.Branches))
	for _, b := range refs.Branches {
		if slices.Contains(f.Branches, b.Name) {
			out.BranchTips[b.Name] = b.SHA
			s.fillBranchStanding(ctx, p, b.Name, b.SHA, out.Branches[b.Name])
		}
	}
	return out, nil
}

// fillBranchStanding fills in whether a pipeline is configured for a branch at
// tip and its NoResult, unless its pipeline at tip cannot be read,
// which only leaves the badge without them.
func (s *service) fillBranchStanding(ctx context.Context, p *project.Project, branch, tip string, st *BranchStanding) {
	if !project.IsCommitHash(tip) || !p.IsProtected(branch) {
		return
	}
	resolved, err := s.resolveCached(ctx, p, tip)
	if err != nil {
		log.Printf("pipeline: project %d's %s for its badge: %v", p.ID, branch, err)
		return
	}
	configured := resolved.Source != SourceNone
	st.Configured = &configured
	if st.NoResult, err = s.noResultOf(ctx, branchKey(p.ID, branch, tip, resolved.Hash), st.Automatic); err != nil {
		log.Printf("pipeline: project %d's %s for its badge: %v", p.ID, branch, err)
	}
}

// current is the newest pipeline of k that counts (countingStatuses): one
// queued or running, or one finished with a result, which a later one
// cancelled, superseded or interrupted never hides. It is nil when there is
// none. A start that reuses results returns this same pipeline once it has
// finished, so the merge gate and reuse never disagree.
func (s *service) current(ctx context.Context, k key) (*Pipeline, error) {
	return s.repo.FindSame(ctx, k, countingStatuses)
}

func (s *service) JobOutput(ctx context.Context, userID, projectID, pipelineID int64, req OutputRequest) (*Output, error) {
	p, err := s.get(ctx, userID, projectID, pipelineID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	live := s.live[req.JobID]
	s.mu.Unlock()
	// A job's live log is recorded before it leaves the map, so a job not
	// in it has either not started or has all it told stored.
	if live != nil && jobOf(p, req.JobID) {
		return readOutput(live.snapshot(), req.Offset, false), nil
	}
	j, told, err := s.repo.JobLog(ctx, p.ID, req.JobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrJobNotFound
	}
	return readOutput(*told, req.Offset, j.Status.Finished()), nil
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
	if s.closed {
		s.mu.Unlock()
		return nil, ErrShuttingDown
	}
	a := s.runs[p.UID]
	if a != nil {
		a.stop(errCancelled)
	}
	s.mu.Unlock()
	if a == nil {
		// Unfinished, but no run of this process holds it: it ended as it
		// was read, or its run could not record how. Finish records nothing
		// over an end.
		s.finish(ctx, p, StatusCancelled, "")
		return s.get(ctx, userID, projectID, pipelineID)
	}
	s.awaitEnd(ctx, a, p.ID)
	return s.get(ctx, userID, projectID, pipelineID)
}

// awaitEnd waits a while for the run of a pipeline to end.
func (s *service) awaitEnd(ctx context.Context, a *activePipeline, pipelineID int64) {
	if !s.awaitRuns(ctx, []*activePipeline{a}) && ctx.Err() == nil {
		log.Printf("pipeline %d: still stopping after %s", pipelineID, s.cancelWait)
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

// announce tells the listeners that pipelines finished, from a goroutine of
// its own, each pipeline as recorded, and records each announced once they
// have all been told. What it does not get to, as when devpad stops first,
// the next Reconcile announces.
func (s *service) announce(pipelineIDs ...int64) {
	if len(pipelineIDs) == 0 {
		return
	}
	s.spawn(func(ctx context.Context) {
		s.mu.Lock()
		listeners := slices.Clone(s.listeners)
		s.mu.Unlock()
		for _, id := range pipelineIDs {
			if !s.tell(ctx, listeners, id) {
				return
			}
		}
	})
}

// tell tells each listener that a pipeline finished, then records it
// announced. It reports false when ctx ended first, which leaves the
// pipeline unannounced.
func (s *service) tell(ctx context.Context, listeners []Listener, pipelineID int64) bool {
	for _, l := range listeners {
		if ctx.Err() != nil {
			return false
		}
		// Each listener gets its own copy, so none sees what another
		// changes.
		p, err := s.repo.Get(ctx, pipelineID)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			log.Printf("pipeline %d: reading it to announce it finished: %v", pipelineID, err)
			return true
		}
		if p == nil {
			return true
		}
		l.PipelineFinished(ctx, p)
	}
	if ctx.Err() != nil {
		return false
	}
	if err := s.repo.MarkAnnounced(ctx, pipelineID, s.now()); err != nil && ctx.Err() == nil {
		log.Printf("pipeline %d: recording it announced: %v", pipelineID, err)
	}
	return true
}
