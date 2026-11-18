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

	want := containerpkg.SidecarSpec{
		Name:        "devpad-svc-42-meilisearch",
		Image:       "getmeili/meilisearch:v1.45",
		Network:     "devpad-net-42",
		Volume:      "devpad-svc-42-meilisearch",
		DataDir:     "/data",
		Healthcheck: []string{"CMD", "curl", "-fs", "http://127.0.0.1:7700/health"},
	}
	got := manager.sidecar
	got.Env = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected sidecar spec:\nwant: %+v\n got: %+v", want, got)
	}

	assertStringSet(t, manager.sidecar.Env, []string{
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
	services        map[int64]*WorkspaceService
	projectServices []*ProjectService
}

func (r *fakeRepository) ListProjectServices(_ context.Context, projectID int64) ([]*ProjectService, error) {
	var out []*ProjectService
	for _, ps := range r.projectServices {
		if ps.ProjectID == projectID {
			out = append(out, ps)
		}
	}
	return out, nil
}

func (r *fakeRepository) AddProjectService(_ context.Context, ps *ProjectService) error {
	for _, have := range r.projectServices {
		if have.ProjectID == ps.ProjectID && have.ServiceType == ps.ServiceType {
			return ErrDuplicateType
		}
	}
	ps.CreatedAt = time.Now()
	r.projectServices = append(r.projectServices, ps)
	return nil
}

func (r *fakeRepository) RemoveProjectService(_ context.Context, projectID int64, serviceType ServiceType) error {
	for i, ps := range r.projectServices {
		if ps.ProjectID == projectID && ps.ServiceType == serviceType {
			r.projectServices = append(r.projectServices[:i], r.projectServices[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// Create refuses a second service of a type on a workspace, as the
// database's unique index does.
func (r *fakeRepository) Create(_ context.Context, svc *WorkspaceService) error {
	if r.services == nil {
		r.services = make(map[int64]*WorkspaceService)
	}
	for _, have := range r.services {
		if have.WorkspaceID == svc.WorkspaceID && have.ServiceType == svc.ServiceType {
			return ErrDuplicateType
		}
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
	sidecar        containerpkg.SidecarSpec
	sidecarCreates int
	volumes        []string
	removedVolumes []string
	arch           string
	archCalls      int
	removed        []string
	removedSidecar []string
	// sidecars are the specs of every sidecar created, in order.
	sidecars []containerpkg.SidecarSpec
	started  []string
	// health is what SidecarHealth reports for each container; a missing
	// one is running and healthy. healthChecks counts the reads.
	health       map[string][]containerpkg.SidecarHealth
	healthChecks int
	// pulled are the images pulled, in order; pullErr fails each pull.
	pulled  []string
	pullErr error
}

func (m *fakeContainerManager) PullImage(_ context.Context, ref string) error {
	m.pulled = append(m.pulled, ref)
	return m.pullErr
}

func (m *fakeContainerManager) SidecarHealth(_ context.Context, id string) (containerpkg.SidecarHealth, error) {
	m.healthChecks++
	seq := m.health[id]
	if len(seq) == 0 {
		return containerpkg.SidecarHealth{Running: true, Status: "healthy"}, nil
	}
	h := seq[0]
	if len(seq) > 1 {
		m.health[id] = seq[1:]
	}
	return h, nil
}

func (m *fakeContainerManager) Create(_ context.Context, _, _, _, _ string, _ []string, _, _ int64) (string, error) {
	return "", nil
}

func (m *fakeContainerManager) Start(_ context.Context, id string) error {
	m.started = append(m.started, id)
	return nil
}

func (m *fakeContainerManager) Stop(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) Restart(_ context.Context, _ string) error {
	return nil
}

func (m *fakeContainerManager) Remove(_ context.Context, id string) error {
	m.removed = append(m.removed, id)
	return nil
}

func (m *fakeContainerManager) RemoveSidecar(_ context.Context, id string) error {
	m.removedSidecar = append(m.removedSidecar, id)
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

func (m *fakeContainerManager) CreateVolume(_ context.Context, name string) error {
	m.volumes = append(m.volumes, name)
	return nil
}

func (m *fakeContainerManager) RemoveVolume(_ context.Context, name string) error {
	m.removedVolumes = append(m.removedVolumes, name)
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

func (m *fakeContainerManager) Rename(context.Context, string, string) error { return nil }
func (m *fakeContainerManager) BuildProjectImage(context.Context, string, []byte, containerpkg.BuildOptions) (string, error) {
	return "", nil
}
func (m *fakeContainerManager) ImageID(context.Context, string) (string, error) { return "", nil }
func (m *fakeContainerManager) ListImages(context.Context, string) ([]containerpkg.ImageInfo, error) {
	return nil, nil
}
func (m *fakeContainerManager) RemoveImage(context.Context, string) error { return nil }
func (m *fakeContainerManager) BuildImage(_ context.Context, _ []byte, _ []byte, _ string) error {
	return nil
}

func (m *fakeContainerManager) CreateSidecar(_ context.Context, spec containerpkg.SidecarSpec) (string, error) {
	m.sidecar = spec
	m.sidecarCreates++
	m.sidecars = append(m.sidecars, spec)
	if m.sidecarID != "" {
		return m.sidecarID, nil
	}
	return spec.Name + "-container", nil
}

func (m *fakeContainerManager) DaemonArch(context.Context) (string, error) {
	m.archCalls++
	if m.arch == "" {
		return "amd64", nil
	}
	return m.arch, nil
}
