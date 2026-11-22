package container

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/devpad-org/devpad/internal/loginpath"
)

// A pipeline job runs in a container of its own, made from the project's
// environment image but with no agent: its entrypoint only sleeps, and devpad
// runs the job's commands in it with ExecJob and reads their exit codes from
// Docker. It is on a network of its own with the job's services, which
// devpad never joins, and gets nothing of devpad's: no agent token, SSH keys
// or project settings, only the job's services' connection variables.
//
// Everything made for a job carries its labels, so whatever a job leaves
// behind, even after a restart or a create that failed part way, can be
// found and removed by them.

// Labels on everything made for a pipeline job.
const (
	LabelPipeline = "devpad.pipeline"
	LabelJob      = "devpad.job"
	labelManaged  = "devpad.managed"
	labelSidecar  = "devpad.sidecar"
)

// JobUser is the user a job's container and commands run as.
const JobUser = "devpad"

var (
	// pipelineIDPattern matches the ids pipelines go by, the agent's own rule
	// for its refs.
	pipelineIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	// jobNamePattern is looser than the pipeline file's rule, and keeps
	// names safe in Docker's: no dots, which separate them.
	jobNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// Job names one job of one pipeline to Docker.
type Job struct {
	Pipeline string
	Name     string
}

// Validate refuses a job whose pipeline id or name Docker's names and
// labels cannot carry.
func (j Job) Validate() error {
	if !pipelineIDPattern.MatchString(j.Pipeline) {
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

// JobContainerSpec describes a job's container.
type JobContainerSpec struct {
	Job Job
	// Image is the project's environment image; empty means WorkspaceImage.
	Image string
	// Env is all the container gets beyond its image's environment: how the
	// job reaches its services.
	Env []string
	// Memory, NanoCPUs and Pids bound the job: memory with no swap, CPU, and
	// the processes and threads it runs at once. Each is required.
	Memory   int64
	NanoCPUs int64
	Pids     int64
	// Lifetime is how long the container lives. It stops itself then,
	// ending whatever still runs in it, even with no devpad to stop it, so
	// it should cover everything the job runs. Required.
	Lifetime time.Duration
}

func (s JobContainerSpec) validate() error {
	if err := s.Job.Validate(); err != nil {
		return err
	}
	if s.Memory <= 0 || s.NanoCPUs <= 0 || s.Pids <= 0 {
		return errors.New("a job's container needs memory, CPU and process limits")
	}
	if s.Lifetime < time.Second {
		return errors.New("a job's container needs a lifetime of at least a second")
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
		return fmt.Errorf("creating the job's network: %w", err)
	}
	return nil
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
	pids := spec.Pids
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
			Resources: container.Resources{
				Memory:     spec.Memory,
				MemorySwap: spec.Memory,
				NanoCPUs:   spec.NanoCPUs,
				PidsLimit:  &pids,
			},
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{net: {}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating the job's container: %w", err)
	}
	fail := func(err error) (string, error) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), jobCleanupTimeout)
		defer cancel()
		_ = m.removeJobContainer(cleanupCtx, resp.ID)
		return "", err
	}
	if err := m.writeLoginPath(ctx, resp.ID); err != nil {
		return fail(err)
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
	JobContainer JobResourceKind = "container"
	JobNetwork   JobResourceKind = "network"
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

// jobCleanupTimeout bounds removing what a job's container start left.
const jobCleanupTimeout = 30 * time.Second

// jobFilters selects by labels the resources of sel: every pipeline's, one
// pipeline's, or one job's.
func jobFilters(sel Job) (client.Filters, error) {
	f := make(client.Filters)
	switch {
	case sel.Pipeline == "" && sel.Name == "":
		return f.Add("label", LabelPipeline), nil
	case sel.Pipeline == "":
		return nil, errors.New("a job is selected with its pipeline")
	case !pipelineIDPattern.MatchString(sel.Pipeline):
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
		var name string
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		resources = append(resources, JobResource{
			Kind:    JobContainer,
			ID:      c.ID,
			Name:    name,
			Job:     Job{Pipeline: c.Labels[LabelPipeline], Name: c.Labels[LabelJob]},
			Sidecar: c.Labels[labelSidecar] == "true",
		})
	}
	for _, n := range networks.Items {
		resources = append(resources, JobResource{
			Kind: JobNetwork,
			ID:   n.ID,
			Name: n.Name,
			Job:  Job{Pipeline: n.Labels[LabelPipeline], Name: n.Labels[LabelJob]},
		})
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
		if r.Kind == JobContainer {
			if err := m.removeJobContainer(ctx, r.ID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, r := range resources {
		if r.Kind != JobNetwork {
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

// isNotFound reports whether Docker said there is no such object, as the
// errors it answers with for one mark themselves.
func isNotFound(err error) bool {
	var notFound interface{ NotFound() }
	return errors.As(err, &notFound)
}
