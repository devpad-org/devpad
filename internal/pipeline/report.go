package pipeline

import (
	"context"
	"fmt"
)

// What a finished pipeline found, for those who act on it: a task's agent,
// told what failed, and the reviewer of its merge request, told how the
// jobs went. A merge request's failure counts against its change only for
// the jobs that don't fail on the target already, which the target
// branch's own pipeline of the same commit tells.

// TargetResult is how the target branch's own pipeline went on the commit a
// merge request's pipeline merged its branch onto, with the same jobs. It
// is the one comparison that tells a job failing because of the change
// from one failing without it: a pipeline of another target commit could
// excuse a regression the change brings, or blame the change for a breakage
// that landed since.
type TargetResult struct {
	// Branch is the target branch.
	Branch string
	// Pipeline is the newest of those pipelines with a result, or nil when
	// none has one: none ran, or none could.
	Pipeline *Pipeline
	// Pending says that, with none of them having a result, one is queued
	// or running, so whether a job fails there too is still to be known.
	// Devpad runs a ticked branch's commits by itself, so the target of a
	// merge request a pipeline guards gets one.
	Pending bool
}

// Fails reports whether job failed in the target's pipeline: its code
// failed there too.
func (t *TargetResult) Fails(job string) bool {
	if t == nil || t.Pipeline == nil {
		return false
	}
	for _, j := range t.Pipeline.Jobs {
		if j.Name == job && j.Status.Failed() {
			return true
		}
	}
	return false
}

// Failing names the jobs whose code failed in the target's pipeline.
func (t *TargetResult) Failing() []string {
	if t == nil || t.Pipeline == nil {
		return nil
	}
	return failedJobs(t.Pipeline)
}

// targetResult is the TargetResult of merge request pipeline p, or nil
// for a branch's pipeline, or one that never merged onto a target commit.
// It is Pending while a pipeline of the target's commit with p's jobs is
// queued or running, or while an automatic run of the target is still to
// start one: the target's run can be held back (autoMinInterval, a
// project's runs going one at a time) when a merge request's pipeline
// fails fast. Callers ask of a pipeline of the target as it is now, whose
// commit such a run would test. With await, a decision waits on that run:
// one that starts nothing tells the BranchRunListeners, so the decision is
// made then.
func (s *service) targetResult(ctx context.Context, p *Pipeline, await bool) (*TargetResult, error) {
	if p.Kind != KindMergeRequest || p.TargetSHA == "" || p.TargetBranch == "" {
		return nil, nil
	}
	target := branchKey(p.ProjectID, p.TargetBranch, p.TargetSHA, p.ConfigHash)
	done, err := s.repo.FindSame(ctx, target, resultStatuses)
	if err != nil {
		return nil, fmt.Errorf("reading %s's pipeline of %s: %w", p.TargetBranch, p.TargetSHA, err)
	}
	if done != nil {
		return &TargetResult{Branch: p.TargetBranch, Pipeline: done}, nil
	}
	running, err := s.repo.FindSame(ctx, target, unfinishedStatuses)
	if err != nil {
		return nil, fmt.Errorf("reading %s's pipeline of %s: %w", p.TargetBranch, p.TargetSHA, err)
	}
	key := autoKey{projectID: p.ProjectID, kind: autoBranch, branch: p.TargetBranch}
	queued := s.auto.has(key)
	if await {
		queued = s.auto.await(key)
	}
	return &TargetResult{Branch: p.TargetBranch, Pending: running != nil || queued}, nil
}

// Report is what a pipeline found.
type Report struct {
	// Pipeline is the pipeline with its jobs and their scripts.
	Pipeline *Pipeline
	// Logs are, by job id, the end of each failed job's output, as much as
	// was asked for, and devpad's notes on it.
	Logs map[int64]*JobLog
	// Target is how the target's own pipeline went on the same commit, for
	// a merge request's pipeline; nil otherwise.
	Target *TargetResult
}

// Report reads what a pipeline of the user's project found: its jobs, the
// last outputTail bytes of each failed job's output with devpad's notes on
// it (none when outputTail is 0), and for a merge request's pipeline how
// the target's own pipeline went on the commit it merged onto, awaiting the
// target's queued automatic run when that is still to say (targetResult).
func (s *service) Report(ctx context.Context, userID, projectID, pipelineID int64, outputTail int) (*Report, error) {
	p, err := s.get(ctx, userID, projectID, pipelineID)
	if err != nil {
		return nil, err
	}
	r := &Report{Pipeline: p, Logs: make(map[int64]*JobLog)}
	if outputTail > 0 {
		for _, j := range p.Jobs {
			if !j.Status.Failed() {
				continue
			}
			_, told, err := s.repo.JobTail(ctx, p.ID, j.ID, outputTail)
			if err != nil {
				return nil, err
			}
			if told != nil {
				r.Logs[j.ID] = told
			}
		}
	}
	if r.Target, err = s.targetResult(ctx, p, true); err != nil {
		return nil, err
	}
	return r, nil
}

// BranchPipeline resolves the pipeline a merge request into a protected
// branch of the user's project would run now: the pipeline file at the
// branch's tip, else the project's setting.
func (s *service) BranchPipeline(ctx context.Context, userID, projectID int64, branch string) (*Resolved, error) {
	p, err := s.projects.Get(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if !p.IsProtected(branch) {
		return nil, fmt.Errorf("%w: %s is not a protected branch", ErrInvalidInput, branch)
	}
	tip, err := s.branchCommit(ctx, p, branch)
	if err != nil {
		return nil, err
	}
	return s.resolveCached(ctx, p, tip)
}
