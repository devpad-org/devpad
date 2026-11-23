package pipeline

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"

	"github.com/devpad-org/devpad/internal/project"
)

// MergeRequestStatus is how a merge request stands for pipelines now: what
// a pipeline must test to count for it, and the pipeline that does. Only
// its official pipelines count: one its change proposes (ProposedStatus)
// never does.
type MergeRequestStatus struct {
	// Open says the merge request can still merge into a protected branch.
	// When it can't, there is no Key and no Current: only Latest.
	Open bool
	// Target is the branch it merges into.
	Target string
	// Key is what a pipeline of the merge request must test to count now:
	// its current head merged onto its target branch's current tip, with
	// the jobs the target's pipeline at that tip has now.
	Key MergeRequestKey
	// Source and ConfigPath say where the jobs that would run now come
	// from. SourceNone means no pipeline is configured: neither the target
	// commit nor the project has one, so no pipeline guards the merge
	// request, and a pipeline of Key would be StatusNotConfigured.
	Source     Source
	ConfigPath string
	// Invalid is why the pipeline that would run now doesn't parse, or
	// empty; a pipeline of Key would be StatusInvalid.
	Invalid string
	// Current is the newest pipeline of Key that counts (Service.current):
	// one queued or running, or one finished with a result; nil when none
	// has run.
	Current *Pipeline
	// Latest is the merge request's newest pipeline that counts, whatever
	// it tested: Current, or an older result to show as out of date.
	Latest *Pipeline
	// Automatic says devpad runs the merge request's pipelines by itself:
	// it is open, into a branch ticked to run them.
	Automatic bool
	// Settled is, when no pipeline of Key counts, the newest of Key that
	// ended without a result, cancelled or unable to run, with why and
	// whether devpad runs Key once more: what the merge request shows
	// rather than "no pipeline yet", and what the merge gate names when it
	// is shut. Nil otherwise.
	Settled *SettledPipeline
	// OnTarget is, when Current failed, how the target's own pipeline went
	// on the commit Current merged onto, which says which of its failures
	// the change didn't bring. Nil otherwise.
	OnTarget *TargetResult
}

// SettledPipeline is a pipeline of the commits and jobs a branch or merge
// request has now that ended without a result, when none of those counts.
type SettledPipeline struct {
	Pipeline *Pipeline
	// Retrying says devpad runs those commits and jobs once more by itself,
	// at the next event or catch-up: the pipeline could not run, the
	// errorRetries are not used up, and its subject runs automatically.
	Retrying bool
	// Reason says, in a sentence for the user, why no result counts and
	// what happens next.
	Reason string
}

// settledOf is the SettledPipeline of what probe tests, or nil when one of
// it counts or none ended without a result. automatic says its subject
// runs by itself.
func (s *service) settledOf(ctx context.Context, probe *Pipeline, automatic bool) (*SettledPipeline, error) {
	current, err := s.current(ctx, probe)
	if err != nil || current != nil {
		return nil, err
	}
	return s.settledWithout(ctx, probe, automatic)
}

// settledWithout is settledOf for what probe tests when none of it counts.
func (s *service) settledWithout(ctx context.Context, probe *Pipeline, automatic bool) (*SettledPipeline, error) {
	ended, retry, err := s.settled(ctx, probe)
	if err != nil || ended == nil {
		return nil, err
	}
	retrying := retry && automatic
	return &SettledPipeline{Pipeline: ended, Retrying: retrying, Reason: s.settledReason(ended, retrying, automatic)}, nil
}

// settledReason says why pipeline p of a subject's commits and jobs as they
// are now leaves them without a result, and whether devpad runs them again.
func (s *service) settledReason(p *Pipeline, retrying, automatic bool) string {
	if p.Status == StatusCancelled {
		if automatic {
			return fmt.Sprintf("Pipeline #%d was cancelled on these commits, so devpad won't run them again by itself: run it again.", p.ID)
		}
		return fmt.Sprintf("Pipeline #%d was cancelled on these commits: run it again.", p.ID)
	}
	why := fmt.Sprintf("Pipeline #%d couldn't run on these commits: %s.", p.ID, strings.TrimSuffix(noResultError(p), "."))
	switch {
	case retrying:
		return why + fmt.Sprintf(" Devpad runs them once more by itself within %d minutes.", int(s.sweepInterval.Minutes()+0.5))
	case automatic:
		return why + " Devpad won't run them again by itself: run it again once the cause is fixed."
	}
	return why + " Run it again once the cause is fixed."
}

// noResultError is why a pipeline that could not run could not: its own
// error, or else its first job that could not be set up.
func noResultError(p *Pipeline) string {
	if p.Error != "" {
		return p.Error
	}
	for _, j := range p.Jobs {
		if j.Status == JobError && j.Error != "" {
			return "job " + j.Name + ": " + j.Error
		}
	}
	return "devpad could not run it"
}

// Configured reports whether a pipeline is configured for the merge
// request now: its target's commit has a pipeline file, or the project a
// setting. One that doesn't parse (Invalid) is too: it can never pass, so
// into a ticked branch a broken pipeline file or setting keeps the merge
// request from merging (Gate) rather than letting it through. A merge
// request that can no longer merge has no Source, and so is never taken for
// one with none.
func (st *MergeRequestStatus) Configured() bool {
	return st.Source != SourceNone
}

// MergeRequestStatus reads how a merge request of the user's project stands
// for pipelines: it reads the merge request's head through MergeRequests,
// its target's tip from the canonical repo, and resolves the pipeline at
// that tip, so the answer is never about commits or jobs that have moved
// on. It is the one place that says whether a merge request's pipeline
// result is current: the merge request page shows it, and its Gate is the
// merge gate.
func (s *service) MergeRequestStatus(ctx context.Context, userID, projectID, mergeRequestID int64) (*MergeRequestStatus, error) {
	p, err := s.projects.Get(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	mr, err := s.mrs.PipelineMergeRequest(ctx, userID, projectID, mergeRequestID)
	if err != nil {
		return nil, err
	}
	if mr == nil {
		return nil, ErrMergeRequestNotFound
	}
	st := &MergeRequestStatus{Target: mr.TargetBranch}
	latest, err := s.repo.Latest(ctx, projectID, LatestFilter{MergeRequestIDs: []int64{mr.ID}, Purpose: PurposeOfficial})
	if err != nil {
		return nil, err
	}
	if len(latest) > 0 {
		st.Latest = latest[0]
	}
	st.Automatic = mergeRequestRunsAutomatically(p, mr)
	if !mr.Open || !p.IsProtected(mr.TargetBranch) || !project.IsCommitHash(mr.HeadSHA) {
		return st, nil
	}
	target, err := s.branchCommit(ctx, p, mr.TargetBranch)
	if err != nil {
		return nil, err
	}
	resolved, err := s.resolveCached(ctx, p, target)
	if err != nil {
		return nil, err
	}
	st.Open = true
	st.Key = MergeRequestKey{MergeRequestID: mr.ID, HeadSHA: mr.HeadSHA, TargetSHA: target, ConfigHash: resolved.Hash}
	st.Source, st.ConfigPath = resolved.Source, resolved.Path
	if resolved.Invalid != nil {
		st.Invalid = resolved.Invalid.Error()
	}
	if st.Current, err = s.current(ctx, st.Key.probe(projectID, PurposeOfficial)); err != nil {
		return nil, err
	}
	if st.Current != nil && st.Current.Status == StatusFailed {
		if st.OnTarget, err = s.targetResult(ctx, st.Current, false); err != nil {
			return nil, err
		}
	}
	if st.Current == nil {
		if st.Settled, err = s.settledWithout(ctx, st.Key.probe(projectID, PurposeOfficial), st.Automatic); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// resolveCache holds what Resolve found at a commit with a project setting,
// which never changes, so a merge request polled for its status reads its
// target's pipeline file once per target commit.
type resolveCache struct {
	mu    sync.Mutex
	found map[resolveKey]*Resolved
}

type resolveKey struct {
	projectID int64
	commit    string
	setting   [sha256.Size]byte
}

// maxResolved bounds the cache; past it, it starts again empty.
const maxResolved = 512

func (s *service) resolveCached(ctx context.Context, p *project.Project, commit string) (*Resolved, error) {
	key := resolveKey{projectID: p.ID, commit: commit, setting: sha256.Sum256([]byte(p.PipelineConfig))}
	c := &s.resolved
	c.mu.Lock()
	r, ok := c.found[key]
	c.mu.Unlock()
	if ok {
		return r, nil
	}
	r, err := Resolve(ctx, s.projects, p, commit)
	if err != nil {
		return nil, fmt.Errorf("resolving the pipeline at %s: %w", commit, err)
	}
	c.mu.Lock()
	if c.found == nil || len(c.found) >= maxResolved {
		c.found = make(map[resolveKey]*Resolved)
	}
	c.found[key] = r
	c.mu.Unlock()
	return r, nil
}
