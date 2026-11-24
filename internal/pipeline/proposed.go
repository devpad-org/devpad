package pipeline

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/project"
)

// A merge request whose change adds, changes, renames or deletes the
// pipeline file proposes other jobs than its target's: once it merges,
// they are what every later merge request into the target has to pass.
// Devpad runs them too, for information: a proposed pipeline
// (PurposeProposed) of the merge request, whose jobs come from the pipeline
// file of the trial merge of its head onto its target, never from the
// project's setting, and run on that same commit. It runs whenever the
// merge request's official pipeline would, by itself into a ticked branch
// (makeAutoRun) and by hand otherwise, and is replaced and stopped as those
// are, but by its own kind only: its purpose is part of its subject, so no
// query or rule that finds the merge request's official pipelines ever
// finds it. A change that removes the pipeline file proposes no jobs, one
// that doesn't merge none that can be read, and one that proposes the very
// jobs the official pipeline runs nothing new, so nothing runs for any of
// them.
//
// The jobs are the change's, so they never cost the results that count
// more than they must: each runs at most as long as the target's longest
// job (DefaultTimeout when the target has none), and proposed jobs take
// job slots only after official ones and at most one at a time
// (jobSlots).

// NoProposal is why a merge request's change runs no proposed pipeline: it
// leaves the pipeline file alone, removes it, proposes the official
// pipeline's own jobs, or doesn't merge onto its target. It is an
// ErrInvalidState.
type NoProposal struct{ reason string }

func (e *NoProposal) Error() string { return e.reason }
func (e *NoProposal) Unwrap() error { return ErrInvalidState }

var (
	// ErrPipelineUnchanged refuses a proposed pipeline of a merge request
	// whose change leaves the pipeline file alone: it proposes nothing.
	ErrPipelineUnchanged error = &NoProposal{"the change leaves the pipeline file alone, so it proposes no pipeline"}
	// ErrPipelineRemoved refuses a proposed pipeline of a merge request
	// whose change removes the pipeline file: it proposes no jobs to run.
	ErrPipelineRemoved error = &NoProposal{"this change removes the pipeline, so it proposes no jobs to run"}
	// ErrSameJobs refuses a proposed pipeline of a merge request whose
	// change proposes the very jobs its official pipeline runs, such as by
	// editing only a comment of the file: they would run twice.
	ErrSameJobs error = &NoProposal{"the change proposes the very jobs the merge request's own pipeline runs, so they run there alone"}
)

// ProposedStatus is how the pipeline a merge request's change proposes
// stands now. Nothing reads it as a result of the merge request.
type ProposedStatus struct {
	// File is the pipeline file the change adds, changes, renames or
	// deletes.
	File string
	// Key is what a proposed pipeline must test to count now: the merge
	// request's head merged onto its target's tip, with the jobs of that
	// trial merge's pipeline file.
	Key MergeRequestKey
	// Automatic says devpad runs it by itself, as it does the merge
	// request's official pipeline.
	Automatic bool
	// Conflict says the change doesn't merge onto the target, so the file
	// it proposes can't be read; Removed that the trial merge has no
	// pipeline file, so the change proposes none; SameJobs that its jobs
	// are the official pipeline's own. Nothing runs for any of them.
	Conflict bool
	Removed  bool
	SameJobs bool
	// ConfigPath is the file the proposed jobs come from, and Invalid why
	// it doesn't parse, or empty.
	ConfigPath string
	Invalid    string
	// Standing is how the merge request's proposed pipelines stand for
	// Key, as MergeRequestStatus's official ones do.
	Standing
}

// proposal is what a merge request's change proposes for its pipeline when
// its head merges onto a target commit: the pipeline file it touches, or ""
// for none, and when it does, the trial merge and the pipeline found
// there. A change devpad can't diff, such as one with no history in common
// with its target, has only the trial merge, which says it conflicts. It
// depends only on the two commits, which never change.
type proposal struct {
	file     string
	merge    *agent.TrialMerge
	resolved *Resolved
}

func (pr *proposal) conflicts() bool {
	return pr.merge != nil && pr.merge.Conflict != nil
}

// refusal is why no proposed pipeline of the proposal runs, before its
// jobs are weighed against the official pipeline's, or nil.
func (pr *proposal) refusal(target string) error {
	switch {
	case pr.conflicts():
		return &NoProposal{fmt.Sprintf("the change doesn't merge onto %s, so the pipeline it proposes can't be read", target)}
	case pr.file == "":
		return ErrPipelineUnchanged
	case pr.resolved.Source == SourceNone:
		return ErrPipelineRemoved
	}
	return nil
}

// proposedJobs is what a proposed pipeline of pr runs beside the official
// pipeline whose jobs official resolved at the target, or why none runs.
// Each job runs at most as long as official's longest, or DefaultTimeout
// when official has no jobs, so a change can't hold a job slot longer than
// the jobs that count do; and jobs that, so limited, are official's own
// run once, in the official pipeline. A proposed file that doesn't parse
// is as it is.
func proposedJobs(pr *proposal, official *Resolved, target string) (*Resolved, error) {
	if err := pr.refusal(target); err != nil {
		return nil, err
	}
	r := pr.resolved
	if r.Config == nil {
		return r, nil
	}
	limit := DefaultTimeout
	if official.Config != nil && len(official.Config.Jobs) > 0 {
		limit = slices.MaxFunc(official.Config.Jobs, func(a, b Job) int { return cmp.Compare(a.Timeout, b.Timeout) }).Timeout
	}
	if slices.ContainsFunc(r.Config.Jobs, func(j Job) bool { return j.Timeout > limit }) {
		jobs := slices.Clone(r.Config.Jobs)
		for i := range jobs {
			jobs[i].Timeout = min(jobs[i].Timeout, limit)
		}
		r = &Resolved{Source: r.Source, Path: r.Path, Config: &Config{Jobs: jobs}, Hash: JobsHash(jobs)}
	}
	if official.Config != nil && official.Hash == r.Hash {
		return nil, ErrSameJobs
	}
	return r, nil
}

// proposalCache holds the proposals read, by project and commits, and for
// proposalRetry each failure to read one, so a merge request polled for
// its status doesn't ask the repo again and again for what it just
// couldn't answer.
type proposalCache struct {
	mu     sync.Mutex
	found  map[proposalKey]*proposal
	failed map[proposalKey]proposalFailure
}

type proposalFailure struct {
	err   error
	until time.Time
}

// proposalRetry is how long a failure to read a proposal stands before
// devpad asks the repo again.
const proposalRetry = 30 * time.Second

// recentFailure is a failure to read a proposal that devpad met within
// proposalRetry and was told of then.
type recentFailure struct{ error }

func (e recentFailure) Unwrap() error { return e.error }

// isRecentFailure reports whether err only repeats a failure devpad was
// already told of.
func isRecentFailure(err error) bool {
	var r recentFailure
	return errors.As(err, &r)
}

type proposalKey struct {
	projectID    int64
	head, target string
}

// maxProposals bounds the cache; past it, it starts again empty.
const maxProposals = 512

// proposal reads what a merge request's head proposes for its pipeline
// onto target: whether its change (merge base to head) touches the
// pipeline file, and if so the pipeline file at their trial merge. Both
// are full commit hashes.
func (s *service) proposal(ctx context.Context, p *project.Project, head, target string) (*proposal, error) {
	key := proposalKey{projectID: p.ID, head: head, target: target}
	c := &s.proposals
	now := s.now()
	c.mu.Lock()
	pr, ok := c.found[key]
	failed, recent := c.failed[key]
	c.mu.Unlock()
	switch {
	case ok:
		return pr, nil
	case recent && now.Before(failed.until):
		return nil, recentFailure{failed.err}
	}
	pr, err := s.readProposal(ctx, p, head, target)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			if c.failed == nil || len(c.failed) >= maxProposals {
				c.failed = make(map[proposalKey]proposalFailure)
			}
			c.failed[key] = proposalFailure{err: err, until: now.Add(proposalRetry)}
		}
		return nil, err
	}
	if c.found == nil || len(c.found) >= maxProposals {
		c.found = make(map[proposalKey]*proposal)
	}
	c.found[key] = pr
	delete(c.failed, key)
	return pr, nil
}

func (s *service) readProposal(ctx context.Context, p *project.Project, head, target string) (*proposal, error) {
	diff, err := s.projects.Diff(ctx, p.UserID, p.ID, target, head)
	if err != nil {
		// A change devpad can't diff may share no history with its
		// target, which the trial merge says is a conflict: then nothing
		// can be proposed, for good.
		merge, mergeErr := s.projects.TrialMerge(ctx, p.UserID, p.ID, head, target)
		if mergeErr == nil && merge.Conflict != nil {
			return &proposal{merge: merge}, nil
		}
		return nil, fmt.Errorf("reading what the change touches: %w", err)
	}
	pr := &proposal{file: project.ChangedPipelineFile(diff.Files)}
	if pr.file == "" {
		return pr, nil
	}
	if pr.merge, err = s.projects.TrialMerge(ctx, p.UserID, p.ID, head, target); err != nil {
		return nil, fmt.Errorf("merging the change onto %s to read the pipeline it proposes: %w", target, err)
	}
	if pr.merge.Conflict != nil {
		return pr, nil
	}
	if pr.resolved, err = ResolveProposed(ctx, s.projects, p, pr.merge.SHA); err != nil {
		return nil, err
	}
	return pr, nil
}

// resolveJobs resolves the jobs a pipeline about to be made runs, and the
// commit they were read from: an official one's are those Resolve finds at
// its branch's commit or its merge request's target's, never the
// source's; a proposed one's those its change proposes at the trial merge
// (proposedJobs), or why none run.
func (s *service) resolveJobs(ctx context.Context, p *project.Project, next *Pipeline) (*Resolved, string, error) {
	if next.Purpose != PurposeProposed {
		commit := configCommit(next)
		r, err := Resolve(ctx, s.projects, p, commit)
		return r, commit, err
	}
	pr, err := s.proposal(ctx, p, next.HeadSHA, next.TargetSHA)
	if err != nil {
		return nil, "", err
	}
	if err := pr.refusal(next.TargetBranch); err != nil {
		return nil, "", err
	}
	official, err := s.resolveCached(ctx, p, next.TargetSHA)
	if err != nil {
		return nil, "", err
	}
	r, err := proposedJobs(pr, official, next.TargetBranch)
	if err != nil {
		return nil, "", err
	}
	return r, pr.merge.SHA, nil
}

// ProposedStatus says how the pipeline an open merge request's change
// proposes stands, at the commits official, the merge request's
// MergeRequestStatus, found, so the two always speak of the same commits.
// It is nil when its change leaves the pipeline file alone. A merge
// request that can no longer merge proposes nothing now, but keeps its
// latest proposed pipeline to show, as official keeps its Latest: then
// there is no Key and no Current, and it is nil when none ran.
func (s *service) ProposedStatus(ctx context.Context, userID, projectID int64, official *MergeRequestStatus) (*ProposedStatus, error) {
	if official == nil {
		return nil, nil
	}
	if !official.Open {
		return s.endedProposedStatus(ctx, projectID, official.MergeRequestID)
	}
	p, err := s.projects.Get(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	head, target := official.Key.HeadSHA, official.Key.TargetSHA
	pr, err := s.proposal(ctx, p, head, target)
	if err != nil || pr.file == "" {
		// A change devpad can't diff, though it conflicts, may touch the
		// file or not: its official pipeline says it doesn't merge.
		return nil, err
	}
	mrID := official.MergeRequestID
	st := &ProposedStatus{
		File:      pr.file,
		Key:       MergeRequestKey{MergeRequestID: mrID, HeadSHA: head, TargetSHA: target},
		Automatic: official.Automatic,
	}
	latest, err := s.repo.Latest(ctx, projectID, LatestFilter{MergeRequestIDs: []int64{mrID}, Purpose: PurposeProposed})
	if err != nil {
		return nil, err
	}
	if len(latest) > 0 {
		st.Latest = latest[0]
	}
	switch {
	case pr.conflicts():
		st.Conflict = true
		return st, nil
	case pr.resolved.Source == SourceNone:
		st.Removed = true
		return st, nil
	}
	resolved, err := s.resolveCached(ctx, p, target)
	if err != nil {
		return nil, err
	}
	jobs, err := proposedJobs(pr, resolved, official.Target)
	if errors.Is(err, ErrSameJobs) {
		st.SameJobs = true
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	st.Key.ConfigHash, st.ConfigPath = jobs.Hash, jobs.Path
	if jobs.Invalid != nil {
		st.Invalid = jobs.Invalid.Error()
	}
	if err := s.stand(ctx, &st.Standing, st.Key.of(projectID, PurposeProposed), st.Automatic); err != nil {
		return nil, err
	}
	return st, nil
}

// endedProposedStatus is what is left of the pipeline a merge request that
// can no longer merge proposed: its latest proposed pipeline, or nil when
// none ran. The change can't be read against its target any more (once
// merged it is part of it), so the file is the one that pipeline's jobs
// came from.
func (s *service) endedProposedStatus(ctx context.Context, projectID, mrID int64) (*ProposedStatus, error) {
	latest, err := s.repo.Latest(ctx, projectID, LatestFilter{MergeRequestIDs: []int64{mrID}, Purpose: PurposeProposed})
	if err != nil || len(latest) == 0 {
		return nil, err
	}
	p := latest[0]
	return &ProposedStatus{File: p.ConfigPath, ConfigPath: p.ConfigPath, Standing: Standing{Latest: p}}, nil
}
