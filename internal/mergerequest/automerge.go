package mergerequest

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/task"
)

// Automatic merges. A task's merge request into a branch the project
// merges into automatically merges once a review agent's approving run
// ends, by devpad's own rules (autoMergeable), never by anything an agent
// says. When a pipeline guards the target, the pipeline must have passed on
// the change merged onto the target as it is then, and an approval whose
// run ends before it has waits for it: its run stays unsettled, and each
// time one of the merge request's pipelines finishes (PipelineFinished),
// or devpad starts again (Reconcile), devpad decides again. An automatic
// merge never overrides the gate.

// errNotAutoMerged refuses an automatic merge that was never going to
// happen, such as into a branch the project does not merge into
// automatically, and so says nothing on the review.
var errNotAutoMerged = errors.New("not merged automatically")

// errAwaitingPipeline holds back an automatic merge whose every condition
// holds but for its pipeline, which has yet to pass and still may.
var errAwaitingPipeline = errors.New("waiting for the merge request's pipeline")

// refusal is why an approval did not merge its change automatically, in
// words for the user.
type refusal string

func (r refusal) Error() string { return string(r) }

func refuse(format string, args ...any) error {
	return refusal(fmt.Sprintf(format, args...))
}

// autoMergeReason says why an automatic merge failed, in words for the
// user: a refusal as it is, and the repo's refusal, such as a conflict.
// Anything else is only in the log.
func autoMergeReason(err error) string {
	var r refusal
	var agentErr *agent.AgentError
	switch {
	case errors.As(err, &r):
		return string(r)
	case errors.As(err, &agentErr) && agentErr.StatusCode >= 400 && agentErr.StatusCode < 500:
		if len(agentErr.Conflicts) > 0 {
			return fmt.Sprintf("%s in %s", agentErr.Message, strings.Join(agentErr.Conflicts, ", "))
		}
		return agentErr.Message
	case errors.Is(err, project.ErrInvalidInput):
		return strings.TrimPrefix(err.Error(), project.ErrInvalidInput.Error()+": ")
	case errors.Is(err, project.ErrNotReady):
		return "the project's repo was not ready"
	default:
		return "devpad could not merge it"
	}
}

// settleApproval settles the end of review reviewID's run, a review
// agent's approval of mr: it merges the change when every condition holds
// (autoMergeable), and records the run settled, with why the approval did
// not merge when the project merges into the target automatically. While
// the merge request's pipeline has yet to pass and still may, it records
// nothing, leaving the run unsettled for the pipeline's end or the next
// start to decide again. It never pushes upstream.
func (s *service) settleApproval(ctx context.Context, mr *MergeRequest, reviewID int64, run domain.AgentRun) {
	merged := s.decideApproval(ctx, mr, reviewID, run)
	if merged != nil {
		log.Printf("merge request %d: merged into %s automatically after review %d approved it", merged.ID, merged.TargetBranch, reviewID)
		s.followMerge(ctx, merged)
	}
}

// decideApproval is settleApproval under the merge request's lock, which
// it takes, so that each approval is settled once: one already settled is
// left alone. It returns the merge request when it merged it.
func (s *service) decideApproval(ctx context.Context, mr *MergeRequest, reviewID int64, run domain.AgentRun) *MergeRequest {
	unlock := s.lock("mr", mr.ID)
	defer unlock()
	rv, err := s.repo.GetReview(ctx, reviewID)
	if err != nil {
		log.Printf("review %d: reading it to settle its approval: %v", reviewID, err)
		return nil
	}
	if rv == nil || rv.RunSettled {
		return nil
	}
	out, err := s.mergeLocked(ctx, mr.UserID, mr.ProjectID, mr.ID, mergeOptions{by: MergedByAuto, admit: func(open *MergeRequest) (*admission, error) {
		return s.autoMergeable(ctx, open, reviewID, run)
	}})
	var moved *targetMovedError
	var reason string
	switch {
	case err == nil:
	case errors.Is(err, errAwaitingPipeline), errors.As(err, &moved) && moved.guarded:
		log.Printf("merge request %d: review %d's approval waits for the pipeline: %v", mr.ID, reviewID, err)
		return nil
	case moved != nil:
		// Unguarded, the target was to stay where the review read it.
		log.Printf("merge request %d: not merged automatically: %v", mr.ID, err)
		reason = autoMergeReason(moved.cause)
	// A merge request merged or closed meanwhile has nothing to say.
	case errors.Is(err, errNotAutoMerged), errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalidState):
	default:
		log.Printf("merge request %d: not merged automatically: %v", mr.ID, err)
		reason = autoMergeReason(err)
	}
	if settleErr := s.repo.SettleRun(ctx, reviewID, reason); settleErr != nil {
		log.Printf("review %d: %v", reviewID, settleErr)
	}
	if err != nil {
		return nil
	}
	return out.MergeRequest
}

// autoMergeable lets mr, whose lock the caller holds, merge automatically
// on the approval of review reviewID in run, or says why not. It merges
// only what the review saw: the review must still be the latest and of the
// current head, its run must have completed, no blocking thread may be
// open, the task must be waiting for review, and the change must leave the
// pipeline file alone (changedPipelineFile). Then the pipeline gate:
// when a pipeline guards the target, it must have passed on the change
// merged onto the target as it is now, which tests what the review could
// not, so the target moving after the review no longer refuses it; one
// still to pass waits (errAwaitingPipeline). When none does, the target
// must still be at the commit the review read the change against. Either
// way the repo merges only while the target is still at that commit, so a
// merge into it meanwhile, by the user or another approval, stops this one.
func (s *service) autoMergeable(ctx context.Context, mr *MergeRequest, reviewID int64, run domain.AgentRun) (*admission, error) {
	p, err := s.projects.Get(ctx, mr.UserID, mr.ProjectID)
	if err != nil {
		return nil, err
	}
	if mr.TaskID == 0 || mr.OpenedBy != AuthorTaskAgent || !p.AutoMerges(mr.TargetBranch) {
		return nil, errNotAutoMerged
	}
	reviews, err := s.repo.ListReviews(ctx, mr.ID)
	if err != nil {
		return nil, err
	}
	if len(reviews) == 0 {
		return nil, errNotAutoMerged
	}
	rv := reviews[len(reviews)-1]
	switch {
	case rv.ID != reviewID:
		return nil, refuse("another review started after this one")
	case rv.Author != AuthorAgent || rv.Status != ReviewSubmitted || rv.Verdict != VerdictApprove:
		return nil, errNotAutoMerged
	case rv.HeadSHA != mr.HeadSHA:
		return nil, refuse("%s moved past the commit this review approved", mr.SourceBranch)
	}
	if why := runCutShort(run); why != "" {
		return nil, refusal(why)
	}
	comments, err := s.repo.ListComments(ctx, mr.ID)
	if err != nil {
		return nil, err
	}
	switch n := len(openBlocking(comments)); n {
	case 0:
	case 1:
		return nil, refuse("a blocking thread is open")
	default:
		return nil, refuse("%d blocking threads are open", n)
	}
	t, err := s.tasks.Get(ctx, mr.UserID, mr.ProjectID, mr.TaskID)
	if err != nil {
		return nil, err
	}
	if t.Status != task.StatusInReview {
		return nil, refuse("its task is %s, not in review", strings.ReplaceAll(string(t.Status), "_", " "))
	}
	file, err := s.changedPipelineFile(ctx, mr)
	if err != nil {
		return nil, err
	}
	if file != "" {
		return nil, refuse("it changes %s, which devpad never merges by itself", file)
	}
	gate, err := s.readGate(ctx, mr)
	if err != nil {
		return nil, err
	}
	switch {
	case gate.Guarded && gate.Clear:
		return &admission{targetSHA: gate.TargetSHA, guarded: true, expectedTree: gate.TestedTree}, nil
	case gate.Waits:
		return nil, errAwaitingPipeline
	case !gate.Clear:
		return nil, refusal(gate.Reason)
	}
	target, err := s.canonicalSHA(ctx, mr.UserID, mr.ProjectID, mr.TargetBranch)
	if err != nil {
		return nil, err
	}
	if rv.TargetSHA == "" || target != rv.TargetSHA {
		return nil, refuse("%s moved after this review read the change", mr.TargetBranch)
	}
	return &admission{targetSHA: rv.TargetSHA}, nil
}

// changedPipelineFile names the pipeline file mr's change adds, changes,
// renames or deletes, or "" when it touches none. A change to the jobs that
// grade it, or to whether any do, is the user's to merge: merged on an
// agent's approval, it would weaken or remove the gate of every merge
// request after it.
func (s *service) changedPipelineFile(ctx context.Context, mr *MergeRequest) (string, error) {
	diff, err := s.projects.Diff(ctx, mr.UserID, mr.ProjectID, mr.TargetBranch, mr.HeadSHA)
	if err != nil {
		return "", fmt.Errorf("reading what the change touches: %w", err)
	}
	pipelineFiles := project.PipelineFiles()
	for _, f := range diff.Files {
		for _, path := range []string{f.Path, f.OldPath} {
			if path != "" && slices.Contains(pipelineFiles, path) {
				return path, nil
			}
		}
	}
	return "", nil
}

// runCutShort says why the run of an approving review did not complete, or
// "" when it did. The approval of a review agent that failed or was stopped
// after approving, even by a restart, does not merge the change.
func runCutShort(run domain.AgentRun) string {
	switch {
	case run.Status == domain.AgentRunCompleted:
		return ""
	case run.Interrupted():
		return "devpad restarted before the review agent finished"
	case run.Status == domain.AgentRunCancelled:
		return "the review agent was stopped before it finished"
	default:
		return "the review agent failed before it finished"
	}
}

// PipelineEnd is one of a merge request's pipelines finishing.
type PipelineEnd struct {
	UserID         int64
	ProjectID      int64
	MergeRequestID int64
}

// PipelineFinished decides again, in the background, on each approval of
// the merge request waiting for its pipeline: one of its pipelines
// finished, however it ended. Being told twice, or of an older pipeline,
// decides nothing twice, since each approval is read again under the merge
// request's lock and settled once.
func (s *service) PipelineFinished(_ context.Context, m PipelineEnd) {
	s.spawn(func(ctx context.Context) {
		mr, err := s.repo.GetByID(ctx, m.MergeRequestID)
		if err != nil || mr == nil || mr.UserID != m.UserID || mr.ProjectID != m.ProjectID {
			return
		}
		s.resumeApprovals(ctx, mr)
	})
}

// ProjectSettingsChanged decides again, in the background, on every
// approval of the project waiting for a pipeline: the branches it merges
// into automatically, or runs pipelines on, or its pipeline setting changed,
// which can open the gate, shut it, or end the wait altogether. Deciding
// now, under the rules as they are, keeps an approval from waiting for a
// pipeline that will never run, and from merging at the next start under
// rules that no longer hold. It only takes note: the project tells it
// synchronously.
func (s *service) ProjectSettingsChanged(_ context.Context, _, after *project.Project) {
	projectID := after.ID
	s.spawn(func(ctx context.Context) {
		reviews, err := s.repo.ListUnsettledReviews(ctx)
		if err != nil {
			log.Printf("project %d: reading the approvals to decide again: %v", projectID, err)
			return
		}
		seen := make(map[int64]bool)
		for _, rv := range reviews {
			if seen[rv.MergeRequestID] {
				continue
			}
			seen[rv.MergeRequestID] = true
			mr, err := s.repo.GetByID(ctx, rv.MergeRequestID)
			if err != nil || mr == nil || mr.ProjectID != projectID {
				continue
			}
			s.resumeApprovals(ctx, mr)
		}
	})
}

// resumeApprovals settles each unsettled approval of mr whose run has
// ended: the latest merges if its pipeline now lets it, and any other is
// refused as no longer the latest. One whose run goes on is left to the
// run's end.
func (s *service) resumeApprovals(ctx context.Context, mr *MergeRequest) {
	reviews, err := s.repo.ListReviews(ctx, mr.ID)
	if err != nil {
		log.Printf("merge request %d: reading its reviews to decide on its approvals: %v", mr.ID, err)
		return
	}
	for _, rv := range reviews {
		if !rv.Finishing() || rv.Verdict != VerdictApprove || rv.RunID == 0 {
			continue
		}
		run, err := s.runs.GetRun(ctx, mr.UserID, rv.RunID)
		if err != nil || !domain.AgentRunStatusTerminal(run.Status) {
			continue
		}
		s.settleApproval(context.WithoutCancel(ctx), mr, rv.ID, *run)
	}
}
