package pipeline

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"github.com/devpad-org/devpad/internal/project"
)

// Automatic runs. Devpad runs a ticked branch's pipeline (one of the
// project's PipelineBranches) on each of its new commits and when the user
// ticks it, and the pipeline of each open merge request into a ticked
// branch when it opens, when its head moves and when its target does;
// changing the project's pipeline setting runs again whatever used it, and
// as devpad starts it catches up on what changed while it was not running.
// Merge requests into other protected branches, and those branches, run
// only when the user starts them.
//
// The project and the merge requests tell of each change through the
// listeners they own (project.BranchListener and project.SettingsListener,
// which the Service is, and the merge request service's head listener,
// which server.go adapts to MergeRequestMoved). Each only queues a run, so
// it may be told under any of its caller's locks. A few workers make the
// runs outside every lock but the start lock Start takes: each reads the
// project and the merge request as they are then, asks runsAutomatically,
// and starts the pipeline with Automatic, so a run of what already has a
// pipeline for its commits and jobs makes none. A merge request's start
// stops its pipelines that test something else (StopStale); a branch's
// replaces only one still queued, and lets one running finish, so the
// branch's history shows each result.
//
// Nothing runs in reaction to a pipeline ending, so no pipeline ever starts
// another. What can start pipelines through a workspace an agent works in,
// a task's round or git push devpad, only moves a merge request's head,
// which gitbridge and the task's rounds pace: per key at most one run is
// read at a time with one more waiting, each merge request start leaves one
// pipeline of it running, and a project runs at most MaxRunningJobs jobs.

const (
	// autoWorkers bounds the workers making automatic runs, across every
	// project.
	autoWorkers = 4
	// maxAutoPending bounds the automatic runs waiting for a worker. Past
	// it a run is dropped, to be caught up on when devpad next starts.
	maxAutoPending = 4096
	// autoStartTimeout bounds one automatic start, which reads the
	// project's repo and waits for the project's other starts.
	autoStartTimeout = 2 * time.Minute
)

// MergeRequestMove is a merge request opening, or its head moving to new
// commits.
type MergeRequestMove struct {
	UserID         int64
	ProjectID      int64
	MergeRequestID int64
	// Opened says it has just opened; otherwise its head moved.
	Opened bool
}

// runsAutomatically is the one rule for what devpad runs by itself, read
// from the project as it is now: the pipeline of a protected branch ticked
// to run pipelines (Project.RunsPipelines), and of an open merge request
// into one. mr, when set, is the merge request whose pipeline it would be;
// otherwise branch is the branch's.
func runsAutomatically(p *project.Project, branch string, mr *MergeRequest) bool {
	if p == nil || p.Status != project.StatusReady {
		return false
	}
	if mr != nil {
		return mr.Open && p.RunsPipelines(mr.TargetBranch)
	}
	return p.RunsPipelines(branch)
}

// BranchMoved queues the runs of a protected branch that a merge or a
// fast-forward from the upstream moved: its own, and those of the open
// merge requests into it but the one that merged.
func (s *service) BranchMoved(_ context.Context, m project.BranchMove) {
	var trigger Trigger
	switch m.Cause {
	case project.MoveMerged:
		trigger = TriggerMerged
	case project.MoveFastForwarded:
		trigger = TriggerUpstream
	default:
		// A published branch is never protected; the merge request from
		// it, which follows it, tells of its own head moving.
		return
	}
	var except []string
	if m.Source != "" {
		except = []string{m.Source}
	}
	s.queueBranch(m.UserID, m.ProjectID, m.Branch, trigger, except)
}

// ProjectSettingsChanged queues the runs of each branch the user just
// ticked to run pipelines, and when the pipeline setting changed, of every
// ticked branch: each with the open merge requests into it. Those whose
// jobs do not come from the setting resolve to the same jobs as before,
// so only what used the setting runs again.
func (s *service) ProjectSettingsChanged(_ context.Context, before, after *project.Project) {
	if before == nil || after == nil {
		return
	}
	for _, b := range after.PipelineBranches {
		if after.RunsPipelines(b) && !before.RunsPipelines(b) {
			s.queueBranch(after.UserID, after.ID, b, TriggerTicked, nil)
		}
	}
	if before.PipelineConfig == after.PipelineConfig {
		return
	}
	for _, b := range after.PipelineBranches {
		if after.RunsPipelines(b) {
			s.queueBranch(after.UserID, after.ID, b, TriggerSetting, nil)
		}
	}
}

// MergeRequestMoved queues the run of a merge request that opened or whose
// head moved.
func (s *service) MergeRequestMoved(_ context.Context, m MergeRequestMove) {
	trigger := TriggerSourceMoved
	if m.Opened {
		trigger = TriggerOpened
	}
	s.queueAuto(&autoRun{key: mergeRequestKey(m.ProjectID, m.MergeRequestID), userID: m.UserID, trigger: trigger})
}

func mergeRequestKey(projectID, mergeRequestID int64) autoKey {
	return autoKey{projectID: projectID, kind: autoMergeRequest, mergeRequestID: mergeRequestID}
}

// queueBranch queues the runs of a branch: its own, for trigger, and those
// of the open merge requests into it but those from except.
func (s *service) queueBranch(userID, projectID int64, branch string, trigger Trigger, except []string) {
	s.queueAuto(&autoRun{key: autoKey{projectID: projectID, kind: autoBranch, branch: branch}, userID: userID, trigger: trigger})
	s.queueAuto(&autoRun{key: autoKey{projectID: projectID, kind: autoInto, branch: branch}, userID: userID, trigger: intoTrigger(trigger), except: except})
}

// intoTrigger is what starts the pipelines of the merge requests into a
// branch when trigger starts the branch's: its moving, for them their
// target moving.
func intoTrigger(trigger Trigger) Trigger {
	if trigger == TriggerMerged || trigger == TriggerUpstream {
		return TriggerTargetMoved
	}
	return trigger
}

// queueAuto queues an automatic run, starting a worker when one is needed.
func (s *service) queueAuto(r *autoRun) {
	startWorker, dropped := s.auto.add(r)
	if dropped {
		log.Printf("project %d: too many automatic pipeline runs waiting; dropping one, which devpad catches up on when it next starts", r.key.projectID)
	}
	if startWorker {
		s.startAutoWorker()
	}
}

// beginAutomatic lets automatic runs start, those that waited first. It
// comes once Reconcile has settled what the last devpad left, so that no
// automatic start is taken for one of those.
func (s *service) beginAutomatic() {
	for n := s.auto.begin(); n > 0; n-- {
		s.startAutoWorker()
	}
}

func (s *service) startAutoWorker() {
	if !s.spawn(s.autoWorker) {
		s.auto.quit()
	}
}

// autoWorker makes the queued automatic runs one at a time until none is
// left or devpad stops.
func (s *service) autoWorker(ctx context.Context) {
	for {
		r, ok := s.auto.next(ctx.Err() != nil)
		if !ok {
			return
		}
		s.makeAutoRun(ctx, r)
		s.auto.done(r.key)
	}
}

// makeAutoRun reads what r is of as it is now and, when runsAutomatically
// says so, starts its pipeline, or for an autoInto run queues the runs of
// the merge requests into the branch.
func (s *service) makeAutoRun(ctx context.Context, r *autoRun) {
	ctx, cancel := context.WithTimeout(ctx, autoStartTimeout)
	defer cancel()
	p, err := s.projects.Get(ctx, r.userID, r.key.projectID)
	if err != nil {
		s.logAutoRefusal(ctx, r, err)
		return
	}
	switch r.key.kind {
	case autoBranch:
		if runsAutomatically(p, r.key.branch, nil) {
			s.startAutomatic(ctx, r, StartInput{UserID: p.UserID, ProjectID: p.ID, Branch: r.key.branch, Trigger: r.trigger, Automatic: true})
		}
	case autoMergeRequest:
		mr, err := s.mrs.PipelineMergeRequest(ctx, p.UserID, p.ID, r.key.mergeRequestID)
		if err != nil {
			s.logAutoRefusal(ctx, r, err)
			return
		}
		if mr != nil && runsAutomatically(p, "", mr) {
			s.startAutomatic(ctx, r, StartInput{UserID: p.UserID, ProjectID: p.ID, MergeRequestID: mr.ID, Trigger: r.trigger, Automatic: true, StopStale: true})
		}
	case autoInto:
		if !runsAutomatically(p, r.key.branch, nil) {
			return
		}
		mrs, err := s.mrs.PipelineMergeRequestsInto(ctx, p.UserID, p.ID, r.key.branch)
		if err != nil {
			s.logAutoRefusal(ctx, r, err)
			return
		}
		for _, mr := range mrs {
			if !slices.Contains(r.except, mr.SourceBranch) {
				s.queueAuto(&autoRun{key: mergeRequestKey(p.ID, mr.ID), userID: p.UserID, trigger: r.trigger})
			}
		}
	}
}

// startAutomatic starts an automatic run's pipeline.
func (s *service) startAutomatic(ctx context.Context, r *autoRun, in StartInput) {
	pl, created, err := s.Start(ctx, in)
	switch {
	case err != nil:
		s.logAutoRefusal(ctx, r, err)
	case created:
		log.Printf("pipeline %d: started by itself (%s) for project %d", pl.ID, in.Trigger, in.ProjectID)
	}
}

// logAutoRefusal logs why an automatic run made nothing, unless that is
// how things stand rather than something going wrong: the merge request
// closed or its target is no longer protected, the project is gone, not
// ready or being deleted, or devpad is stopping.
func (s *service) logAutoRefusal(ctx context.Context, r *autoRun, err error) {
	switch {
	case ctx.Err() != nil,
		errors.Is(err, ErrInvalidState),
		errors.Is(err, ErrMergeRequestNotFound),
		errors.Is(err, ErrShuttingDown),
		errors.Is(err, project.ErrNotFound),
		errors.Is(err, project.ErrForbidden),
		errors.Is(err, project.ErrNotReady),
		errors.Is(err, project.ErrDeleting):
		return
	}
	log.Printf("project %d: automatic pipeline run (%s): %v", r.key.projectID, r.trigger, err)
}

// catchUp queues the runs of every ticked branch, each with the open merge
// requests into it, so that what changed while devpad was not running, of
// which no listener was told, runs now. Each makes a pipeline only where
// the commits and jobs as they are have none.
func (s *service) catchUp(ctx context.Context) {
	projects, err := s.projects.ListPipelineProjects(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("pipelines: listing the projects to catch up on: %v", err)
		}
		return
	}
	for _, p := range projects {
		for _, b := range p.PipelineBranches {
			if p.RunsPipelines(b) {
				s.queueBranch(p.UserID, p.ID, b, TriggerCatchUp, nil)
			}
		}
	}
}
