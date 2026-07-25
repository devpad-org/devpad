package wsservice

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	containerpkg "github.com/devpad-org/devpad/internal/container"
)

func TestStartMeilisearchServiceConfiguresContainerAndConnectionEnv(t *testing.T) {
	repo := &fakeRepository{
		services: map[int64]*WorkspaceService{
			1: {
				ID:          1,
				WorkspaceID: 42,
				ServiceType: ServiceMeilisearch,
				VolumeName:  "devpad-svc-42-meilisearch",
				Status:      StatusStopped,
				Config:      `{"image":"getmeili/meilisearch:v1.45","port":7700,"defaultPass":"master-key"}`,
			},
		},
	}
	manager := &fakeContainerManager{sidecarID: "meili-container"}
	service := NewService(repo, manager)

	started, err := service.Start(context.Background(), 1, "devpad-net-42")
	if err != nil {
		t.Fatalf("starting service: %v", err)
	}

	if started.Status != StatusRunning {
		t.Fatalf("expected service running, got %q", started.Status)
	}
	if started.ContainerID != "meili-container" {
		t.Fatalf("expected container id to be set, got %q", started.ContainerID)
	}

	if manager.sidecarName != "devpad-svc-42-meilisearch" {
		t.Fatalf("expected sidecar name devpad-svc-42-meilisearch, got %q", manager.sidecarName)
	}
	if manager.sidecarImage != "getmeili/meilisearch:v1.45" {
		t.Fatalf("expected Meilisearch image, got %q", manager.sidecarImage)
	}
	if manager.sidecarVolume != "devpad-svc-42-meilisearch" {
		t.Fatalf("expected Meilisearch volume, got %q", manager.sidecarVolume)
	}
	if manager.sidecarNetwork != "devpad-net-42" {
		t.Fatalf("expected sidecar network devpad-net-42, got %q", manager.sidecarNetwork)
	}
	if manager.sidecarPort != 7700 {
		t.Fatalf("expected sidecar port 7700, got %d", manager.sidecarPort)
	}

	assertStringSet(t, manager.sidecarEnv, []string{
		"MEILI_MASTER_KEY=master-key",
		"MEILI_DB_PATH=/data",
		"MEILI_NO_ANALYTICS=true",
	})

	env, err := service.EnvVars(context.Background(), 42, "devpad-net-42")
	if err != nil {
		t.Fatalf("getting env vars: %v", err)
	}
	assertStringSet(t, env, []string{
		"MEILISEARCH_HOST=devpad-svc-42-meilisearch",
		"MEILISEARCH_PORT=7700",
		"MEILISEARCH_URL=http://devpad-svc-42-meilisearch:7700",
		"MEILISEARCH_MASTER_KEY=master-key",
		"MEILISEARCH_API_KEY=master-key",
		"MEILI_MASTER_KEY=master-key",
	})
}

func assertStringSet(t *testing.T, got, want []string) {
	t.Helper()

	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected strings:\nwant: %#v\n got: %#v", want, got)
	}
}

type fakeRepository struct {
	services map[int64]*WorkspaceService
}

func (r *fakeRepository) Create(_ context.Context, svc *WorkspaceService) error {
	if r.services == nil {
		r.services = make(map[int64]*WorkspaceService)
	}
	svc.ID = int64(len(r.services) + 1)
	now := time.Now()
	svc.CreatedAt = now
	svc.UpdatedAt = now
	r.services[svc.ID] = svc
	return nil
}

func (r *fakeRepository) GetByID(_ context.Context, id int64) (*WorkspaceService, error) {
	return r.services[id], nil
}

func (r *fakeRepository) ListByWorkspaceID(_ context.Context, workspaceID int64) ([]*WorkspaceService, error) {
	var services []*WorkspaceService
	for _, service := range r.services {
		if service.WorkspaceID == workspaceID {
			services = append(services, service)
		}
	}
	return services, nil
}

func (r *fakeRepository) Update(_ context.Context, svc *WorkspaceService) error {
	r.services[svc.ID] = svc
	return nil
}

func (r *fakeRepository) Delete(_ context.Context, id int64) error {
	delete(r.services, id)
	return nil
}

type fakeContainerManager struct {
	sidecarID      string
	sidecarName    string
	sidecarImage   string
	sidecarVolume  string
	sidecarNetwork string
	sidecarEnv     []string
	sidecarPort    int
}

func (m *fakeContainerManager) Create(_ context.Context, _, _, _ string, _ []string, _, _ int64) (string, error) {
	return "", nil
}

func (m *fakeContainerManager) Start(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) Stop(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) Restart(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) Remove(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) UpdateResources(_ context.Context, _ string, _, _ int64) error {
	return nil
}

func (m *fakeContainerManager) GetIP(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (m *fakeContainerManager) GetEnv(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (m *fakeContainerManager) Stats(_ context.Context, _ string) (*containerpkg.ContainerStats, error) {
	return &containerpkg.ContainerStats{}, nil
}

func (m *fakeContainerManager) CreateNetwork(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) RemoveNetwork(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) ConnectToNetwork(_ context.Context, _, _ string) error {
	return nil
}

func (m *fakeContainerManager) EnsureSelfAttached(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) DetachSelf(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) CreateVolume(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) RemoveVolume(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) Exec(_ context.Context, _ string, _ []string) (string, error) {
	return "", nil
}

func (m *fakeContainerManager) ExecAttach(_ context.Context, _ string) (containerpkg.HijackedResponse, error) {
	return containerpkg.HijackedResponse{}, nil
}

func (m *fakeContainerManager) ExecResize(_ context.Context, _ string, _, _ uint) error {
	return nil
}

func (m *fakeContainerManager) CopyFileToContainer(_ context.Context, _, _ string, _ []byte, _ int64) error {
	return nil
}

func (m *fakeContainerManager) BuildImage(_ context.Context, _ []byte, _ []byte, _ string) error {
	return nil
}

func (m *fakeContainerManager) CreateSidecar(_ context.Context, name, imageName, volumeName, networkName string, env []string, port int) (string, error) {
	m.sidecarName = name
	m.sidecarImage = imageName
	m.sidecarVolume = volumeName
	m.sidecarNetwork = networkName
	m.sidecarEnv = append([]string(nil), env...)
	m.sidecarPort = port
	return m.sidecarID, nil
}
