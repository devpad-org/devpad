package wsservice

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/container"
)

var (
	ErrNotFound      = errors.New("service not found")
	ErrDuplicateType = errors.New("workspace already has a service of this type")
	ErrInvalidType   = errors.New("unsupported service type")
	// ErrLicenseNotAccepted is returned when a service with licence terms
	// is created without the user accepting them, or would get a container
	// without a record of their acceptance.
	ErrLicenseNotAccepted = errors.New("the service's licence terms were not accepted")
	// ErrUnsupportedArch is returned when the Docker daemon's architecture
	// cannot run a service's image.
	ErrUnsupportedArch = errors.New("the service's image does not support this Docker host's architecture")
)

// CreateOptions carries who is creating a service and what they agreed to.
type CreateOptions struct {
	UserID int64
	// AcceptLicense is the user's acceptance of the service's licence
	// terms, required for a service that has them (see Catalog).
	AcceptLicense bool
}

// Service defines workspace service business logic.
type Service interface {
	Create(ctx context.Context, workspaceID int64, serviceType ServiceType, opts CreateOptions) (*WorkspaceService, error)
	// Catalog lists every service type a workspace can deploy, sorted, with
	// its licence terms and whether this Docker host can run it.
	Catalog(ctx context.Context) []CatalogEntry
	List(ctx context.Context, workspaceID int64) ([]*WorkspaceService, error)
	Get(ctx context.Context, id int64) (*WorkspaceService, error)
	Delete(ctx context.Context, id int64) error

	// Start starts a single service container and attaches it to the given network.
	Start(ctx context.Context, serviceID int64, networkName string) (*WorkspaceService, error)
	// Stop stops a single service container.
	Stop(ctx context.Context, serviceID int64) (*WorkspaceService, error)

	// Lifecycle methods called by the workspace service during start/stop/delete.
	StartAll(ctx context.Context, workspaceID int64, networkName string) error
	StopAll(ctx context.Context, workspaceID int64) error
	DeleteAll(ctx context.Context, workspaceID int64) error

	// EnvVars returns connection info env vars for all services in a workspace.
	EnvVars(ctx context.Context, workspaceID int64, networkName string) ([]string, error)

	// ProjectServices lists the services each new workspace of a project
	// gets its own copy of.
	ProjectServices(ctx context.Context, projectID int64) ([]*ProjectService, error)
	// AddProjectService adds a service to a project's list. Workspaces that
	// already exist are not changed; the project workspace gets it when it
	// is rebuilt.
	AddProjectService(ctx context.Context, projectID int64, serviceType ServiceType, opts CreateOptions) (*ProjectService, error)
	// RemoveProjectService removes a service from a project's list. The
	// copies workspaces already have stay until they are deleted.
	RemoveProjectService(ctx context.Context, projectID int64, serviceType ServiceType) error
	// Provision gives a workspace of a project its own copy of each of the
	// project's services it does not have yet. With start, it starts them;
	// otherwise it only creates their containers, so that a workspace
	// container made next is given their connection details.
	Provision(ctx context.Context, workspaceID, projectID int64, networkName string, start bool) error
	// AwaitReady waits until each running service of a workspace passes its
	// health check.
	AwaitReady(ctx context.Context, workspaceID int64) error
	// AdoptWorkspaceServices adds the services a workspace has to its
	// project's list, for a workspace that became a project's workspace.
	AdoptWorkspaceServices(ctx context.Context, workspaceID, projectID int64) error
}

// Provision waits up to readyTimeout for each new service to be ready,
// checking every readyPoll. The longest wait is for SQL Server's first
// start.
const (
	defaultReadyTimeout = 4 * time.Minute
	defaultReadyPoll    = time.Second
)

type service struct {
	repo      Repository
	container container.Manager
	now       func() time.Time

	readyTimeout time.Duration
	readyPoll    time.Duration

	// arch caches the Docker daemon's architecture once read.
	archMu sync.Mutex
	arch   string
}

// NewService creates a new workspace service Service.
func NewService(repo Repository, cm container.Manager) Service {
	return &service{
		repo:         repo,
		container:    cm,
		now:          time.Now,
		readyTimeout: defaultReadyTimeout,
		readyPoll:    defaultReadyPoll,
	}
}

func (s *service) Create(ctx context.Context, workspaceID int64, serviceType ServiceType, opts CreateOptions) (*WorkspaceService, error) {
	acceptance, err := s.checkDeployable(ctx, serviceType, opts)
	if err != nil {
		return nil, err
	}
	return s.create(ctx, workspaceID, serviceType, acceptance)
}

// checkDeployable refuses a service devpad cannot deploy for the user: an
// unknown type, one this Docker host cannot run, or one whose licence terms
// they did not accept. It returns their acceptance for a service that has
// terms.
func (s *service) checkDeployable(ctx context.Context, serviceType ServiceType, opts CreateOptions) (*LicenseAcceptance, error) {
	def, ok := lookupDefinition(serviceType)
	if !ok {
		return nil, ErrInvalidType
	}
	if err := s.checkArch(ctx, serviceType, def); err != nil {
		return nil, err
	}
	if def.license == nil {
		return nil, nil
	}
	if !opts.AcceptLicense {
		return nil, ErrLicenseNotAccepted
	}
	return &LicenseAcceptance{
		UserID:     opts.UserID,
		AcceptedAt: s.now().UTC(),
		TermsURL:   def.license.URL,
		Image:      def.defaults.Image,
	}, nil
}

// create adds a stopped service of serviceType to a workspace, with a new
// password and volume, deployed under acceptance when it has licence terms.
func (s *service) create(ctx context.Context, workspaceID int64, serviceType ServiceType, acceptance *LicenseAcceptance) (*WorkspaceService, error) {
	def, ok := lookupDefinition(serviceType)
	if !ok {
		return nil, ErrInvalidType
	}
	if def.license != nil && acceptance == nil {
		return nil, ErrLicenseNotAccepted
	}

	// Check for duplicate service type on this workspace.
	existing, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	for _, svc := range existing {
		if svc.ServiceType == serviceType {
			return nil, ErrDuplicateType
		}
	}

	cfg := def.defaults
	cfg.DefaultPass = generatePassword(24)
	if def.license != nil {
		a := *acceptance
		cfg.LicenseAcceptance = &a
	}
	configJSON, _ := json.Marshal(cfg)

	volumeName := fmt.Sprintf("devpad-svc-%d-%s", workspaceID, serviceType)
	if err := s.container.CreateVolume(ctx, volumeName); err != nil {
		return nil, fmt.Errorf("creating service volume: %w", err)
	}

	svc := &WorkspaceService{
		WorkspaceID: workspaceID,
		ServiceType: serviceType,
		VolumeName:  volumeName,
		Status:      StatusStopped,
		Config:      string(configJSON),
	}
	if err := s.repo.Create(ctx, svc); err != nil {
		_ = s.container.RemoveVolume(ctx, volumeName)
		return nil, fmt.Errorf("creating service record: %w", err)
	}
	return svc, nil
}

// checkArch refuses a service whose image the Docker daemon cannot run.
func (s *service) checkArch(ctx context.Context, serviceType ServiceType, def definition) error {
	want := def.requiredArch
	if want == "" {
		return nil
	}
	arch, err := s.daemonArch(ctx)
	if err != nil {
		return fmt.Errorf("checking docker architecture: %w", err)
	}
	if arch != want {
		return fmt.Errorf("%w: %s needs %s, the Docker host is %s", ErrUnsupportedArch, serviceType, want, arch)
	}
	return nil
}

// daemonArch returns the Docker daemon's architecture, asking Docker only
// until it answers: the architecture cannot change while devpad runs.
func (s *service) daemonArch(ctx context.Context) (string, error) {
	s.archMu.Lock()
	defer s.archMu.Unlock()
	if s.arch != "" {
		return s.arch, nil
	}
	arch, err := s.container.DaemonArch(ctx)
	if err != nil {
		return "", err
	}
	s.arch = arch
	return arch, nil
}

func (s *service) Catalog(ctx context.Context) []CatalogEntry {
	catalog := make([]CatalogEntry, 0, len(definitions))
	var arch string
	archKnown := true
	for _, t := range SupportedTypes() {
		serviceType := ServiceType(t)
		def := definitions[serviceType]
		entry := CatalogEntry{Type: serviceType, License: def.catalogLicense()}
		// Docker is asked only when some service needs a particular
		// architecture. If it cannot say, Create refuses with the reason.
		if def.requiredArch != "" && archKnown {
			if arch == "" {
				var err error
				if arch, err = s.daemonArch(ctx); err != nil {
					log.Printf("checking docker architecture: %v", err)
					archKnown = false
				}
			}
			if archKnown && arch != def.requiredArch {
				entry.Unavailable = fmt.Sprintf("needs an %s Docker host; this one is %s", def.requiredArch, arch)
			}
		}
		catalog = append(catalog, entry)
	}
	return catalog
}

func (s *service) List(ctx context.Context, workspaceID int64) ([]*WorkspaceService, error) {
	services, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	return services, nil
}

func (s *service) Get(ctx context.Context, id int64) (*WorkspaceService, error) {
	svc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting service: %w", err)
	}
	if svc == nil {
		return nil, ErrNotFound
	}
	return svc, nil
}

func (s *service) Delete(ctx context.Context, id int64) error {
	svc, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	// Stop and remove the container if running.
	if svc.ContainerID != "" {
		_ = s.container.Stop(ctx, svc.ContainerID)
		if err := s.container.RemoveSidecar(ctx, svc.ContainerID); err != nil {
			log.Printf("service %d: failed to remove container: %v", svc.ID, err)
		}
	}

	if svc.VolumeName != "" {
		if err := s.container.RemoveVolume(ctx, svc.VolumeName); err != nil {
			log.Printf("service %d: failed to remove volume: %v", svc.ID, err)
		}
	}

	if err := s.repo.Delete(ctx, svc.ID); err != nil {
		return fmt.Errorf("deleting service record: %w", err)
	}
	return nil
}

func (s *service) StartAll(ctx context.Context, workspaceID int64, networkName string) error {
	services, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("listing services: %w", err)
	}

	for _, svc := range services {
		if err := s.startOne(ctx, svc, networkName); err != nil {
			log.Printf("service %d (%s): start failed: %v", svc.ID, svc.ServiceType, err)
		}
	}
	return nil
}

func (s *service) StopAll(ctx context.Context, workspaceID int64) error {
	services, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("listing services: %w", err)
	}

	for _, svc := range services {
		if err := s.stopOne(ctx, svc); err != nil {
			log.Printf("service %d (%s): stop failed: %v", svc.ID, svc.ServiceType, err)
		}
	}
	return nil
}

func (s *service) DeleteAll(ctx context.Context, workspaceID int64) error {
	services, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("listing services: %w", err)
	}

	for _, svc := range services {
		if err := s.Delete(ctx, svc.ID); err != nil {
			log.Printf("service %d (%s): delete failed: %v", svc.ID, svc.ServiceType, err)
		}
	}
	return nil
}

func (s *service) EnvVars(ctx context.Context, workspaceID int64, networkName string) ([]string, error) {
	services, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	var envs []string
	for _, svc := range services {
		// A container's environment is fixed for its life, so it names
		// every service the workspace may use, stopped ones too: a service
		// started later is then reachable without a new container.
		if svc.ContainerID == "" {
			continue
		}

		var cfg ServiceConfig
		if err := json.Unmarshal([]byte(svc.Config), &cfg); err != nil {
			continue
		}

		// Use the container name as the hostname — Docker DNS resolves it
		// within the shared network.
		host := sidecarContainerName(workspaceID, svc.ServiceType)

		def, ok := lookupDefinition(svc.ServiceType)
		if !ok {
			continue
		}
		envs = append(envs, def.connectionVars(svc.ServiceType, host, cfg)...)
	}
	return envs, nil
}

func (s *service) Start(ctx context.Context, serviceID int64, networkName string) (*WorkspaceService, error) {
	svc, err := s.Get(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if err := s.startOne(ctx, svc, networkName); err != nil {
		return nil, err
	}
	return svc, nil
}

func (s *service) Stop(ctx context.Context, serviceID int64) (*WorkspaceService, error) {
	svc, err := s.Get(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if err := s.stopOne(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

func (s *service) startOne(ctx context.Context, svc *WorkspaceService, networkName string) error {
	if svc.Status == StatusRunning {
		return nil
	}
	if err := s.ensureContainer(ctx, svc, networkName); err != nil {
		return err
	}

	if err := s.container.Start(ctx, svc.ContainerID); err != nil {
		return fmt.Errorf("starting sidecar container: %w", err)
	}

	svc.Status = StatusRunning
	if err := s.repo.Update(ctx, svc); err != nil {
		return fmt.Errorf("updating service status: %w", err)
	}

	log.Printf("workspace %d: started %s service", svc.WorkspaceID, svc.ServiceType)
	return nil
}

// ensureContainer creates the service's container if it has none yet.
func (s *service) ensureContainer(ctx context.Context, svc *WorkspaceService, networkName string) error {
	if svc.ContainerID != "" {
		return nil
	}
	var cfg ServiceConfig
	if err := json.Unmarshal([]byte(svc.Config), &cfg); err != nil {
		return fmt.Errorf("parsing service config: %w", err)
	}
	def, ok := lookupDefinition(svc.ServiceType)
	if !ok {
		return ErrInvalidType
	}
	env, err := def.containerEnv(cfg)
	if err != nil {
		return err
	}
	var healthcheck []string
	if def.healthcheck != nil {
		healthcheck = def.healthcheck(cfg)
	}
	containerID, err := s.container.CreateSidecar(ctx, container.SidecarSpec{
		Name:        sidecarContainerName(svc.WorkspaceID, svc.ServiceType),
		Image:       cfg.Image,
		Network:     networkName,
		Env:         env,
		Volume:      svc.VolumeName,
		DataDir:     def.dataDir,
		Healthcheck: healthcheck,
	})
	if err != nil {
		return fmt.Errorf("creating sidecar container: %w", err)
	}
	svc.ContainerID = containerID
	if err := s.repo.Update(ctx, svc); err != nil {
		return fmt.Errorf("recording sidecar container: %w", err)
	}
	return nil
}

func (s *service) stopOne(ctx context.Context, svc *WorkspaceService) error {
	if svc.Status == StatusStopped {
		return nil
	}

	if svc.ContainerID != "" {
		if err := s.container.Stop(ctx, svc.ContainerID); err != nil {
			return fmt.Errorf("stopping sidecar container: %w", err)
		}
	}

	svc.Status = StatusStopped
	if err := s.repo.Update(ctx, svc); err != nil {
		return fmt.Errorf("updating service status: %w", err)
	}
	return nil
}

func (s *service) ProjectServices(ctx context.Context, projectID int64) ([]*ProjectService, error) {
	return s.repo.ListProjectServices(ctx, projectID)
}

func (s *service) AddProjectService(ctx context.Context, projectID int64, serviceType ServiceType, opts CreateOptions) (*ProjectService, error) {
	acceptance, err := s.checkDeployable(ctx, serviceType, opts)
	if err != nil {
		return nil, err
	}
	ps := &ProjectService{ProjectID: projectID, ServiceType: serviceType, LicenseAcceptance: acceptance}
	if err := s.repo.AddProjectService(ctx, ps); err != nil {
		return nil, err
	}
	return ps, nil
}

func (s *service) RemoveProjectService(ctx context.Context, projectID int64, serviceType ServiceType) error {
	return s.repo.RemoveProjectService(ctx, projectID, serviceType)
}

func (s *service) Provision(ctx context.Context, workspaceID, projectID int64, networkName string, start bool) error {
	wanted, err := s.repo.ListProjectServices(ctx, projectID)
	if err != nil {
		return err
	}
	if len(wanted) == 0 {
		return nil
	}
	existing, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("listing services: %w", err)
	}
	have := make(map[ServiceType]bool, len(existing))
	for _, svc := range existing {
		have[svc.ServiceType] = true
	}

	for _, ps := range wanted {
		if have[ps.ServiceType] {
			continue
		}
		def, ok := lookupDefinition(ps.ServiceType)
		if !ok {
			return fmt.Errorf("the project's %s service: %w", ps.ServiceType, ErrInvalidType)
		}
		if err := s.checkArch(ctx, ps.ServiceType, def); err != nil {
			return fmt.Errorf("the project's %s service: %w", ps.ServiceType, err)
		}
		svc, err := s.create(ctx, workspaceID, ps.ServiceType, ps.LicenseAcceptance)
		if err != nil {
			return fmt.Errorf("adding the project's %s service: %w", ps.ServiceType, err)
		}
		if start {
			err = s.startOne(ctx, svc, networkName)
		} else {
			err = s.ensureContainer(ctx, svc, networkName)
		}
		if err != nil {
			return fmt.Errorf("deploying the project's %s service: %w", ps.ServiceType, err)
		}
	}
	return nil
}

func (s *service) AwaitReady(ctx context.Context, workspaceID int64) error {
	services, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("listing services: %w", err)
	}
	// They were all started already, so they get ready side by side and
	// this waits about as long as the slowest.
	for _, svc := range services {
		if svc.Status != StatusRunning || svc.ContainerID == "" {
			continue
		}
		if err := s.awaitReady(ctx, svc); err != nil {
			return err
		}
	}
	return nil
}

// awaitReady waits until a started service's health check passes. A
// service whose container has no health check is ready once it runs.
func (s *service) awaitReady(ctx context.Context, svc *WorkspaceService) error {
	ctx, cancel := context.WithTimeout(ctx, s.readyTimeout)
	defer cancel()
	ticker := time.NewTicker(s.readyPoll)
	defer ticker.Stop()
	for {
		h, err := s.container.SidecarHealth(ctx, svc.ContainerID)
		if err != nil {
			if ctx.Err() == nil {
				return fmt.Errorf("checking the %s service: %w", svc.ServiceType, err)
			}
		} else if h.Running {
			switch h.Status {
			case "healthy", "none":
				return nil
			case "unhealthy":
				return fmt.Errorf("the %s service started but is not accepting connections", svc.ServiceType)
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("the %s service was not ready after %s", svc.ServiceType, s.readyTimeout)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *service) AdoptWorkspaceServices(ctx context.Context, workspaceID, projectID int64) error {
	services, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("listing services: %w", err)
	}
	for _, svc := range services {
		var cfg ServiceConfig
		if err := json.Unmarshal([]byte(svc.Config), &cfg); err != nil {
			return fmt.Errorf("parsing the %s service's config: %w", svc.ServiceType, err)
		}
		ps := &ProjectService{ProjectID: projectID, ServiceType: svc.ServiceType, LicenseAcceptance: cfg.LicenseAcceptance}
		if err := s.repo.AddProjectService(ctx, ps); err != nil && !errors.Is(err, ErrDuplicateType) {
			return err
		}
	}
	return nil
}

// generatePassword returns a cryptographically random alphanumeric string of
// the given length, with at least one lower-case letter, upper-case letter
// and digit: SQL Server refuses to start with a password drawing on fewer
// than three character classes. Alphanumeric keeps it safe in connection
// URLs.
func generatePassword(length int) string {
	const (
		lower = "abcdefghijklmnopqrstuvwxyz"
		upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		digit = "0123456789"
	)
	for {
		b := make([]byte, length)
		for i := range b {
			b[i] = randomByte(lower + upper + digit)
		}
		p := string(b)
		if strings.ContainsAny(p, lower) && strings.ContainsAny(p, upper) && strings.ContainsAny(p, digit) {
			return p
		}
	}
}

func randomByte(alphabet string) byte {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
	return alphabet[n.Int64()]
}
