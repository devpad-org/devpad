package wsservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/devpad-org/devpad/internal/container"
)

var (
	ErrNotFound      = errors.New("service not found")
	ErrDuplicateType = errors.New("workspace already has a service of this type")
	ErrInvalidType   = errors.New("unsupported service type")
)

// Service defines workspace service business logic.
type Service interface {
	Create(ctx context.Context, workspaceID int64, serviceType ServiceType) (*WorkspaceService, error)
	List(ctx context.Context, workspaceID int64) ([]*WorkspaceService, error)
	Get(ctx context.Context, id int64) (*WorkspaceService, error)
	Delete(ctx context.Context, id int64) error

	// Lifecycle methods called by the workspace service during start/stop/delete.
	StartAll(ctx context.Context, workspaceID int64, networkName string) error
	StopAll(ctx context.Context, workspaceID int64) error
	DeleteAll(ctx context.Context, workspaceID int64) error

	// EnvVars returns connection info env vars for all services in a workspace.
	EnvVars(ctx context.Context, workspaceID int64, networkName string) ([]string, error)
}

type service struct {
	repo      Repository
	container container.Manager
}

// NewService creates a new workspace service Service.
func NewService(repo Repository, cm container.Manager) Service {
	return &service{repo: repo, container: cm}
}

func (s *service) Create(ctx context.Context, workspaceID int64, serviceType ServiceType) (*WorkspaceService, error) {
	if !ValidServiceType(serviceType) {
		return nil, ErrInvalidType
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

	cfg := DefaultConfigs[serviceType]
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
		if err := s.container.Remove(ctx, svc.ContainerID); err != nil {
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
		if svc.Status != StatusRunning || svc.ContainerID == "" {
			continue
		}

		var cfg ServiceConfig
		if err := json.Unmarshal([]byte(svc.Config), &cfg); err != nil {
			continue
		}

		// Use the container name as the hostname — Docker DNS resolves it
		// within the shared network.
		host := sidecarContainerName(workspaceID, svc.ServiceType)

		prefix := strings.ToUpper(string(svc.ServiceType))
		envs = append(envs,
			fmt.Sprintf("%s_HOST=%s", prefix, host),
			fmt.Sprintf("%s_PORT=%d", prefix, cfg.Port),
			fmt.Sprintf("%s_USER=%s", prefix, cfg.DefaultUser),
			fmt.Sprintf("%s_PASSWORD=%s", prefix, cfg.DefaultPass),
			fmt.Sprintf("%s_DATABASE=%s", prefix, cfg.DefaultDB),
		)
	}
	return envs, nil
}

func (s *service) startOne(ctx context.Context, svc *WorkspaceService, networkName string) error {
	if svc.Status == StatusRunning {
		return nil
	}

	var cfg ServiceConfig
	if err := json.Unmarshal([]byte(svc.Config), &cfg); err != nil {
		return fmt.Errorf("parsing service config: %w", err)
	}

	// If no container exists yet, create one.
	if svc.ContainerID == "" {
		env := sidecarEnv(svc.ServiceType, cfg)
		name := sidecarContainerName(svc.WorkspaceID, svc.ServiceType)
		containerID, err := s.container.CreateSidecar(ctx, name, cfg.Image, svc.VolumeName, networkName, env, cfg.Port)
		if err != nil {
			return fmt.Errorf("creating sidecar container: %w", err)
		}
		svc.ContainerID = containerID
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

func sidecarContainerName(workspaceID int64, serviceType ServiceType) string {
	return fmt.Sprintf("devpad-svc-%d-%s", workspaceID, serviceType)
}

func sidecarEnv(serviceType ServiceType, cfg ServiceConfig) []string {
	switch serviceType {
	case ServicePostgres:
		return []string{
			fmt.Sprintf("POSTGRES_USER=%s", cfg.DefaultUser),
			fmt.Sprintf("POSTGRES_PASSWORD=%s", cfg.DefaultPass),
			fmt.Sprintf("POSTGRES_DB=%s", cfg.DefaultDB),
			"PGDATA=/data/pgdata",
		}
	case ServiceMongoDB:
		return []string{
			fmt.Sprintf("MONGO_INITDB_ROOT_USERNAME=%s", cfg.DefaultUser),
			fmt.Sprintf("MONGO_INITDB_ROOT_PASSWORD=%s", cfg.DefaultPass),
			fmt.Sprintf("MONGO_INITDB_DATABASE=%s", cfg.DefaultDB),
		}
	default:
		return nil
	}
}
