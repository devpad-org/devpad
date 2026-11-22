package wsservice

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/devpad-org/devpad/internal/container"
)

// ErrNotProjectService is returned when a pipeline job names a service its
// project does not have.
var ErrNotProjectService = errors.New("the project has no such service")

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
	// containers are the copies' containers.
	containers []string
}

// StartJobServices starts a fresh copy of each service a pipeline job names
// on the job's network, and waits until each passes its health check. Every
// copy is the job's alone, with a password of its own, no volume and
// container.JobServiceLimits, and nothing records it but its labels: a job
// is not a workspace, so it has no workspace_services rows. It refuses,
// before starting any, a service the project does not have, one this Docker
// host cannot run, and one whose licence terms changed since the project
// accepted them. If one fails it removes those it made; a create cut short
// can still leave one, which removing the job's resources finds by its
// labels, as it finds them all.
//
// On the job's network each copy goes by its type, such as postgres, which
// its connection variables name as its host.
func (s *service) StartJobServices(ctx context.Context, req JobServicesRequest) (*JobServices, error) {
	if err := req.Job.Validate(); err != nil {
		return nil, err
	}
	services := &JobServices{}
	if len(req.Types) == 0 {
		return services, nil
	}
	copies, err := s.planJobServices(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.startJobServices(ctx, req.Job, copies, services); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), container.JobCleanupTimeout)
		defer cancel()
		if rmErr := s.RemoveJobServices(cleanupCtx, services); rmErr != nil {
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
			return nil, outdatedLicenseError("copying", t, err)
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

// startJobServices creates and starts each copy, recording each in
// services as it is made, then waits for them all.
func (s *service) startJobServices(ctx context.Context, job container.Job, copies []jobService, services *JobServices) error {
	started := make([]startedService, 0, len(copies))
	for _, c := range copies {
		spec, err := sidecarSpec(c.def, c.cfg)
		if err != nil {
			return err
		}
		host := string(c.serviceType)
		spec.Name = job.ServiceName(host)
		spec.Network = job.NetworkName()
		spec.NetworkAlias = host
		spec.Labels = job.Labels()
		spec.Limits = jobServiceLimits(c.def)
		// A copy that exits fails the job rather than coming back with
		// its data gone, and one a stopped devpad leaves stays stopped.
		spec.NoRestart = true
		containerID, err := s.container.CreateSidecar(ctx, spec)
		if err != nil {
			return fmt.Errorf("creating the %s service: %w", c.serviceType, err)
		}
		services.containers = append(services.containers, containerID)
		if err := s.container.Start(ctx, containerID); err != nil {
			return fmt.Errorf("starting the %s service: %w", c.serviceType, err)
		}
		started = append(started, startedService{containerID, c.serviceType})
		services.Env = append(services.Env, c.def.connectionVars(c.serviceType, host, c.cfg)...)
	}
	return s.awaitAll(ctx, started)
}

// jobServiceLimits bound a job's copy of a service: a job's services'
// limits, with more memory for one that needs it.
func jobServiceLimits(def definition) container.Limits {
	limits := container.JobServiceLimits
	limits.Memory = max(limits.Memory, def.minMemory)
	return limits
}

// RemoveJobServices removes the services StartJobServices started, with
// whatever their images keep in anonymous volumes.
func (s *service) RemoveJobServices(ctx context.Context, services *JobServices) error {
	if services == nil {
		return nil
	}
	var errs []error
	for _, id := range services.containers {
		if err := s.container.RemoveSidecar(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
