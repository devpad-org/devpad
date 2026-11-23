package pipeline

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/devpad-org/devpad/internal/project"
)

// MergeRequestStatus is how a merge request stands for pipelines now: what
// a pipeline must test to count for it, and the pipeline that does.
type MergeRequestStatus struct {
	// Open says the merge request can still merge into a protected branch.
	// When it can't, there is no Key and no Current: only Latest.
	Open bool
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
	// Current is the newest pipeline of Key that counts (Status.Counts):
	// one queued or running, or one finished with a result. It is what
	// LatestForMergeRequest returns for Key, and nil when none has run.
	Current *Pipeline
	// Latest is the merge request's newest pipeline that counts, whatever
	// it tested: Current, or an older result to show as out of date.
	Latest *Pipeline
}

// Passed reports whether the merge request's pipeline passed on its head
// and target as they are now, with the jobs its target has now.
func (st *MergeRequestStatus) Passed() bool {
	return st.Open && st.Current != nil && st.Current.Status == StatusPassed
}

// Configured reports whether a pipeline guards the merge request now.
func (st *MergeRequestStatus) Configured() bool {
	return st.Source != SourceNone
}

// MergeRequestStatus reads how a merge request of the user's project stands
// for pipelines: it reads the merge request's head through MergeRequests,
// its target's tip from the canonical repo, and resolves the pipeline at
// that tip, so the answer is never about commits or jobs that have moved
// on. It is the one place that says whether a merge request's pipeline
// result is current: the merge request page shows it, and the merge gate is
// to refuse unless Passed, or no pipeline is Configured.
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
	st := &MergeRequestStatus{}
	latest, err := s.repo.Latest(ctx, projectID, LatestFilter{MergeRequestIDs: []int64{mr.ID}})
	if err != nil {
		return nil, err
	}
	if len(latest) > 0 {
		st.Latest = latest[0]
	}
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
	if st.Current, err = s.LatestForMergeRequest(ctx, userID, projectID, st.Key); err != nil {
		return nil, err
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
