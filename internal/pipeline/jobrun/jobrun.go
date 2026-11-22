// Package jobrun runs one job of a pipeline in a fresh container of its
// own, and says how it ended.
//
// A job gets its own network and fresh copies of the services it names,
// then a container from the project's image with no agent in it, holding
// the bundle of what its pipeline tests. Devpad clones the bundle into
// /workspace, runs the job's script under its timeout, and reads how it
// ended from Docker, never from anything the job prints. Everything the job
// made is removed after, whatever happened.
//
// The package knows nothing of pipelines' records: the pipeline package
// decides when a job runs and what its outcome means for its pipeline.
package jobrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/pipelineid"
	"github.com/devpad-org/devpad/internal/wsservice"
)

// Runtime is what running a job needs of Docker; container.JobRuntime is
// one.
type Runtime interface {
	CreateJobNetwork(ctx context.Context, job container.Job) error
	StartJobContainer(ctx context.Context, spec container.JobContainerSpec) (string, error)
	ExecJob(ctx context.Context, containerID string, spec container.ExecSpec, out io.Writer) (container.ExecResult, error)
	RemoveJobResources(ctx context.Context, sel container.Job) error
}

// Services gives a job fresh copies of its project's services; the
// workspace service package's Service is one.
type Services interface {
	StartJobServices(ctx context.Context, req wsservice.JobServicesRequest) (*wsservice.JobServices, error)
	RemoveJobServices(ctx context.Context, services *wsservice.JobServices) error
}

// Spec is a job to run.
type Spec struct {
	// Job names the job to Docker: its pipeline's UID and its own name.
	Job container.Job
	// ProjectID is the project whose services the job gets copies of.
	ProjectID int64
	// Image is the project's environment image; empty means devpad's own.
	Image string
	// Run is the job's bash script, and Timeout how long it may run.
	Run     string
	Timeout time.Duration
	// Services are the types of the project's services the job names.
	Services []string
	// Bundle is the git bundle of what the pipeline tests.
	Bundle Bundle
}

// Bundle is a git bundle on the host holding what a pipeline tests at its
// pipeline's ref.
type Bundle struct {
	Path string
	Size int64
}

// Outcome is how a job ended.
type Outcome string

const (
	// Passed: the script exited 0.
	Passed Outcome = "passed"
	// Failed: the script exited with another status.
	Failed Outcome = "failed"
	// TimedOut: the script ran past the job's timeout and was stopped.
	TimedOut Outcome = "timed_out"
	// OutOfMemory: the script failed after the container ran out of memory.
	OutOfMemory Outcome = "out_of_memory"
	// Stopped: the caller's context ended before the job did. Its cause
	// says why.
	Stopped Outcome = "stopped"
	// Error: the job could not be set up or followed, so its script did not
	// run to an end.
	Error Outcome = "error"
)

// Result is how a job ended.
type Result struct {
	Outcome Outcome
	// ExitCode is the script's exit status, when it ran to one.
	ExitCode *int
	// Error says, for the user, why a job that did not pass did not.
	Error string
}

// Config tunes a Runner; zero fields get their defaults.
type Config struct {
	// Limits bound each job's container; zero means
	// container.DefaultJobLimits.
	Limits container.Limits
	// CloneTimeout bounds cloning the bundle into the container.
	CloneTimeout time.Duration
	// NetworkRetry is how often a job retries for a network while Docker
	// has no address pool left, and NetworkWait how long it keeps trying.
	NetworkRetry time.Duration
	NetworkWait  time.Duration
}

const (
	defaultCloneTimeout = 10 * time.Minute
	defaultNetworkRetry = 10 * time.Second
	defaultNetworkWait  = 15 * time.Minute
	// lifetimeMargin is what a job's container lives beyond its clone and
	// its timeout, so it outlives every command devpad runs in it but stops
	// on its own even if devpad is gone.
	lifetimeMargin = 2 * time.Minute
)

// bundlePath is where a job's container holds the bundle.
const bundlePath = "/tmp/pipeline.bundle"

// Runner runs jobs.
type Runner struct {
	runtime  Runtime
	services Services
	cfg      Config
}

// New creates a Runner.
func New(runtime Runtime, services Services, cfg Config) *Runner {
	if cfg.Limits == (container.Limits{}) {
		cfg.Limits = container.DefaultJobLimits()
	}
	if cfg.CloneTimeout <= 0 {
		cfg.CloneTimeout = defaultCloneTimeout
	}
	if cfg.NetworkRetry <= 0 {
		cfg.NetworkRetry = defaultNetworkRetry
	}
	if cfg.NetworkWait <= 0 {
		cfg.NetworkWait = defaultNetworkWait
	}
	return &Runner{runtime: runtime, services: services, cfg: cfg}
}

// errTimedOut is the cause of a script's context when its time is up.
var errTimedOut = errors.New("the job ran out of time")

// Run runs a job, writing to out what it prints, with a line from devpad
// before each step of setting it up and one saying how it ended. It always
// removes what the job made before it returns. When ctx ends first the
// outcome is Stopped, whatever the job was doing.
func (r *Runner) Run(ctx context.Context, spec Spec, out io.Writer) Result {
	if err := spec.validate(); err != nil {
		return r.ended(out, Result{Outcome: Error, Error: err.Error()})
	}
	defer r.cleanup(ctx, spec.Job)

	if err := r.createNetwork(ctx, spec.Job, out); err != nil {
		return r.setupFailed(ctx, out, err)
	}
	services, err := r.startServices(ctx, spec, out)
	if err != nil {
		return r.setupFailed(ctx, out, err)
	}
	defer r.removeServices(ctx, spec.Job, services)

	containerID, err := r.startContainer(ctx, spec, services, out)
	if err != nil {
		return r.setupFailed(ctx, out, err)
	}
	if result, ok := r.clone(ctx, spec, containerID, out); !ok {
		return r.ended(out, result)
	}
	return r.ended(out, r.runScript(ctx, spec, containerID, out))
}

func (s Spec) validate() error {
	if err := s.Job.Validate(); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(s.Run) == "":
		return errors.New("the job has nothing to run")
	case s.Timeout <= 0:
		return errors.New("the job has no timeout")
	case s.Bundle.Path == "" || s.Bundle.Size <= 0:
		return errors.New("the job has no bundle to clone")
	}
	return nil
}

// createNetwork makes the job's network, waiting while Docker has no
// address pool left for it: other networks, such as other jobs', free them.
func (r *Runner) createNetwork(ctx context.Context, job container.Job, out io.Writer) error {
	say(out, "creating the job's network")
	deadline := time.Now().Add(r.cfg.NetworkWait)
	waiting := false
	for {
		err := r.runtime.CreateJobNetwork(ctx, job)
		if err == nil || !errors.Is(err, container.ErrNoNetworkPool) || ctx.Err() != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w; waited %s for one: Docker's default-address-pools setting decides how many networks it can make", err, r.cfg.NetworkWait)
		}
		if !waiting {
			say(out, "Docker has no address pool left for another network; waiting for one to be freed")
			waiting = true
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(r.cfg.NetworkRetry):
		}
	}
}

func (r *Runner) startServices(ctx context.Context, spec Spec, out io.Writer) (*wsservice.JobServices, error) {
	if len(spec.Services) == 0 {
		return &wsservice.JobServices{}, nil
	}
	say(out, "starting %s and waiting until %s ready", strings.Join(spec.Services, ", "), plural(len(spec.Services), "it is", "they are"))
	types := make([]wsservice.ServiceType, len(spec.Services))
	for i, t := range spec.Services {
		types[i] = wsservice.ServiceType(t)
	}
	return r.services.StartJobServices(ctx, wsservice.JobServicesRequest{ProjectID: spec.ProjectID, Types: types, Job: spec.Job})
}

func (r *Runner) startContainer(ctx context.Context, spec Spec, services *wsservice.JobServices, out io.Writer) (string, error) {
	image := spec.Image
	if image == "" {
		image = container.WorkspaceImage
	}
	say(out, "starting the job's container from %s", image)
	bundle, err := os.Open(spec.Bundle.Path)
	if err != nil {
		return "", fmt.Errorf("opening the bundle: %w", err)
	}
	defer bundle.Close()
	return r.runtime.StartJobContainer(ctx, container.JobContainerSpec{
		Job:      spec.Job,
		Image:    image,
		Env:      services.Env,
		Files:    []container.JobFile{{Path: bundlePath, Mode: 0o644, Size: spec.Bundle.Size, Content: bundle}},
		Limits:   r.cfg.Limits,
		Lifetime: r.cfg.CloneTimeout + spec.Timeout + lifetimeMargin,
	})
}

// cloneCommands clone the bundle into /workspace as a job's container
// runs them: each a command of its own, never through a shell, and naming
// nothing but devpad's own ref, so nothing of the repo's, such as a branch
// name, is ever read as a command. The checkout is detached at the commit
// tested, as CI checkouts usually are.
func cloneCommands(pipelineUID string) [][]string {
	return [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "fetch", "-q", "--no-tags", bundlePath, pipelineid.Ref(pipelineUID)},
		{"git", "checkout", "-q", "--detach", "FETCH_HEAD"},
	}
}

// clone clones the bundle into the container's /workspace, reporting false
// with the job's result when it could not.
func (r *Runner) clone(ctx context.Context, spec Spec, containerID string, out io.Writer) (Result, bool) {
	say(out, "cloning the commit under test into /workspace")
	cloneCtx, cancel := context.WithTimeoutCause(ctx, r.cfg.CloneTimeout, errCloneTimedOut)
	defer cancel()
	for _, cmd := range cloneCommands(spec.Job.Pipeline) {
		res, err := r.runtime.ExecJob(cloneCtx, containerID, container.ExecSpec{Cmd: cmd, WorkingDir: "/workspace"}, out)
		switch {
		case ctx.Err() != nil:
			return stopped(ctx), false
		case errors.Is(err, errCloneTimedOut):
			return Result{Outcome: Error, Error: fmt.Sprintf("cloning the commit under test took over %s", r.cfg.CloneTimeout)}, false
		case err != nil:
			return Result{Outcome: Error, Error: fmt.Sprintf("cloning the commit under test: %v", err)}, false
		case res.ExitCode != 0:
			return Result{Outcome: Error, Error: fmt.Sprintf("cloning the commit under test: %s exited with %d", strings.Join(cmd[:2], " "), res.ExitCode)}, false
		}
	}
	return Result{}, true
}

var errCloneTimedOut = errors.New("cloning ran out of time")

// runScript runs the job's script under its timeout, and says how it ended.
func (r *Runner) runScript(ctx context.Context, spec Spec, containerID string, out io.Writer) Result {
	say(out, "running the job")
	runCtx, cancel := context.WithTimeoutCause(ctx, spec.Timeout, errTimedOut)
	defer cancel()
	res, err := r.runtime.ExecJob(runCtx, containerID, container.ExecSpec{Cmd: container.JobScript(spec.Run), WorkingDir: "/workspace"}, out)
	switch {
	case ctx.Err() != nil:
		return stopped(ctx)
	case err != nil && errors.Is(context.Cause(runCtx), errTimedOut):
		return Result{Outcome: TimedOut, Error: fmt.Sprintf("the job ran for longer than its timeout of %s", spec.Timeout)}
	case err != nil:
		return Result{Outcome: Error, Error: fmt.Sprintf("running the job: %v", err)}
	}
	code := res.ExitCode
	switch {
	case code == 0:
		return Result{Outcome: Passed, ExitCode: &code}
	case res.OOMKilled:
		return Result{Outcome: OutOfMemory, ExitCode: &code, Error: fmt.Sprintf("the job ran out of memory (%s) and exited with %d", memory(r.cfg.Limits.Memory), code)}
	default:
		return Result{Outcome: Failed, ExitCode: &code, Error: fmt.Sprintf("the job exited with %d", code)}
	}
}

// setupFailed is the result of a job that could not be set up: Stopped when
// ctx ended, which is likely why, and otherwise an error saying what failed,
// which the errors of each step already name.
func (r *Runner) setupFailed(ctx context.Context, out io.Writer, err error) Result {
	if ctx.Err() != nil {
		return r.ended(out, stopped(ctx))
	}
	msg := err.Error()
	if errors.Is(err, wsservice.ErrNotProjectService) || errors.Is(err, wsservice.ErrInvalidType) {
		msg += ": add it to the project's services, or take it out of the job"
	}
	return r.ended(out, Result{Outcome: Error, Error: msg})
}

func stopped(ctx context.Context) Result {
	return Result{Outcome: Stopped, Error: context.Cause(ctx).Error()}
}

// ended writes devpad's line saying how the job ended, and returns result.
func (r *Runner) ended(out io.Writer, result Result) Result {
	switch result.Outcome {
	case Passed:
		say(out, "the job passed")
	case Stopped:
		say(out, "the job was stopped: %s", result.Error)
	default:
		say(out, "%s", result.Error)
	}
	return result
}

// removeServices removes the services the job started.
func (r *Runner) removeServices(ctx context.Context, job container.Job, services *wsservice.JobServices) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), container.JobCleanupTimeout)
	defer cancel()
	if err := r.services.RemoveJobServices(cleanupCtx, services); err != nil {
		log.Printf("pipeline %s job %s: removing its services: %v", job.Pipeline, job.Name, err)
	}
}

// cleanup removes everything Docker has of the job, found by its labels and
// names, which covers what a create cut short left. It runs on a context of
// its own, since the job's ending is often why it runs.
func (r *Runner) cleanup(ctx context.Context, job container.Job) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), container.JobCleanupTimeout)
	defer cancel()
	if err := r.runtime.RemoveJobResources(cleanupCtx, job); err != nil {
		log.Printf("pipeline %s job %s: removing what it made: %v", job.Pipeline, job.Name, err)
	}
}

// say writes one of devpad's own lines into a job's output.
func say(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "devpad: "+format+"\n", args...)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func memory(bytes int64) string {
	if bytes >= 1<<30 && bytes%(1<<30) == 0 {
		return fmt.Sprintf("%d GB", bytes>>30)
	}
	return fmt.Sprintf("%d MB", bytes>>20)
}
