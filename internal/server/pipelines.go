package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/devpad-org/devpad/internal/mergerequest"
	"github.com/devpad-org/devpad/internal/pipeline"
	"github.com/devpad-org/devpad/internal/task"
)

// pipelineMergeRequests shows pipelines the merge requests they test. The
// pipeline package never imports the merge request package, which gates
// merges on pipelines, and the merge request package never imports the
// pipeline package, so the two meet here.
type pipelineMergeRequests struct {
	mrs mergerequest.Service
}

func (a pipelineMergeRequests) PipelineMergeRequest(ctx context.Context, userID, projectID, mrID int64) (*pipeline.MergeRequest, error) {
	mr, err := a.mrs.Lookup(ctx, userID, projectID, mrID)
	if errors.Is(err, mergerequest.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pipelineMergeRequest(mr), nil
}

func (a pipelineMergeRequests) PipelineMergeRequestsInto(ctx context.Context, userID, projectID int64, target string) ([]*pipeline.MergeRequest, error) {
	mrs, err := a.mrs.ListOpenInto(ctx, userID, projectID, target)
	if err != nil {
		return nil, err
	}
	out := make([]*pipeline.MergeRequest, 0, len(mrs))
	for _, mr := range mrs {
		out = append(out, pipelineMergeRequest(mr))
	}
	return out, nil
}

func pipelineMergeRequest(mr *mergerequest.MergeRequest) *pipeline.MergeRequest {
	return &pipeline.MergeRequest{
		ID:           mr.ID,
		SourceBranch: mr.SourceBranch,
		TargetBranch: mr.TargetBranch,
		HeadSHA:      mr.HeadSHA,
		Open:         mr.Status == mergerequest.StatusOpen,
		Merged:       mr.Status == mergerequest.StatusMerged,
	}
}

// mergeRequestPipelines tells the pipeline service each time a merge
// request opens or its head moves, so that one into a ticked branch runs
// its pipeline, and when it closes or merges, so that its pipelines stop.
// The merge request service owns the listener, and never imports the
// pipeline package, so the two meet here too.
type mergeRequestPipelines struct {
	pipelines pipeline.Service
}

func (a mergeRequestPipelines) MergeRequestHeadMoved(ctx context.Context, m mergerequest.HeadMove) {
	a.pipelines.MergeRequestMoved(ctx, pipeline.MergeRequestMove{
		UserID: m.UserID, ProjectID: m.ProjectID, MergeRequestID: m.MergeRequestID, Opened: m.Opened,
	})
}

func (a mergeRequestPipelines) MergeRequestEnded(ctx context.Context, m mergerequest.End) {
	a.pipelines.MergeRequestEnded(ctx, pipeline.MergeRequestEnd{
		UserID: m.UserID, ProjectID: m.ProjectID, MergeRequestID: m.MergeRequestID, Merged: m.Merged,
	})
}

// mergeRequestGate is the merge gate every merge asks first: the pipeline
// service's MergeRequestStatus.Gate, the one rule for whether a merge
// request's pipeline lets it merge. It only reads, so the merge request
// service asks it under the merge request's lock.
type mergeRequestGate struct {
	pipelines pipeline.Service
}

func (a mergeRequestGate) MergeGate(ctx context.Context, userID, projectID, mrID int64) (*mergerequest.Gate, error) {
	st, err := a.pipelines.MergeRequestStatus(ctx, userID, projectID, mrID)
	if err != nil {
		return nil, err
	}
	g := st.Gate()
	out := &mergerequest.Gate{
		State: string(g.State), Guarded: g.Guarded(), Clear: g.Clear(), Waits: g.Waits, Overridable: g.Overridable(),
		Reason: g.Reason, HeadSHA: g.HeadSHA, TargetSHA: g.TargetSHA, TestedTree: g.TestedTree,
	}
	if g.Pipeline != nil {
		out.PipelineID = g.Pipeline.ID
	}
	return out, nil
}

// mergeRequestPipelineReports reads for the merge request service what a
// merge request's pipeline found, for its task's agent and its reviewer:
// every job with its status and time, and of each that failed its script,
// devpad's word on it, the end of its output and whether it fails on the
// target too.
type mergeRequestPipelineReports struct {
	pipelines pipeline.Service
}

func (a mergeRequestPipelineReports) PipelineReport(ctx context.Context, userID, projectID, pipelineID int64, outputTail int) (*mergerequest.PipelineReport, error) {
	r, err := a.pipelines.Report(ctx, userID, projectID, pipelineID, outputTail)
	if err != nil {
		return nil, err
	}
	p := r.Pipeline
	out := &mergerequest.PipelineReport{
		PipelineID: p.ID, Status: string(p.Status), HeadSHA: p.HeadSHA, TargetSHA: p.TargetSHA,
		ConflictPaths: p.ConflictPaths, ConflictMore: p.ConflictMore, TargetPending: r.Target != nil && r.Target.Pending,
	}
	if p.ConfigSource == pipeline.SourceFile {
		out.ConfigPath = p.ConfigPath
	}
	for _, j := range p.Jobs {
		standing := mergerequest.PipelineJobStanding{Name: j.Name, Status: string(j.Status)}
		if j.StartedAt != nil && j.FinishedAt != nil {
			standing.Duration = j.FinishedAt.Sub(*j.StartedAt)
		}
		if j.Status.Failed() {
			standing.AlsoFailingOnTarget = r.Target.Fails(j.Name)
		}
		out.Jobs = append(out.Jobs, standing)
		if !j.Status.Failed() {
			continue
		}
		res := task.PipelineJobResult{
			Name: j.Name, Status: standing.Status, ExitCode: j.ExitCode, Duration: standing.Duration, AlsoFailingOnTarget: standing.AlsoFailingOnTarget,
			Run: j.Run, Error: j.Error, OutputBytes: j.OutputBytes,
		}
		if told := r.Logs[j.ID]; told != nil {
			res.Output = string(told.Output)
			for _, n := range told.Notes {
				res.Notes = append(res.Notes, n.Text)
			}
		}
		out.Failed = append(out.Failed, res)
	}
	return out, nil
}

// mergeRequestPipelineEnds tells the merge request service each time an
// official pipeline finishes: one of a merge request's, so that its task
// hears how it went, a review waiting for it starts, and an automatic merge
// waiting for it decides again; or a protected branch's, so that a failure
// of a merge request into it waiting on it is decided. A pipeline a change
// proposes is for information alone, so the merge request service never
// hears of it: it sends no task back, starts no review and decides no
// merge (the gate never names one either). The pipeline service tells it
// holding none of its locks; the merge request service takes its own.
type mergeRequestPipelineEnds struct {
	mrs mergerequest.Service
}

func (a mergeRequestPipelineEnds) PipelineFinished(ctx context.Context, p *pipeline.Pipeline) {
	if p.Purpose != pipeline.PurposeOfficial {
		return
	}
	if p.Kind == pipeline.KindBranch {
		a.mrs.BranchPipelineFinished(ctx, mergerequest.BranchPipelineEnd{
			UserID: p.UserID, ProjectID: p.ProjectID, Branch: p.Branch, PipelineID: p.ID,
		})
		return
	}
	a.mrs.PipelineFinished(ctx, mergerequest.PipelineEnd{
		UserID: p.UserID, ProjectID: p.ProjectID, MergeRequestID: p.MergeRequestID, PipelineID: p.ID,
	})
}

// BranchRunStartedNone tells the merge request service that a branch's
// automatic run that a merge request's result waited on started no
// pipeline, so it decides the merge requests into the branch again, as a
// branch pipeline's end does.
func (a mergeRequestPipelineEnds) BranchRunStartedNone(ctx context.Context, userID, projectID int64, branch string) {
	a.mrs.BranchPipelineFinished(ctx, mergerequest.BranchPipelineEnd{UserID: userID, ProjectID: projectID, Branch: branch})
}

// JobScripts reads the scripts of the jobs a merge request into branch
// runs, those of the pipeline at branch's tip, for its look-closely mark.
func (a mergeRequestPipelineReports) JobScripts(ctx context.Context, userID, projectID int64, branch string) ([]string, error) {
	r, err := a.pipelines.BranchPipeline(ctx, userID, projectID, branch)
	if errors.Is(err, pipeline.ErrInvalidInput) {
		// A branch no longer protected runs no pipeline.
		return nil, nil
	}
	if err != nil || r.Config == nil {
		return nil, err
	}
	var scripts []string
	for _, j := range r.Config.Jobs {
		scripts = append(scripts, j.Run)
	}
	return scripts, nil
}

// taskPipelines tells a task's agent the jobs its merge request's pipeline
// will run: those of the pipeline at the tip of the branch it merges into.
type taskPipelines struct {
	pipelines pipeline.Service
}

func (a taskPipelines) PipelineJobs(ctx context.Context, userID, projectID int64, branch string) (*task.PipelineJobs, error) {
	r, err := a.pipelines.BranchPipeline(ctx, userID, projectID, branch)
	if err != nil || r.Config == nil {
		return nil, err
	}
	out := &task.PipelineJobs{}
	if r.Source == pipeline.SourceFile {
		out.Path = r.Path
	}
	for _, j := range r.Config.Jobs {
		out.Jobs = append(out.Jobs, task.PipelineJob{Name: j.Name, Run: j.Run, Services: j.Services})
	}
	return out, nil
}

// pipelineBundleDir is where this devpad keeps its pipelines' bundles: a
// directory of the user's own, named by the database, so that the same
// devpad finds it again after a restart and removes what it left there, and
// no other devpad uses it. Empty, when there is none, means the system's
// temporary directory, where nothing left is removed.
func pipelineBundleDir(dbPath string) string {
	cache, err := os.UserCacheDir()
	if err != nil {
		log.Printf("pipelines keep their bundles in the system's temporary directory: %v", err)
		return ""
	}
	db, err := filepath.Abs(dbPath)
	if err != nil {
		log.Printf("pipelines keep their bundles in the system's temporary directory: %v", err)
		return ""
	}
	sum := sha256.Sum256([]byte(db))
	dir := filepath.Join(cache, "devpad", "pipeline-bundles-"+hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("pipelines keep their bundles in the system's temporary directory: %v", err)
		return ""
	}
	return dir
}
