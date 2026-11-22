package wsservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/devpad-org/devpad/internal/container"
)

// ErrNotProjectService is returned when a pipeline job names a service its
// project does not have.
var ErrNotProjectService = errors.New("the project has no such service")

// jobCleanupTimeout bounds removing a job's services after they failed to
// start.
const jobCleanupTimeout = 30 * time.Second

// JobServicesRequest asks for fresh copies of a project's services for one
// pipeline job.
type JobServicesRequest struct {
	ProjectID int64
	// Types are the job's services, as its pipeline names them.
	Types []ServiceType
	// Job names and labels the copies, which join its network.
	Job container.Job
}

// JobServices are a job's copies of its project's services, started and
// ready.
type JobServices struct {
	// Env is how the job reaches them: each service's usual connection
	// variables, for its container's environment.
	Env []string
}

// StartJobServices starts a fresh copy of each service a pipeline job names
// on the job's network, and waits until each passes its health check. Every
// copy is the job's alone, with a password of its own and no volume, and
// nothing records it but its labels: a job is not a workspace, so it has no
// workspace_services rows. It refuses, before starting any, a service the
// project does not have, one this Docker host cannot run, and one whose
// licence terms changed since the project accepted them. If one fails it
// removes them all; RemoveJobServices, or removing the job's resources,
// removes them otherwise.
//
// On the job's network each copy goes by its type, such as postgres, which
// its connection variables name as its host.
func (s *service) StartJobServices(ctx context.Context, req JobServicesRequest) (*JobServices, error) {
	if err := req.Job.Validate(); err != nil {
		return nil, err
	}
	if len(req.Types) == 0 {
		return &JobServices{}, nil
	}
	copies, err := s.planJobServices(ctx, req)
	if err != nil {
		return nil, err
	}
	services, err := s.startJobServices(ctx, req.Job, copies)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), jobCleanupTimeout)
		defer cancel()
		if rmErr := s.RemoveJobServices(cleanupCtx, req.Job); rmErr != nil {
			log.Printf("pipeline %s job %s: removing its services after they failed: %v", req.Job.Pipeline, req.Job.Name, rmErr)
		}
		return nil, err
	}
	return services, nil
}

// jobService is a copy of a service a job is to get.
type jobService struct {
	serviceType ServiceType
	def         definition
	cfg         ServiceConfig
}

// planJobServices checks that the job can have each of its services, and
// makes their configurations.
func (s *service) planJobServices(ctx context.Context, req JobServicesRequest) ([]jobService, error) {
	projectServices, err := s.repo.ListProjectServices(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	byType := make(map[ServiceType]*ProjectService, len(projectServices))
	for _, ps := range projectServices {
		byType[ps.ServiceType] = ps
	}
	copies := make([]jobService, 0, len(req.Types))
	seen := make(map[ServiceType]bool, len(req.Types))
	for _, t := range req.Types {
		if seen[t] {
			return nil, fmt.Errorf("the job names the %s service twice", t)
		}
		seen[t] = true
		ps, ok := byType[t]
		if !ok {
			if !ValidServiceType(t) {
				return nil, fmt.Errorf("%s: %w", t, ErrInvalidType)
			}
			return nil, fmt.Errorf("%s: %w", t, ErrNotProjectService)
		}
		def, cfg, err := newConfig(t, ps.LicenseAcceptance)
		if errors.Is(err, ErrLicenseOutdated) {
			return nil, fmt.Errorf("the project's %s service: %w; remove it from the project's services and add it again to accept the new terms", t, err)
		}
		if err != nil {
			return nil, fmt.Errorf("the project's %s service: %w", t, err)
		}
		if err := s.checkArch(ctx, t, def); err != nil {
			return nil, fmt.Errorf("the project's %s service: %w", t, err)
		}
		copies = append(copies, jobService{serviceType: t, def: def, cfg: cfg})
	}
	return copies, nil
}

// startJobServices creates and starts each copy, then waits for them all.
func (s *service) startJobServices(ctx context.Context, job container.Job, copies []jobService) (*JobServices, error) {
	result := &JobServices{}
	started := make([]startedService, 0, len(copies))
	for _, c := range copies {
		spec, err := sidecarSpec(c.def, c.cfg)
		if err != nil {
			return nil, err
		}
		host := string(c.serviceType)
		spec.Name = job.ServiceName(host)
		spec.Network = job.NetworkName()
		spec.NetworkAlias = host
		spec.Labels = job.Labels()
		// A copy that exits fails the job rather than coming back with
		// its data gone, and one a stopped devpad leaves stays stopped.
		spec.NoRestart = true
		containerID, err := s.container.CreateSidecar(ctx, spec)
		if err != nil {
			return nil, fmt.Errorf("creating the %s service: %w", c.serviceType, err)
		}
		if err := s.container.Start(ctx, containerID); err != nil {
			return nil, fmt.Errorf("starting the %s service: %w", c.serviceType, err)
		}
		started = append(started, startedService{containerID, c.serviceType})
		result.Env = append(result.Env, c.def.connectionVars(c.serviceType, host, c.cfg)...)
	}
	if err := s.awaitAll(ctx, started); err != nil {
		return nil, err
	}
	return result, nil
}

// RemoveJobServices removes a pipeline job's services, found by its labels,
// with whatever their images keep in anonymous volumes.
func (s *service) RemoveJobServices(ctx context.Context, job container.Job) error {
	if err := job.Validate(); err != nil {
		return err
	}
	resources, err := s.container.ListJobResources(ctx, job)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range resources {
		if r.Kind == container.JobContainer && r.Sidecar {
			if err := s.container.RemoveSidecar(ctx, r.ID); err != nil {
				errs = append(errs, fmt.Errorf("removing %s: %w", r.Name, err))
			}
		}
	}
	return errors.Join(errs...)
}
