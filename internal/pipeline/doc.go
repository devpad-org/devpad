// Package pipeline is a project's CI pipeline: the jobs its
// .devpad/pipeline.yml names, which run on each new commit of the protected
// branches the user ticks and on each merge request into them, each job in
// a fresh container no agent can reach.
//
// # Where the jobs come from
//
// Which copy of the file counts decides what an agent can get away with.
// Resolve reads the file at a full commit hash: a branch pipeline's own
// commit, and for a merge request the target's commit its head is merged
// onto, never the change's, so an agent cannot rewrite the jobs that grade
// it. A commit without a file uses the project's pipeline setting, which
// only the user edits (Handler.HandleSetConfig validates it, the project
// stores it). A file that does not parse makes the pipeline invalid and
// never falls back to the setting, which would quietly run other jobs. Only
// protected branches run pipelines: an unprotected branch's file is its
// author's to edit. Resolved.Hash is of the jobs (JobsHash), so rewriting
// the YAML without changing them keeps it. A merge request whose change
// touches the pipeline file also runs the pipeline it proposes, read at the
// trial merge, for information only (proposed.go).
//
// # A run
//
// Service.Start resolves a pipeline under the project's start lock
// (lockStarts), so a start that takes it after another never reads older
// commits or settings, and never supersedes a newer commit's pipeline with
// an older one's. What a start returns instead of running again, and what
// it stops, follows from its trigger alone (startPolicyOf): the user's Run
// again always runs; a restart's re-run reuses a result; devpad's own runs
// also reuse one the user cancelled or that couldn't run errorRetries
// times, and a merge request's stop its started pipelines of other commits
// or jobs. Every start supersedes its subject's queued pipelines of
// something else.
//
// The run (runner.go) waits for an environment build and a job slot
// (jobSlots: MaxRunningJobs per project, official jobs before proposed
// ones), has the repo container pin what it tests to
// refs/devpad/pipelines/<UID> (a commit, or the trial merge, whose tree a
// merge it lets through must land: TestedTree), bundles that once and runs
// every job through jobrun: its own network, fresh copies of its services,
// a container from the recorded image with the bundle put in before it
// starts, the script under its timeout, the result from Docker's exit code,
// never from anything the job printed, and cleanup by label. Devpad's own
// notes on a job are kept apart from its output, so a job can't forge one.
// Cancelling, superseding or deleting the project only ends a run's
// context, with a cause the run records.
//
// # What counts
//
// countingStatuses is the one predicate for which pipeline stands for a
// subject's commits and jobs: one queued or running, or one finished with a
// result; one cancelled, superseded, interrupted or unable to run never
// hides an older result. Service.current, reuse and the badges all go by
// it. Every lookup is by key (subject, purpose included, commits and jobs
// hash): a key without a purpose matches nothing, so a lookup that forgets
// it finds no pass rather than a proposed pipeline's.
//
// MergeRequestStatus is the one answer to whether a merge request's result
// is current: it reads the head, the target's tip and the jobs resolved
// there now, and its Standing (Current, else NoResult, else Latest, out of
// date), which a branch's badge and the proposed card share. Its Gate is the
// merge gate: guarded when the merge request is open, its target is ticked
// and a pipeline is configured there (an invalid one too), and then clear
// only on a pass of the current key. The merge request service asks it
// before every merge.
//
// # Automatic runs
//
// auto.go: branchRunsAutomatically and mergeRequestRunsAutomatically decide
// what runs by itself. The project's BranchListener and SettingsListener and
// the merge request service's listener only queue runs or end contexts;
// autoQueue coalesces them per key, hands a key out at most once per
// autoMinInterval and a project's runs one at a time, for a few workers
// that read the state as it is then and Start. Nothing starts a pipeline
// because one ended, so no pipeline starts another, and what an agent can
// do (push a new head) runs at most one pipeline per interval per merge
// request. As each pipeline gets a slot, begin checks its subject still
// wants it.
//
// # Reconcile and shutdown
//
// Shutdown records nothing, so Reconcile treats it as a crash: queued and
// running pipelines no run of this process holds become interrupted and
// their subjects run again (rerun_of) unless a newer result covers them,
// bundles left in this devpad's directory go, listeners are told what they
// were not, and a sweep every sweepInterval removes what finished
// pipelines left in Docker and the canonical repo, never anything of a UID
// the database doesn't know nor of a run of this process. Automatic runs
// begin only after the interrupted ones are marked, with a catch-up of
// every ticked branch.
//
// # Events
//
//   - project.BranchListener and project.SettingsListener: told
//     synchronously once the project's locks are released, a merge's move
//     while the merge request service still holds that merge request's
//     lock; the Service only queues.
//   - mergerequest.Listener (adapted in internal/server): a head move told
//     outside the merge request's lock; MergeRequestEnded under it, which
//     only ends contexts.
//   - Listener: each finished pipeline, from a goroutine holding no
//     pipeline lock, at least once and in any order (announced_at, and
//     Reconcile tells again), so a listener re-reads what it acts on.
//     BranchRunListener hears that an automatic branch run Report awaited
//     started no pipeline.
//
// # Lock order
//
// A merge request's lock comes before its task's lock, which comes before
// the project's branch and publish locks. Start, and with it the project's
// start lock, is never taken under any of them: only HTTP handlers,
// Reconcile and the automatic-run workers call it. Under a merge request's
// or a task's lock, code only reads pipelines (MergeRequestStatus and its
// Gate, Report, BranchPipeline, none of which takes the start lock:
// TestReadsUnderAMergeRequestsLockNeverWaitForTheStartLock), queues work
// (MergeRequestMoved, BranchMoved) or ends contexts (MergeRequestEnded).
// The Service's own mutexes (mu, autoQueue's, jobSlots', the resolve and
// proposal caches) are leaves: nothing calls out while holding one.
//
// # Outside the package
//
// This package imports project, never mergerequest or task; they never
// import it either. internal/server/pipelines.go adapts each side to the
// other's narrow interfaces, mapping the gate's states and pipeline
// statuses explicitly (an unknown one shuts the gate). The merge request
// service merges, sends a failure back to the task, which decides its
// rounds (task.PipelineRounds), and starts reviews once the gate lets
// them; a proposed pipeline's end is never told to it. Job output reaches
// only the task's own agent and its user: a reviewer and the assistant see
// job names, statuses and devpad's summary.
package pipeline
