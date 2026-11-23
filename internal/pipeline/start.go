package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/devpad-org/devpad/internal/project"
)

// A pipeline is resolved when it starts: the commits it tests are those of
// its branch or merge request then, and its jobs those of the pipeline at
// the commit they are read from, the branch's or the merge request's
// target's. Its record holds the jobs as resolved, so it runs exactly those,
// whatever changes meanwhile, and two starts that would test the same thing
// are one pipeline.
//
// Everything a pipeline is resolved from is read under its project's start
// lock, which also covers finding the same pipeline and superseding others:
// a start that takes the lock after another reads commits and a setting at
// least as new, so it never supersedes a newer commit's pipeline with an
// older one's.

func (s *service) Start(ctx context.Context, in StartInput) (*Pipeline, bool, error) {
	return s.start(ctx, in, 0)
}

// start starts a pipeline, recording it as running again rerunOf when that
// is set.
func (s *service) start(ctx context.Context, in StartInput, rerunOf int64) (*Pipeline, bool, error) {
	if in.Trigger == "" {
		return nil, false, fmt.Errorf("%w: a pipeline needs a trigger", ErrInvalidInput)
	}
	// The project is the user's before anyone takes its lock.
	if _, err := s.projects.Get(ctx, in.UserID, in.ProjectID); err != nil {
		return nil, false, err
	}
	unlock, err := s.lockStarts(ctx, in.ProjectID)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	p, err := s.projects.Get(ctx, in.UserID, in.ProjectID)
	if err != nil {
		return nil, false, err
	}
	if p.Status != project.StatusReady {
		return nil, false, project.ErrNotReady
	}
	next, err := s.subjectOf(ctx, p, in)
	if err != nil {
		return nil, false, err
	}
	resolved, configSHA, err := s.resolveJobs(ctx, p, next)
	if err != nil {
		var none *NoProposal
		if in.StopStale && errors.As(err, &none) {
			// What the change proposed before, it no longer does.
			s.stopSubject(next.subject(), staleCause(none.Error()))
		}
		return nil, false, err
	}
	uid, err := newUID()
	if err != nil {
		return nil, false, err
	}
	next.UID, next.Trigger, next.RerunOf = uid, in.Trigger, rerunOf
	next.ConfigSource, next.ConfigPath, next.ConfigHash, next.ConfigSHA = resolved.Source, resolved.Path, resolved.Hash, configSHA
	next.CreatedAt = s.now()
	setJobs(next, resolved)

	same, err := s.reusable(ctx, next, in)
	if err != nil {
		return nil, false, err
	}
	// A run again never reuses a result from before the pipeline it runs
	// again, which was started despite it, as the user's Run again is.
	if same != nil && rerunOf != 0 && same.ID < rerunOf && same.Status.Finished() {
		same = nil
	}
	created := same == nil || s.stopping(same.UID)
	if created {
		if err := s.create(ctx, next); err != nil {
			return nil, false, err
		}
		same = next
	}
	s.supersede(same, in.StopStale)
	return same, created, nil
}

// reusable finds the pipeline a start of next returns rather than make
// one: one queued or running of the same commits and jobs; with
// ReuseResult, or Automatic, one that counts; and with Automatic one that
// ended without a result that devpad doesn't run again by itself
// (settled).
func (s *service) reusable(ctx context.Context, next *Pipeline, in StartInput) (*Pipeline, error) {
	statuses := unfinishedStatuses
	if in.ReuseResult || in.Automatic {
		statuses = countingStatuses
	}
	same, err := s.repo.FindSame(ctx, next, statuses)
	if err != nil || same != nil || !in.Automatic {
		return same, err
	}
	ended, retry, err := s.settled(ctx, next)
	if err != nil || retry {
		return nil, err
	}
	return ended, nil
}

// settled returns the newest pipeline testing what p tests that ended
// without a result (endedStatuses), or nil, and whether devpad is to run
// those commits and jobs once more by itself: never after the user
// cancelled them, and after an error only while the database holds no more
// than errorRetries of them.
func (s *service) settled(ctx context.Context, p *Pipeline) (ended *Pipeline, retry bool, err error) {
	ended, err = s.repo.FindSame(ctx, p, endedStatuses)
	if err != nil || ended == nil || ended.Status != StatusError {
		return ended, false, err
	}
	failed, err := s.repo.CountSame(ctx, p, []Status{StatusError})
	if err != nil {
		return nil, false, err
	}
	return ended, failed <= errorRetries, nil
}

// subjectOf reads what in asks to test: the pipeline it would be, with its
// subject, purpose and commits filled in.
func (s *service) subjectOf(ctx context.Context, p *project.Project, in StartInput) (*Pipeline, error) {
	purpose := in.Purpose
	if purpose == "" {
		purpose = PurposeOfficial
	}
	branch := strings.TrimSpace(in.Branch)
	var next *Pipeline
	var err error
	switch {
	case !slices.Contains(Purposes(), purpose):
		return nil, fmt.Errorf("%w: unknown pipeline purpose %q", ErrInvalidInput, purpose)
	case branch != "" && in.MergeRequestID != 0, branch == "" && in.MergeRequestID == 0:
		return nil, fmt.Errorf("%w: a pipeline tests a branch or a merge request", ErrInvalidInput)
	case branch != "" && purpose == PurposeProposed:
		return nil, fmt.Errorf("%w: only a merge request's change proposes a pipeline", ErrInvalidInput)
	case branch != "":
		next, err = s.branchSubject(ctx, p, branch)
	default:
		next, err = s.mergeRequestSubject(ctx, p, in.UserID, in.MergeRequestID)
	}
	if err != nil {
		return nil, err
	}
	next.Purpose = purpose
	return next, nil
}

// branchSubject is a pipeline of a protected branch's current commit, read
// with that commit's pipeline. An unprotected branch is its author's to
// change, an agent's as often as not, so its pipeline file proves nothing.
func (s *service) branchSubject(ctx context.Context, p *project.Project, branch string) (*Pipeline, error) {
	if !p.IsProtected(branch) {
		return nil, fmt.Errorf("%w: only protected branches run pipelines; %s is not protected", ErrInvalidInput, branch)
	}
	sha, err := s.branchCommit(ctx, p, branch)
	if err != nil {
		return nil, err
	}
	return &Pipeline{ProjectID: p.ID, UserID: p.UserID, Kind: KindBranch, Branch: branch, HeadSHA: sha}, nil
}

// mergeRequestSubject is a pipeline of an open merge request's head merged
// onto its target's current commit, read with the target commit's pipeline:
// the source is the change an agent wrote, so it never decides the jobs that
// grade it.
func (s *service) mergeRequestSubject(ctx context.Context, p *project.Project, userID, mrID int64) (*Pipeline, error) {
	mr, err := s.mrs.PipelineMergeRequest(ctx, userID, p.ID, mrID)
	if err != nil {
		return nil, err
	}
	if mr == nil {
		return nil, ErrMergeRequestNotFound
	}
	if !mr.Open {
		return nil, fmt.Errorf("%w: the merge request is no longer open", ErrInvalidState)
	}
	if !p.IsProtected(mr.TargetBranch) {
		return nil, fmt.Errorf("%w: the merge request's target %s is no longer protected", ErrInvalidState, mr.TargetBranch)
	}
	if !project.IsCommitHash(mr.HeadSHA) {
		return nil, fmt.Errorf("%w: the merge request has no head commit yet", ErrInvalidState)
	}
	target, err := s.branchCommit(ctx, p, mr.TargetBranch)
	if err != nil {
		return nil, err
	}
	return &Pipeline{
		ProjectID: p.ID, UserID: p.UserID, Kind: KindMergeRequest,
		MergeRequestID: mr.ID, SourceBranch: mr.SourceBranch, TargetBranch: mr.TargetBranch,
		HeadSHA: mr.HeadSHA, TargetSHA: target,
	}, nil
}

// branchCommit is the commit a branch of the canonical repo is at.
func (s *service) branchCommit(ctx context.Context, p *project.Project, branch string) (string, error) {
	refs, err := s.projects.Branches(ctx, p.UserID, p.ID)
	if err != nil {
		return "", err
	}
	for _, b := range refs.Branches {
		if b.Name == branch {
			if !project.IsCommitHash(b.SHA) {
				return "", fmt.Errorf("branch %s is at %q, which is not a commit hash", branch, b.SHA)
			}
			return b.SHA, nil
		}
	}
	return "", fmt.Errorf("%w: the project has no branch %s", ErrInvalidInput, branch)
}

// configCommit is the commit a pipeline's jobs are read at: the branch's
// own, or the merge request's target's.
func configCommit(p *Pipeline) string {
	if p.Kind == KindMergeRequest {
		return p.TargetSHA
	}
	return p.HeadSHA
}

// setJobs gives a pipeline its jobs as resolved, all queued, or makes it
// finished at once when there is nothing to run: a pipeline that does not
// parse, or none at all.
func setJobs(p *Pipeline, r *Resolved) {
	switch {
	case r.Invalid != nil:
		p.Status, p.Error, p.FinishedAt = StatusInvalid, r.Invalid.Error(), &p.CreatedAt
	case r.Config == nil:
		p.Status, p.FinishedAt = StatusNotConfigured, &p.CreatedAt
	default:
		p.Status = StatusQueued
		for i, job := range r.Config.Jobs {
			p.Jobs = append(p.Jobs, &JobRun{Job: job, Position: i, Status: JobQueued})
		}
	}
}

// supersede has current replace every queued pipeline of its subject that
// tests something else: the newer commits, or jobs, are what count. One that
// has started is left to finish, unless stale asks to stop those too. The
// caller holds the project's start lock.
func (s *service) supersede(current *Pipeline, stale bool) {
	k, cause := current.key(), supersededBy(current.ID)
	if slices.Contains(endedStatuses, current.Status) {
		// What replaces them is a pipeline that ended without a result,
		// which the commits and jobs as they are now keep (settled).
		cause = staleCause("what it tests is no longer current")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.runs {
		if a.subject == k.subject && a.key != k && (!a.started || stale) {
			a.stop(cause)
		}
	}
}

// stopSubject stops every pipeline of a subject, queued or running, for
// cause. The caller holds the project's start lock.
func (s *service) stopSubject(sub subject, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.runs {
		if a.subject == sub {
			a.stop(cause)
		}
	}
}

// stopping reports whether the pipeline uid names is being stopped, though
// it may not have recorded so yet, so that it is no longer the one testing
// what it tests.
func (s *service) stopping(uid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.runs[uid]
	return a != nil && a.stopping
}

// create records a new pipeline and, unless it finished at once, runs it in
// the background; one that did is announced. Neither happens while devpad
// stops or the project is being deleted.
func (s *service) create(ctx context.Context, p *Pipeline) error {
	if p.Status.Finished() {
		s.mu.Lock()
		err := s.admit(p.ProjectID)
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if err := s.repo.Create(ctx, p); err != nil {
			return err
		}
		s.announce(p.ID)
		return nil
	}
	a, err := s.reserve(p)
	if err != nil {
		return err
	}
	if err := s.repo.Create(ctx, p); err != nil {
		s.forget(a)
		return err
	}
	// The run owns its own copy, which no one else touches.
	run := p.clone()
	go func() {
		defer s.forget(a)
		s.run(a.ctx, a, run)
	}()
	return nil
}

// admit refuses a new pipeline of the project while devpad stops or the
// project is being deleted. The caller holds mu.
func (s *service) admit(projectID int64) error {
	switch {
	case s.closed:
		return ErrShuttingDown
	case s.deleting[projectID]:
		return project.ErrDeleting
	}
	return nil
}

// reserve makes the run of a queued pipeline whose record is yet to be
// made, so that what settles a devpad that stopped never takes the record
// for one it left. Shutdown waits for the run, and stopping the project's
// pipelines stops it, from then on.
func (s *service) reserve(p *Pipeline) (*activePipeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.admit(p.ProjectID); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(s.rootCtx)
	a := &activePipeline{
		uid: p.UID, subject: p.subject(), key: p.key(), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		automatic: p.Trigger.Automatic(), target: p.TargetBranch,
	}
	s.runs[p.UID] = a
	s.wg.Add(1)
	return a, nil
}

// forget drops a run that has ended, or never began.
func (s *service) forget(a *activePipeline) {
	s.mu.Lock()
	delete(s.runs, a.uid)
	s.mu.Unlock()
	a.cancel(nil)
	close(a.done)
	s.wg.Done()
}

// newUID makes a pipeline's UID: random, so that no other devpad sharing the
// Docker daemon makes the same.
func newUID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("making a pipeline id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
