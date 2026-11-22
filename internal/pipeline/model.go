package pipeline

import (
	"errors"
	"slices"
	"time"
)

// Kind is what a pipeline tests.
type Kind string

const (
	// KindBranch tests a commit of a protected branch, with that commit's
	// pipeline.
	KindBranch Kind = "branch"
	// KindMergeRequest tests a merge request's head merged onto its target's
	// commit, with the target commit's pipeline.
	KindMergeRequest Kind = "merge_request"
)

// Status is where a pipeline is, or how it ended.
type Status string

const (
	// StatusQueued waits for its project's environment and a job slot; it
	// has made nothing yet.
	StatusQueued Status = "queued"
	// StatusRunning has a job slot: it is pinning what it tests, bundling
	// it, or running its jobs.
	StatusRunning Status = "running"
	// StatusPassed: every job passed.
	StatusPassed Status = "passed"
	// StatusFailed: a job's code failed, ran out of time, or ran out of
	// memory.
	StatusFailed Status = "failed"
	// StatusError: devpad could not run it, or could not set up a job, and
	// no job failed.
	StatusError Status = "error"
	// StatusInvalid: its pipeline does not parse. It never runs.
	StatusInvalid Status = "invalid"
	// StatusConflict: the merge request's source does not merge onto its
	// target, so there is nothing to test.
	StatusConflict Status = "conflict"
	// StatusNotConfigured: the commit has no pipeline file and the project
	// no pipeline setting, so there is no pipeline. It is never a pass.
	StatusNotConfigured Status = "not_configured"
	// StatusCancelled was stopped before it finished.
	StatusCancelled Status = "cancelled"
	// StatusSuperseded was replaced by a pipeline of the same branch or
	// merge request that tests something else: before it started, or while
	// it ran when the start that replaced it asked (StartInput.StopStale).
	StatusSuperseded Status = "superseded"
	// StatusInterrupted was queued or running when devpad stopped; a new
	// pipeline runs it again.
	StatusInterrupted Status = "interrupted"
)

// Finished reports whether a pipeline in status s is done for good.
func (s Status) Finished() bool {
	return s != StatusQueued && s != StatusRunning
}

// resultStatuses are those of pipelines that say something of the commits
// they tested, rather than having been stopped or having failed to run.
var resultStatuses = []Status{StatusPassed, StatusFailed, StatusInvalid, StatusConflict, StatusNotConfigured}

// unfinishedStatuses are those of pipelines still to end.
var unfinishedStatuses = []Status{StatusQueued, StatusRunning}

// countingStatuses are those of pipelines that count for what they test:
// one still to say how the commits stand, or one that said. One that was
// cancelled, superseded or interrupted, or could not run, said nothing, so
// it never hides an older one that did.
var countingStatuses = append(slices.Clone(unfinishedStatuses), resultStatuses...)

// HasResult reports whether a pipeline that ended in status s says
// something of the commits it tested, so that running the same jobs on them
// again would only say it again.
func (s Status) HasResult() bool {
	return slices.Contains(resultStatuses, s)
}

// Counts reports whether a pipeline in status s counts for what it tests:
// it has not finished, or it finished with a result. Reusing a result
// (StartInput.ReuseResult) and the latest pipeline of a merge request
// (Service.LatestForMergeRequest) both go by it, so they always agree.
func (s Status) Counts() bool {
	return slices.Contains(countingStatuses, s)
}

// JobStatus is where a job of a pipeline is, or how it ended.
type JobStatus string

const (
	// JobQueued waits for a job slot, or for its pipeline to start.
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	// JobPassed exited 0.
	JobPassed JobStatus = "passed"
	// JobFailed exited with another status.
	JobFailed JobStatus = "failed"
	// JobTimedOut ran past its timeout and was stopped.
	JobTimedOut JobStatus = "timed_out"
	// JobOutOfMemory failed after its container ran out of memory.
	JobOutOfMemory JobStatus = "out_of_memory"
	// JobError could not be set up, such as a service that never got ready
	// or one the project does not have, or devpad lost track of it.
	JobError JobStatus = "error"
	// JobCancelled was running when its pipeline was cancelled.
	JobCancelled JobStatus = "cancelled"
	// JobSkipped never ran: its pipeline ended first.
	JobSkipped JobStatus = "skipped"
	// JobInterrupted was running when devpad stopped.
	JobInterrupted JobStatus = "interrupted"
)

// Finished reports whether a job in status s is done for good.
func (s JobStatus) Finished() bool {
	return s != JobQueued && s != JobRunning
}

// failed reports whether a job ended because its own code failed.
func (s JobStatus) failed() bool {
	return s == JobFailed || s == JobTimedOut || s == JobOutOfMemory
}

// Trigger is what started a pipeline.
type Trigger string

const (
	// TriggerUser is the user, starting it by hand.
	TriggerUser Trigger = "user"
	// TriggerRestart is devpad running a pipeline again that its restart
	// interrupted.
	TriggerRestart Trigger = "restart"
)

const (
	// MaxRunningJobs is how many jobs of one project run at once, across
	// all its pipelines.
	MaxRunningJobs = 3
	// MaxJobOutput is how much of the end of a job's output is kept.
	MaxJobOutput = 1 << 20
	// KeptOutputs is how many of a project's newest pipelines that started
	// keep their jobs' output.
	KeptOutputs = 50
)

var (
	ErrNotFound    = errors.New("pipeline not found")
	ErrJobNotFound = errors.New("pipeline job not found")
	// ErrMergeRequestNotFound is a merge request the project does not have.
	ErrMergeRequestNotFound = errors.New("merge request not found")
	ErrInvalidInput         = errors.New("invalid input")
	ErrInvalidState         = errors.New("the pipeline cannot do that in its current status")
	// ErrShuttingDown refuses starting a pipeline while devpad stops.
	ErrShuttingDown = errors.New("devpad is shutting down")
)

// Pipeline is one run of a project's pipeline, on a branch's commit or on a
// merge request's head merged onto its target.
type Pipeline struct {
	ID        int64
	ProjectID int64
	UserID    int64
	// UID names the pipeline outside devpad's database: what it tests in the
	// canonical repo, refs/devpad/pipelines/<UID>, and everything its jobs
	// make in Docker.
	UID  string
	Kind Kind
	// Branch is the protected branch a KindBranch pipeline tests.
	Branch string
	// MergeRequestID is the merge request a KindMergeRequest pipeline tests,
	// and SourceBranch and TargetBranch its branches.
	MergeRequestID int64
	SourceBranch   string
	TargetBranch   string
	// HeadSHA is the commit tested: the branch's, or the merge request's
	// source head. TargetSHA is the target commit a merge request's head is
	// merged onto, whose pipeline it runs; empty for a branch.
	HeadSHA   string
	TargetSHA string
	// TestedSHA is the commit the jobs got: HeadSHA for a branch; for a
	// merge request the trial merge, or the target or head itself when one
	// already has the other. Empty until it is pinned, and when the source
	// does not merge.
	TestedSHA string
	// ConfigSource and ConfigPath say where the jobs came from, and
	// ConfigHash which they were (Resolved.Hash).
	ConfigSource Source
	ConfigPath   string
	ConfigHash   string
	// Image is the image the jobs ran on, set when the pipeline starts.
	Image  string
	Status Status
	// Error says why the pipeline itself ended as it did, when that was not
	// its jobs: what devpad could not do, the parse error of an invalid
	// pipeline, why the source does not merge, or why it was cancelled.
	Error string
	// ConflictPaths are the files that conflict, for StatusConflict.
	ConflictPaths []string
	Trigger       Trigger
	// RerunOf is the interrupted pipeline this one runs again, or zero.
	RerunOf int64
	// RefPinned says the canonical repo may still hold the pipeline's ref.
	RefPinned  bool
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	// AnnouncedAt is when every listener had been told it finished; nil
	// until then.
	AnnouncedAt *time.Time
	// Jobs are the pipeline's jobs in their file's order, when read with it.
	Jobs []*JobRun
}

// clone is a copy of p that shares nothing with it, jobs included.
func (p *Pipeline) clone() *Pipeline {
	c := *p
	c.ConflictPaths = append([]string(nil), p.ConflictPaths...)
	c.Jobs = make([]*JobRun, len(p.Jobs))
	for i, j := range p.Jobs {
		jc := *j
		jc.Services = append([]string(nil), j.Services...)
		c.Jobs[i] = &jc
	}
	return &c
}

// key is what makes two pipelines the same run: the same branch or merge
// request, the same commits and the same jobs.
type key struct {
	subject    subject
	head       string
	target     string
	configHash string
}

func (p *Pipeline) key() key {
	return key{subject: p.subject(), head: p.HeadSHA, target: p.TargetSHA, configHash: p.ConfigHash}
}

// MergeRequestKey names what a merge request's pipeline tests: its head
// merged onto a target commit, with the jobs of the pipeline at that commit.
type MergeRequestKey struct {
	MergeRequestID int64
	HeadSHA        string
	TargetSHA      string
	// ConfigHash is Resolved.Hash of the pipeline Resolve finds at TargetSHA
	// now, so that a pass of jobs the pipeline no longer has never counts.
	ConfigHash string
}

// probe is a pipeline of the project testing what k names, to look up
// others that test the same.
func (k MergeRequestKey) probe(projectID int64) *Pipeline {
	return &Pipeline{
		ProjectID: projectID, Kind: KindMergeRequest, MergeRequestID: k.MergeRequestID,
		HeadSHA: k.HeadSHA, TargetSHA: k.TargetSHA, ConfigHash: k.ConfigHash,
	}
}

// subject is what a pipeline is of: a branch or a merge request of a
// project.
type subject struct {
	projectID      int64
	kind           Kind
	branch         string
	mergeRequestID int64
}

func (p *Pipeline) subject() subject {
	return subject{projectID: p.ProjectID, kind: p.Kind, branch: p.Branch, mergeRequestID: p.MergeRequestID}
}

// JobRun is a job of a pipeline as it runs or ran.
type JobRun struct {
	// Job is what it runs, as resolved when its pipeline started.
	Job
	ID         int64
	PipelineID int64
	// Position is its place in its file, from 0.
	Position int
	Status   JobStatus
	// ExitCode is the script's exit status, when it ran to one.
	ExitCode *int
	// Error says why a job that did not pass did not.
	Error string
	// OutputBytes is how much it printed, and KeptBytes how much of the end
	// of that is stored: up to MaxJobOutput, and none once it is no longer
	// kept. Both are counted from the job's output as a running job's live
	// output reads it.
	OutputBytes int64
	KeptBytes   int64
	StartedAt   *time.Time
	FinishedAt  *time.Time
}

// OutputKept reports whether what is kept of the job's output is still
// there: it is not once the project has KeptOutputs newer pipelines.
func (j *JobRun) OutputKept() bool {
	return j.OutputBytes == 0 || j.KeptBytes > 0
}

// Note is one of devpad's own notes on a job: a step of setting it up, or
// how it ended. Notes are kept apart from what the job prints, so nothing a
// job prints can pass for one.
type Note struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// JobLog is what is kept of what a job told: the end of its output, Total
// the bytes it printed in all, and devpad's notes on it.
type JobLog struct {
	Output []byte
	Total  int64
	Notes  []Note
}
