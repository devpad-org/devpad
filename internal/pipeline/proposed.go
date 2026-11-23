package pipeline

import (
	"context"
	"fmt"
	"sync"

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
// finds it. A change that removes the pipeline file proposes no jobs, and
// one that doesn't merge none that can be read, so nothing runs for either.

// NoProposal is why a merge request's change runs no proposed pipeline: it
// leaves the pipeline file alone, removes it, or doesn't merge onto its
// target. It is an ErrInvalidState.
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
	// pipeline file, so the change proposes none. Nothing runs for either.
	Conflict bool
	Removed  bool
	// ConfigPath is the file the proposed jobs come from, and Invalid why
	// it doesn't parse, or empty.
	ConfigPath string
	Invalid    string
	// Current, Latest and Settled are as MergeRequestStatus's, of the
	// merge request's proposed pipelines.
	Current *Pipeline
	Latest  *Pipeline
	Settled *SettledPipeline
}

// proposal is what a merge request's change proposes for its pipeline when
// its head merges onto a target commit: the pipeline file it touches, or ""
// for none, and when it does, the trial merge and the pipeline found there.
// It depends only on the two commits, which never change.
type proposal struct {
	file     string
	merge    *agent.TrialMerge
	resolved *Resolved
}

// refusal is why no proposed pipeline of the proposal runs, or nil.
func (pr *proposal) refusal(target string) error {
	switch {
	case pr.file == "":
		return ErrPipelineUnchanged
	case pr.merge.Conflict != nil:
		return &NoProposal{fmt.Sprintf("the change doesn't merge onto %s, so the pipeline it proposes can't be read", target)}
	case pr.resolved.Source == SourceNone:
		return ErrPipelineRemoved
	}
	return nil
}

// proposalCache holds the proposals read, by project and commits.
type proposalCache struct {
	mu    sync.Mutex
	found map[proposalKey]*proposal
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
	c.mu.Lock()
	pr, ok := c.found[key]
	c.mu.Unlock()
	if ok {
		return pr, nil
	}
	pr, err := s.readProposal(ctx, p, head, target)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.found == nil || len(c.found) >= maxProposals {
		c.found = make(map[proposalKey]*proposal)
	}
	c.found[key] = pr
	c.mu.Unlock()
	return pr, nil
}

func (s *service) readProposal(ctx context.Context, p *project.Project, head, target string) (*proposal, error) {
	diff, err := s.projects.Diff(ctx, p.UserID, p.ID, target, head)
	if err != nil {
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

// resolveJobs resolves the jobs a pipeline about to be made runs: an
// official one's are those Resolve finds at its branch's commit or its
// merge request's target's, never the source's; a proposed one's those its
// change proposes, or why none run.
func (s *service) resolveJobs(ctx context.Context, p *project.Project, next *Pipeline) (*Resolved, error) {
	if next.Purpose != PurposeProposed {
		return Resolve(ctx, s.projects, p, configCommit(next))
	}
	pr, err := s.proposal(ctx, p, next.HeadSHA, next.TargetSHA)
	if err != nil {
		return nil, err
	}
	if err := pr.refusal(next.TargetBranch); err != nil {
		return nil, err
	}
	return pr.resolved, nil
}

// ProposedStatus says how the pipeline an open merge request's change
// proposes stands, at the commits official, the merge request's
// MergeRequestStatus, found, so the two always speak of the same commits.
// It is nil when the merge request can no longer merge or its change
// leaves the pipeline file alone.
func (s *service) ProposedStatus(ctx context.Context, userID, projectID int64, official *MergeRequestStatus) (*ProposedStatus, error) {
	if official == nil || !official.Open {
		return nil, nil
	}
	p, err := s.projects.Get(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	head, target := official.Key.HeadSHA, official.Key.TargetSHA
	pr, err := s.proposal(ctx, p, head, target)
	if err != nil || pr.file == "" {
		return nil, err
	}
	mrID := official.Key.MergeRequestID
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
	case pr.merge.Conflict != nil:
		st.Conflict = true
		return st, nil
	case pr.resolved.Source == SourceNone:
		st.Removed = true
		return st, nil
	}
	st.Key.ConfigHash, st.ConfigPath = pr.resolved.Hash, pr.resolved.Path
	if pr.resolved.Invalid != nil {
		st.Invalid = pr.resolved.Invalid.Error()
	}
	probe := st.Key.probe(projectID, PurposeProposed)
	if st.Current, err = s.repo.FindSame(ctx, probe, countingStatuses); err != nil {
		return nil, err
	}
	if st.Current == nil {
		if st.Settled, err = s.settledWithout(ctx, probe, st.Automatic); err != nil {
			return nil, err
		}
	}
	return st, nil
}
