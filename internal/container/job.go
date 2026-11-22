package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/devpad-org/devpad/internal/loginpath"
	"github.com/devpad-org/devpad/internal/pipelineid"
)

// A pipeline job runs in a container of its own, made from the project's
// environment image but with no agent: its entrypoint only sleeps, and devpad
// runs the job's commands in it with ExecJob and reads their exit codes from
// Docker. It is on a network of its own with the job's services, which
// devpad never joins, and gets nothing of devpad's: no agent token, SSH keys
// or project settings, only the job's services' connection variables and
// the files devpad puts in before it starts. It has no root to become: its
// user's sudo is refused, and it has no capabilities. Tools a job needs
// belong in the project's environment.
//
// Everything made for a job carries its labels and a name of the job's, so
// whatever a job leaves behind, even after a restart or a create that failed
// part way, can be found and removed by them.

// JobRuntime is what running pipeline jobs needs of Docker.
type JobRuntime interface {
	// CreateJobNetwork creates a job's own bridge network, which devpad
	// never joins. It fails with ErrNoNetworkPool while Docker has no
	// address range left for one.
	CreateJobNetwork(ctx context.Context, job Job) error
	// StartJobContainer creates a job's container on its network, puts its
	// files in, and starts it.
	StartJobContainer(ctx context.Context, spec JobContainerSpec) (containerID string, err error)
	// ExecJob runs a command in a job's container, streaming its output to
	// out, and reports how it ended. When ctx ends first it kills the
	// container.
	ExecJob(ctx context.Context, containerID string, spec ExecSpec, out io.Writer) (ExecResult, error)
	// ListJobResources lists the containers and networks of pipeline jobs:
	// with sel empty every pipeline's, with only its Pipeline set one
	// pipeline's, or one job's.
	ListJobResources(ctx context.Context, sel Job) ([]JobResource, error)
	// RemoveJobResources removes what ListJobResources lists for sel, which
	// names a pipeline: containers with their anonymous volumes, then
	// networks.
	RemoveJobResources(ctx context.Context, sel Job) error
}

// Docker is the Docker daemon as devpad uses it: for workspaces and their
// services, and for pipeline jobs.
type Docker interface {
	Manager
	JobRuntime
}

// ErrNoNetworkPool is returned when Docker has no address range left for a
// job's network. It passes once other networks go, so a job waits and
// tries again rather than failing.
var ErrNoNetworkPool = errors.New("docker has no address pool left for another network")

// Labels on everything made for a pipeline job.
const (
	LabelPipeline = "devpad.pipeline"
	LabelJob      = "devpad.job"
	labelManaged  = "devpad.managed"
	labelSidecar  = "devpad.sidecar"
)

// JobUser is the user a job's container and commands run as.
const JobUser = "devpad"

// JobCleanupTimeout bounds removing what a job made, which runs on a context
// of its own since cancelling is often why it runs.
const JobCleanupTimeout = 30 * time.Second

// Limits bound a container: memory with no swap, CPU, and the processes and
// threads it runs at once.
type Limits struct {
	Memory   int64
	NanoCPUs int64
	Pids     int64
}

// The limits of a pipeline job's container, and of each of its services.
var (
	JobLimits        = Limits{Memory: 4 << 30, NanoCPUs: 2_000_000_000, Pids: 4096}
	JobServiceLimits = Limits{Memory: 2 << 30, NanoCPUs: 1_000_000_000, Pids: 2048}
)

func (l Limits) resources() container.Resources {
	r := container.Resources{Memory: l.Memory, MemorySwap: l.Memory, NanoCPUs: l.NanoCPUs}
	if l.Pids > 0 {
		pids := l.Pids
		r.PidsLimit = &pids
	}
	return r
}

// jobNamePattern is looser than the pipeline file's rule, and keeps names
// safe in Docker's: no dots, which separate them.
var jobNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Job names one job of one pipeline to Docker.
type Job struct {
	Pipeline string
	Name     string
}

// Validate refuses a job whose pipeline id or name Docker's names and
// labels cannot carry.
func (j Job) Validate() error {
	if !pipelineid.Valid(j.Pipeline) {
		return fmt.Errorf("pipeline id %q must be lowercase letters, digits and hyphens", j.Pipeline)
	}
	if !jobNamePattern.MatchString(j.Name) {
		return fmt.Errorf("job name %q must be lowercase letters, digits, hyphens and underscores", j.Name)
	}
	return nil
}

// Labels are the labels on everything made for the job.
func (j Job) Labels() map[string]string {
	return map[string]string{labelManaged: "true", LabelPipeline: j.Pipeline, LabelJob: j.Name}
}

// NetworkName is the job's network. A job name never has a dot, so no two
// jobs' names collide.
func (j Job) NetworkName() string { return "devpad-job-" + j.Pipeline + "." + j.Name }

// ContainerName is the job's container.
func (j Job) ContainerName() string { return "devpad-job-" + j.Pipeline + "." + j.Name }

// ServiceName is the container of the job's service of serviceType.
func (j Job) ServiceName(serviceType string) string { return j.ContainerName() + "." + serviceType }

// ownsContainer reports whether a container of this name is the job's or
// one of its services'. Images pass their labels on to containers, so a
// label alone could put another container under a job's; names are never
// inherited.
func (j Job) ownsContainer(name string) bool {
	return name == j.ContainerName() || strings.HasPrefix(name, j.ContainerName()+".")
}

// JobFile is a file put in a job's container before it starts, so before
// anything of the job's runs in it, such as the bundle the job clones.
type JobFile struct {
	// Path is absolute, in a directory the image has, such as /tmp.
	Path string
	Mode int64
	// Size is how many bytes Content holds.
	Size    int64
	Content io.Reader
}

// JobContainerSpec describes a job's container.
type JobContainerSpec struct {
	Job Job
	// Image is the project's environment image; empty means WorkspaceImage.
	Image string
	// Env is all the container gets beyond its image's environment: how the
	// job reaches its services.
	Env []string
	// Files are put in, owned by root, before it starts.
	Files []JobFile
	// Limits bound the job; zero means JobLimits.
	Limits Limits
	// Lifetime is how long the container lives. It stops itself then,
	// ending whatever still runs in it, even with no devpad to stop it, so
	// it should cover everything the job runs. Required.
	Lifetime time.Duration
}

func (s *JobContainerSpec) validate() error {
	if err := s.Job.Validate(); err != nil {
		return err
	}
	if s.Limits == (Limits{}) {
		s.Limits = JobLimits
	}
	if s.Limits.Memory <= 0 || s.Limits.NanoCPUs <= 0 || s.Limits.Pids <= 0 {
		return errors.New("a job's container needs memory, CPU and process limits")
	}
	if s.Lifetime < time.Second {
		return errors.New("a job's container needs a lifetime of at least a second")
	}
	for _, f := range s.Files {
		if !path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path || path.Base(f.Path) == "/" || f.Size < 0 || f.Content == nil {
			return fmt.Errorf("a job's file needs a clean absolute path and content, got %q", f.Path)
		}
	}
	return nil
}

func (m *manager) CreateJobNetwork(ctx context.Context, job Job) error {
	if err := job.Validate(); err != nil {
		return err
	}
	// A bridge network like a workspace's, with outbound internet. Devpad
	// never joins it: it reaches the job only through Docker.
	if _, err := m.cli.NetworkCreate(ctx, job.NetworkName(), client.NetworkCreateOptions{
		Driver: "bridge",
		Labels: job.Labels(),
	}); err != nil {
		return jobNetworkError(err)
	}
	return nil
}

// jobNetworkError says why a job's network could not be made, telling
// running out of address pools, which passes, from the rest.
func jobNetworkError(err error) error {
	if strings.Contains(err.Error(), "non-overlapping") && strings.Contains(err.Error(), "address pool") {
		return fmt.Errorf("creating the job's network: %w: %w", ErrNoNetworkPool, err)
	}
	return fmt.Errorf("creating the job's network: %w", err)
}

func (m *manager) StartJobContainer(ctx context.Context, spec JobContainerSpec) (string, error) {
	if err := spec.validate(); err != nil {
		return "", err
	}
	image := spec.Image
	if image == "" {
		image = WorkspaceImage
	}
	net := spec.Job.NetworkName()
	withInit := true
	resp, err := m.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: spec.Job.ContainerName(),
		Config: &container.Config{
			Image:      image,
			User:       JobUser,
			WorkingDir: "/workspace",
			Env:        spec.Env,
			Labels:     spec.Job.Labels(),
			// Not the agent: the container only waits for the commands
			// devpad runs in it, and stops itself when its time is up.
			Entrypoint: []string{"sleep"},
			Cmd:        []string{strconv.FormatInt(int64((spec.Lifetime+time.Second-1)/time.Second), 10)},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:   container.NetworkMode(net),
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			// Docker's init runs as PID 1 and reaps the processes a job
			// orphans, which would otherwise fill its process limit.
			Init: &withInit,
			// The job's code was written by the agent it grades: it gets no
			// way to root, through sudo or a setuid binary, and no
			// capabilities to use as root if it found one.
			SecurityOpt: []string{"no-new-privileges:true"},
			CapDrop:     []string{"ALL"},
			Resources:   spec.Limits.resources(),
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{net: {}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating the job's container: %w", err)
	}
	fail := func(err error) (string, error) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), JobCleanupTimeout)
		defer cancel()
		_ = m.removeJobContainer(cleanupCtx, resp.ID)
		return "", err
	}
	// Copying into a container whose processes the job controls has been a
	// way out of it, so it is done only now, before anything runs in it.
	if err := m.writeLoginPath(ctx, resp.ID); err != nil {
		return fail(err)
	}
	for _, f := range spec.Files {
		archive := fileArchive(path.Base(f.Path), f.Mode, f.Size, f.Content)
		_, err := m.cli.CopyToContainer(ctx, resp.ID, client.CopyToContainerOptions{DestinationPath: path.Dir(f.Path), Content: archive})
		archive.Close()
		if err != nil {
			return fail(fmt.Errorf("putting %s in the job's container: %w", f.Path, err))
		}
	}
	if _, err := m.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return fail(fmt.Errorf("starting the job's container: %w", err))
	}
	return resp.ID, nil
}

// writeLoginPath keeps the container's PATH in its login shells, which the
// job runs in, as the agent does in a workspace's.
func (m *manager) writeLoginPath(ctx context.Context, containerID string) error {
	env, err := m.GetEnv(ctx, containerID)
	if err != nil {
		return err
	}
	var path string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	if path == "" {
		return nil
	}
	if err := m.CopyFileToContainer(ctx, containerID, loginpath.File[1:], loginpath.Script(path), 0o644); err != nil {
		return fmt.Errorf("writing the job's login PATH: %w", err)
	}
	return nil
}

// JobResourceKind is what a JobResource is.
type JobResourceKind string

const (
	JobResourceContainer JobResourceKind = "container"
	JobResourceNetwork   JobResourceKind = "network"
)

// JobResource is a container or network made for a pipeline job, as Docker
// lists it by its labels.
type JobResource struct {
	Kind JobResourceKind
	ID   string
	Name string
	Job  Job
	// Sidecar says a container is one of the job's services.
	Sidecar bool
}

// jobFilters selects by labels the resources of sel: every pipeline's, one
// pipeline's, or one job's.
func jobFilters(sel Job) (client.Filters, error) {
	f := make(client.Filters)
	switch {
	case sel.Pipeline == "" && sel.Name == "":
		return f.Add("label", LabelPipeline), nil
	case sel.Pipeline == "":
		return nil, errors.New("a job is selected with its pipeline")
	case !pipelineid.Valid(sel.Pipeline):
		return nil, fmt.Errorf("pipeline id %q must be lowercase letters, digits and hyphens", sel.Pipeline)
	case sel.Name == "":
		return f.Add("label", LabelPipeline+"="+sel.Pipeline), nil
	default:
		if err := sel.Validate(); err != nil {
			return nil, err
		}
		// Docker matches every label filter given.
		return f.Add("label", LabelPipeline+"="+sel.Pipeline, LabelJob+"="+sel.Name), nil
	}
}

// jobResource is what Docker listed, when it is a job's: labelled with a job
// Docker can name, and named as that job's. Anything else carrying the
// labels, such as a container whose image had them, is not listed, and so
// never removed with a job.
func jobResource(kind JobResourceKind, id, name string, labels map[string]string) (JobResource, bool) {
	job := Job{Pipeline: labels[LabelPipeline], Name: labels[LabelJob]}
	if job.Validate() != nil {
		return JobResource{}, false
	}
	switch kind {
	case JobResourceContainer:
		if !job.ownsContainer(name) {
			return JobResource{}, false
		}
	case JobResourceNetwork:
		if name != job.NetworkName() {
			return JobResource{}, false
		}
	}
	return JobResource{Kind: kind, ID: id, Name: name, Job: job, Sidecar: kind == JobResourceContainer && labels[labelSidecar] == "true"}, true
}

func (m *manager) ListJobResources(ctx context.Context, sel Job) ([]JobResource, error) {
	filters, err := jobFilters(sel)
	if err != nil {
		return nil, err
	}
	containers, err := m.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing job containers: %w", err)
	}
	networks, err := m.cli.NetworkList(ctx, client.NetworkListOptions{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing job networks: %w", err)
	}
	resources := make([]JobResource, 0, len(containers.Items)+len(networks.Items))
	for _, c := range containers.Items {
		for _, name := range c.Names {
			if r, ok := jobResource(JobResourceContainer, c.ID, strings.TrimPrefix(name, "/"), c.Labels); ok {
				resources = append(resources, r)
				break
			}
		}
	}
	for _, n := range networks.Items {
		if r, ok := jobResource(JobResourceNetwork, n.ID, n.Name, n.Labels); ok {
			resources = append(resources, r)
		}
	}
	return resources, nil
}

func (m *manager) RemoveJobResources(ctx context.Context, sel Job) error {
	if sel.Pipeline == "" {
		return errors.New("removing job resources needs a pipeline")
	}
	resources, err := m.ListJobResources(ctx, sel)
	if err != nil {
		return err
	}
	var errs []error
	// Containers first: Docker refuses to remove a network they are on.
	for _, r := range resources {
		if r.Kind == JobResourceContainer {
			if err := m.removeJobContainer(ctx, r.ID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, r := range resources {
		if r.Kind != JobResourceNetwork {
			continue
		}
		if _, err := m.cli.NetworkRemove(ctx, r.ID, client.NetworkRemoveOptions{}); err != nil && !isNotFound(err) {
			errs = append(errs, fmt.Errorf("removing network %s: %w", r.Name, err))
		}
	}
	return errors.Join(errs...)
}

// removeJobContainer removes a job's container, or one of its services',
// with the anonymous volumes its image declared: a project's environment can
// declare one, and nothing in it outlives the job.
func (m *manager) removeJobContainer(ctx context.Context, containerID string) error {
	if _, err := m.cli.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !isNotFound(err) {
		return fmt.Errorf("removing container %s: %w", shortID(containerID), err)
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// isNotFound reports whether Docker said there is no such object.
func isNotFound(err error) bool { return cerrdefs.IsNotFound(err) }
