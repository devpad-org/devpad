package mergerequest

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/task"
)

// What a merge request's pipeline ending sets going. When a pipeline
// guards it (its Gate), a task's merge request tells its task how the
// pipeline of its commits and jobs as they are now ended, so that a failure
// goes back to the task's agent (task.PipelineRounds decides whether), and
// the reviews it owes, the project's automatic review and the next round
// of an auto-fixing one, start only once that pipeline passed: a review of
// code that fails its jobs is wasted, and the agent fixing them would move
// the head under it. Unguarded, reviews start as soon as they are owed.
//
// Pipelines are told at least once, in any order: what acts reads the gate
// again under the merge request's lock and acts only on the pipeline it
// names, and the task decides each pipeline once.

// PipelineReport is what one of a merge request's pipelines found.
type PipelineReport struct {
	PipelineID int64
	// State is the gate's state the pipeline stands for as its merge
	// request's current one, as server.go maps its status: GateQueued,
	// GateRunning, GatePassed, GateFailed, GateConflict, GateInvalid,
	// GateErrored, GateCancelled or GateUnguarded (no pipeline configured), or
	// empty for one replaced or interrupted, or in a status the mapping
	// doesn't know, which tells neither a task nor a reviewer anything.
	State     GateState
	HeadSHA   string
	TargetSHA string
	// ConfigPath is the pipeline file its jobs came from, "" for the
	// project's pipeline setting.
	ConfigPath    string
	ConflictPaths []string
	// ConflictMore is how many more files conflict than ConflictPaths
	// lists.
	ConflictMore int
	// Jobs are all its jobs in the file's order, as a review agent may read
	// of them, and Failed those whose code failed, with the end of their
	// output when it was asked for, which only the task's agent reads.
	Jobs   []PipelineJobStanding
	Failed []task.PipelineJobResult
	// TargetPending says the target's own pipeline of TargetSHA is still to
	// finish, so whether a job fails there too is not known yet.
	TargetPending bool
}

// PipelineJobStanding is how one of a pipeline's jobs ended, in devpad's
// words alone: nothing the job printed, nor its script.
type PipelineJobStanding struct {
	Name     string
	Status   string
	Duration time.Duration
	// AlsoFailingOnTarget says it failed in the target branch's own
	// pipeline of the same commit too.
	AlsoFailingOnTarget bool
}

// BranchPipelineEnd is a protected branch's pipeline finishing, or an
// automatic run of the branch a merge request's result waited on starting
// none (PipelineID 0).
type BranchPipelineEnd struct {
	UserID     int64
	ProjectID  int64
	Branch     string
	PipelineID int64
}

// readReport reads what pipeline pipelineID of mr found.
func (s *service) readReport(ctx context.Context, mr *MergeRequest, pipelineID int64, outputTail int) (*PipelineReport, error) {
	pipelines := s.pipelineReader()
	if pipelines == nil {
		return nil, errNoPipelines
	}
	r, err := pipelines.PipelineReport(ctx, mr.UserID, mr.ProjectID, pipelineID, outputTail)
	if err != nil {
		return nil, fmt.Errorf("reading pipeline #%d: %w", pipelineID, err)
	}
	return r, nil
}

// PipelineFinished acts on one of the merge request's pipelines finishing,
// however it ended: it tells the task of a task's merge request how its
// pipeline ended and starts a review the gate now lets in (followPipeline),
// then decides again, in the background, on each approval waiting for the
// pipeline. The pipeline service records the pipeline announced only once
// this returns, so what it decides for the task and the review is decided
// first, and a devpad that stops before then is told again as it starts.
func (s *service) PipelineFinished(ctx context.Context, m PipelineEnd) {
	mr, err := s.repo.GetByID(ctx, m.MergeRequestID)
	if err != nil || mr == nil || mr.UserID != m.UserID || mr.ProjectID != m.ProjectID {
		return
	}
	if s.closing() {
		return
	}
	s.followPipeline(ctx, mr.ID, m.PipelineID)
	s.spawn(func(ctx context.Context) { s.resumeApprovals(ctx, mr) })
}

// BranchPipelineFinished follows the pipelines of the open task merge
// requests into a branch whose own pipeline finished: a failure of one of
// them that waited to know whether its jobs fail on the branch too is
// decided now.
func (s *service) BranchPipelineFinished(ctx context.Context, m BranchPipelineEnd) {
	if s.closing() {
		return
	}
	open, err := s.repo.ListOpenByTarget(ctx, m.ProjectID, m.Branch)
	if err != nil {
		log.Printf("project %d: reading the merge requests into %s after its pipeline #%d: %v", m.ProjectID, m.Branch, m.PipelineID, err)
		return
	}
	for _, mr := range open {
		if mr.UserID == m.UserID && mr.TaskID != 0 {
			s.followPipeline(ctx, mr.ID, 0)
		}
	}
}

// closing reports whether the service is shutting down.
func (s *service) closing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// followPipeline acts on how the pipeline gate of merge request mrID stands
// now: it tells a task's merge request's task how the pipeline guarding it
// ended, when that pipeline is only (any, when only is 0), and starts a
// review the merge request owes once the gate lets it (resumeReviewLocked).
// It holds the merge request's lock throughout, so its head cannot move
// meanwhile; merge requests call into tasks holding their locks, never the
// other way round.
func (s *service) followPipeline(ctx context.Context, mrID, only int64) {
	unlock := s.lock("mr", mrID)
	defer unlock()
	mr, err := s.repo.GetByID(ctx, mrID)
	if err != nil || mr == nil || mr.Status != StatusOpen {
		return
	}
	g, err := s.readGate(ctx, mr)
	if err != nil {
		log.Printf("merge request %d: following its pipeline: %v", mr.ID, err)
		return
	}
	if only == 0 || g.PipelineID == only {
		s.tellTask(ctx, mr, g)
	}
	if err := s.resumeReviewLocked(ctx, mr, g); err != nil {
		log.Printf("merge request %d: starting the review it owes: %v", mr.ID, err)
	}
}

// tellTask tells the task of mr, whose lock the caller holds, how the
// pipeline guarding mr ended on its commits and jobs as they are now, as g
// found: passed, failed, or found the branch doesn't merge. Nothing else
// is the agent's to answer: a pipeline that couldn't run, was cancelled or
// doesn't parse says nothing of its work, and the gate says why. A failure
// that comes while an agent reviews the same commits waits for the review
// (task.PipelineResult.Reviewing), whose end follows the pipeline again.
func (s *service) tellTask(ctx context.Context, mr *MergeRequest, g *Gate) {
	if mr.TaskID == 0 {
		return
	}
	res, err := s.pipelineResult(ctx, mr, g)
	if err != nil {
		log.Printf("merge request %d: %v", mr.ID, err)
		return
	}
	if res == nil {
		return
	}
	if res.Outcome != task.PipelinePassed {
		reviews, err := s.repo.ListReviews(ctx, mr.ID)
		if err != nil {
			log.Printf("merge request %d: reading its reviews: %v", mr.ID, err)
			return
		}
		res.Reviewing = agentReviewing(reviews, g.HeadSHA)
	}
	if _, err := s.tasks.PipelineEnded(ctx, mr.UserID, mr.ProjectID, mr.TaskID, *res); err != nil && !errors.Is(err, task.ErrNotFound) {
		log.Printf("merge request %d: telling task %d how pipeline #%d ended: %v", mr.ID, mr.TaskID, g.PipelineID, err)
	}
}

// pipelineResult is how the pipeline guarding mr ended on its commits and
// jobs as they are now, as g found, for its task: passed, failed with the
// end of each failed job's output, or found the branch doesn't merge. It is
// nil when no pipeline guards mr or none of its head has one of those
// results.
func (s *service) pipelineResult(ctx context.Context, mr *MergeRequest, g *Gate) (*task.PipelineResult, error) {
	if !g.Guarded || g.PipelineID == 0 || g.HeadSHA != mr.HeadSHA {
		return nil, nil
	}
	res := &task.PipelineResult{PipelineID: g.PipelineID, HeadSHA: g.HeadSHA, TargetBranch: mr.TargetBranch, TargetSHA: g.TargetSHA}
	tail := 0
	switch g.State {
	case GatePassed:
		res.Outcome = task.PipelinePassed
		return res, nil
	case GateFailed:
		tail, res.Outcome = task.PipelineOutputTail, task.PipelineFailed
	case GateConflict:
		res.Outcome = task.PipelineConflict
	default:
		return nil, nil
	}
	rep, err := s.readReport(ctx, mr, g.PipelineID, tail)
	if err != nil {
		return nil, err
	}
	if rep.HeadSHA != g.HeadSHA || rep.State != g.State {
		return nil, nil
	}
	res.JobsFrom = jobsFrom(rep.ConfigPath, mr.TargetBranch)
	res.Failed, res.TargetPending, res.ConflictFiles, res.ConflictMore = rep.Failed, rep.TargetPending, rep.ConflictPaths, rep.ConflictMore
	return res, nil
}

// agentReviewing reports whether an agent review of head is waiting to
// start or under way.
func agentReviewing(reviews []*Review, head string) bool {
	for _, rv := range reviews {
		if rv.Author == AuthorAgent && rv.Status == ReviewPending && rv.HeadSHA == head {
			return true
		}
	}
	return false
}

// sendBackPipeline is what the user's send back of mr tells the task's
// agent of mr's pipeline, with devpad's summary of it: the failure of the
// pipeline guarding mr now, read afresh, or when that can't be read, a
// failure that waits for the user, as the task keeps it. Empty when the
// pipeline has nothing of the task's to fix.
func (s *service) sendBackPipeline(ctx context.Context, mr *MergeRequest, t *task.Task) (feedback, summary string) {
	if res, err := s.currentPipeline(ctx, mr); err != nil {
		log.Printf("merge request %d: reading its pipeline to send back: %v", mr.ID, err)
	} else {
		if res != nil && res.Outcome != task.PipelinePassed {
			return task.PipelineFeedback(*res, mr.SourceBranch)
		}
		return "", ""
	}
	if t.Status == task.StatusChangesRequested && t.PipelineRounds.Held != task.HoldNone {
		return t.PendingMessage, t.PendingPipeline
	}
	return "", ""
}

// currentPipeline is pipelineResult as mr's gate stands now, under mr's
// lock.
func (s *service) currentPipeline(ctx context.Context, mr *MergeRequest) (*task.PipelineResult, error) {
	unlock := s.lock("mr", mr.ID)
	defer unlock()
	g, err := s.readGate(ctx, mr)
	if err != nil {
		return nil, err
	}
	return s.pipelineResult(ctx, mr, g)
}

// jobsFrom says where a pipeline into target got its jobs, for an agent.
func jobsFrom(configPath, target string) string {
	if configPath == "" {
		return "the project's pipeline setting"
	}
	return fmt.Sprintf("`%s` on `%s`", configPath, target)
}

// owedReview is a review a merge request owes: its round, and what it runs.
type owedReview struct {
	in    AgentReviewInput
	round int
	// first says it is the project's automatic review of a new merge
	// request, rather than the next round of an auto-fixing one.
	first bool
}

// reviewOwed is the review mr owes, given its reviews, or nil: the
// project's automatic review, while it waits for the pipeline and nothing
// has reviewed mr since, or the next round of an auto-fixing review, once
// the task's agent answered its requested changes with new commits.
// Anything since, such as the user's own review, ends the loop, as does a
// verdict other than request changes or the last round. moot says the
// automatic review that waited no longer will: another review came first,
// or the project stopped reviewing by itself.
func reviewOwed(p *project.Project, mr *MergeRequest, reviews []*Review) (owed *owedReview, moot bool) {
	if mr.ReviewAwaitsPipeline {
		if len(reviews) == 0 && p.AutoReview {
			return &owedReview{in: AgentReviewInput{AutoFix: mr.TaskID != 0}, round: 1, first: true}, false
		}
		moot = true
	}
	if len(reviews) == 0 || mr.TaskID == 0 {
		// Only a task's agent answers requested changes with commits.
		return nil, moot
	}
	last := reviews[len(reviews)-1]
	if last.Author != AuthorAgent || last.Status != ReviewSubmitted || last.Verdict != VerdictRequestChanges || !last.AutoFix || last.Round >= maxReviewRounds || last.HeadSHA == mr.HeadSHA {
		return nil, moot
	}
	return &owedReview{in: AgentReviewInput{AgentID: last.AgentID, Model: last.Model, Effort: last.Effort, AutoFix: true}, round: last.Round + 1}, moot
}

// resumeReview starts the review merge request mrID owes, if the gate lets
// it now, under the merge request's lock, so that no other review can start
// between reading the last review and starting the next. The next round
// runs the same agent and model as the review it follows. It returns why
// it could not decide, such as the gate unread.
func (s *service) resumeReview(ctx context.Context, mrID int64) error {
	unlock := s.lock("mr", mrID)
	defer unlock()
	mr, err := s.repo.GetByID(ctx, mrID)
	if err != nil {
		return err
	}
	if mr == nil || mr.Status != StatusOpen {
		return nil
	}
	if err := s.resumeReviewLocked(ctx, mr, nil); err != nil {
		log.Printf("merge request %d: starting the review it owes: %v", mr.ID, err)
		return err
	}
	return nil
}

// resumeReviews starts the reviews the open merge requests of project
// projectID owe whose gate lets them now, as when the project's settings
// change, which can open the gate or take it away. As devpad starts,
// followOpen does this for every project.
func (s *service) resumeReviews(ctx context.Context, projectID int64) {
	open, err := s.repo.ListByProjectID(ctx, projectID)
	if err != nil {
		log.Printf("reading the merge requests whose reviews to start: %v", err)
		return
	}
	for _, mr := range open {
		if mr.Status == StatusOpen && ctx.Err() == nil {
			s.resumeReview(ctx, mr.ID)
		}
	}
}

// followOpen follows, as devpad starts, each open merge request's
// pipeline: a task's tells its task how its pipeline stands
// (followPipeline), since what a failure waited on before a restart, such
// as a queued run of the target that starts no pipeline now, may tell it
// nothing more; every one starts the review it owes, if the gate lets it.
// The task decides each pipeline once, so one already decided goes back to
// nobody again.
func (s *service) followOpen(ctx context.Context) {
	open, err := s.repo.ListOpen(ctx)
	if err != nil {
		log.Printf("reading the open merge requests to follow: %v", err)
		return
	}
	for _, mr := range open {
		if ctx.Err() != nil {
			return
		}
		if mr.TaskID != 0 {
			s.followPipeline(ctx, mr.ID, 0)
		} else {
			s.resumeReview(ctx, mr.ID)
		}
	}
}

// resumeReviewLocked starts the review mr owes (reviewOwed) once the
// pipeline gate lets it: at once when no pipeline guards mr, else only
// once its pipeline passed on its commits and jobs as they are now. g is
// the gate as the caller read it, or nil to read it here, which happens
// only when mr owes a review; reading it is the error returned. A review
// that cannot start is recorded failed, so the merge request says why. The
// caller holds mr's lock.
func (s *service) resumeReviewLocked(ctx context.Context, mr *MergeRequest, g *Gate) error {
	p, err := s.projects.Get(ctx, mr.UserID, mr.ProjectID)
	if err != nil {
		return err
	}
	reviews, err := s.repo.ListReviews(ctx, mr.ID)
	if err != nil {
		return err
	}
	owed, moot := reviewOwed(p, mr, reviews)
	if moot {
		s.setReviewAwaits(ctx, mr, false)
	}
	if owed == nil {
		return nil
	}
	if g == nil {
		if g, err = s.readGate(ctx, mr); err != nil {
			return err
		}
	}
	if g.Guarded && !g.Clear {
		return nil
	}
	_, err = s.startAgentReviewLocked(ctx, mr.UserID, mr.ProjectID, mr.ID, owed.in, owed.round, nil)
	if owed.first {
		s.setReviewAwaits(ctx, mr, false)
	}
	if err != nil && s.rootCtx.Err() == nil {
		s.reviewNotStarted(ctx, mr, owed, err)
	}
	return nil
}

// setReviewAwaits records whether mr's automatic review waits for its
// pipeline.
func (s *service) setReviewAwaits(ctx context.Context, mr *MergeRequest, awaits bool) {
	if mr.ReviewAwaitsPipeline == awaits {
		return
	}
	if err := s.repo.SetReviewAwaitsPipeline(ctx, mr.ID, awaits); err != nil {
		log.Printf("merge request %d: %v", mr.ID, err)
		return
	}
	mr.ReviewAwaitsPipeline = awaits
}

// reviewNotStarted records the review mr owed as failed with why it could
// not start.
func (s *service) reviewNotStarted(ctx context.Context, mr *MergeRequest, owed *owedReview, err error) {
	rv := &Review{
		MergeRequestID: mr.ID, Author: AuthorAgent, Status: ReviewFailed, HeadSHA: mr.HeadSHA,
		AgentID: orDefault(owed.in.AgentID, domain.ReviewAgentID), Model: owed.in.Model, Effort: owed.in.Effort, AutoFix: owed.in.AutoFix, Round: owed.round,
	}
	if owed.first {
		log.Printf("merge request %d: starting automatic review: %v", mr.ID, err)
		rv.Error = "The automatic review could not start: " + err.Error()
		if errors.Is(err, errNoModel) {
			rv.Error = "The automatic review could not start: the review agent has no default model. Start a review with a model picked, or set a default model for the review agent in Settings."
		}
	} else {
		log.Printf("merge request %d: starting review round %d: %v", mr.ID, owed.round, err)
		rv.Error = "The next round of review could not start: " + err.Error()
	}
	if err := s.repo.CreateReview(ctx, rv); err != nil {
		log.Printf("merge request %d: recording that its review could not start: %v", mr.ID, err)
	}
}

// reviewPipelineSection tells a review agent how the pipeline of the
// change it reviews went: whether it passed, each job's name, status and
// time, and where the jobs came from. It never quotes what a job printed:
// that came from the code under review, which could use it to steer its
// reviewer. Empty when no pipeline of the change's commits and jobs as
// they are now has a result or is under way.
func reviewPipelineSection(rep *PipelineReport, target string) string {
	if rep == nil {
		return ""
	}
	var b strings.Builder
	onto := fmt.Sprintf("this change merged onto `%s` at `%s`", target, short(rep.TargetSHA))
	b.WriteString("## The pipeline\n\n")
	switch rep.State {
	case GateQueued, GateRunning:
		fmt.Fprintf(&b, "Devpad's pipeline #%d is still %s on %s, so its result isn't known yet.\n\n", rep.PipelineID, rep.State, onto)
		return b.String()
	case GateConflict:
		fmt.Fprintf(&b, "Devpad's pipeline #%d found this change doesn't merge onto `%s` at `%s`, so no job ran.\n\n", rep.PipelineID, target, short(rep.TargetSHA))
		return b.String()
	case GatePassed:
		fmt.Fprintf(&b, "Devpad's pipeline #%d passed on %s.", rep.PipelineID, onto)
	case GateFailed:
		fmt.Fprintf(&b, "Devpad's pipeline #%d failed on %s.", rep.PipelineID, onto)
	default:
		return ""
	}
	fmt.Fprintf(&b, " Its jobs come from %s, not from the change, and ran in fresh containers no agent could reach:\n\n", jobsFrom(rep.ConfigPath, target))
	for _, j := range rep.Jobs {
		fmt.Fprintf(&b, "- `%s`: %s", j.Name, strings.ReplaceAll(j.Status, "_", " "))
		if j.Duration > 0 {
			fmt.Fprintf(&b, " in %s", j.Duration.Round(time.Second))
		}
		if j.AlsoFailingOnTarget {
			fmt.Fprintf(&b, ", and fails on `%s` too", target)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nA pass says the jobs ran clean, not that they still test what they did, so check the change doesn't weaken them (see below).\n\n")
	return b.String()
}

// reviewPipeline reads the pipeline section of a review of rv's head: the
// pipeline the gate names, when it is of the commits the review reads and
// stands for them now, or nothing when it can't be read, which is no
// reason to hold the review back.
func (s *service) reviewPipeline(ctx context.Context, mr *MergeRequest, rv *Review) string {
	g, err := s.readGate(ctx, mr)
	if err != nil {
		log.Printf("review %d: reading the pipeline for its prompt: %v", rv.ID, err)
		return ""
	}
	if g.PipelineID == 0 || g.HeadSHA != rv.HeadSHA {
		return ""
	}
	switch g.State {
	case GatePassed, GateFailed, GateConflict, GateQueued, GateRunning:
	default:
		return ""
	}
	rep, err := s.readReport(ctx, mr, g.PipelineID, 0)
	if err != nil {
		log.Printf("review %d: %v", rv.ID, err)
		return ""
	}
	if rep.HeadSHA != rv.HeadSHA {
		return ""
	}
	return reviewPipelineSection(rep, mr.TargetBranch)
}
