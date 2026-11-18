package wsservice

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	containerpkg "github.com/devpad-org/devpad/internal/container"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func TestProjectServicesRepositoryRoundTrip(t *testing.T) {
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, username, email, password) VALUES (1, 'owner', 'owner@example.com', 'x')`,
		`INSERT INTO projects (id, user_id, name, status) VALUES (1, 1, 'shop', 'ready')`,
	} {
		if _, err := db.Conn().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	repo := NewRepository(db.Conn())
	ctx := context.Background()

	accepted := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	acceptance := &LicenseAcceptance{UserID: 1, AcceptedAt: accepted, TermsURL: "https://example.com/terms", Image: "mssql:1"}
	if err := repo.AddProjectService(ctx, &ProjectService{ProjectID: 1, ServiceType: ServicePostgres}); err != nil {
		t.Fatalf("adding postgres: %v", err)
	}
	if err := repo.AddProjectService(ctx, &ProjectService{ProjectID: 1, ServiceType: ServiceSQLServer, LicenseAcceptance: acceptance}); err != nil {
		t.Fatalf("adding sqlserver: %v", err)
	}
	if err := repo.AddProjectService(ctx, &ProjectService{ProjectID: 1, ServiceType: ServicePostgres}); !errors.Is(err, ErrDuplicateType) {
		t.Fatalf("expected a second postgres to be ErrDuplicateType, got %v", err)
	}

	got, err := repo.ListProjectServices(ctx, 1)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 project services, got %d", len(got))
	}
	for _, ps := range got {
		switch ps.ServiceType {
		case ServicePostgres:
			if ps.LicenseAcceptance != nil {
				t.Errorf("postgres: expected no licence acceptance, got %+v", ps.LicenseAcceptance)
			}
		case ServiceSQLServer:
			if ps.LicenseAcceptance == nil || *ps.LicenseAcceptance != *acceptance {
				t.Errorf("sqlserver: expected acceptance %+v, got %+v", acceptance, ps.LicenseAcceptance)
			}
		}
	}

	if err := repo.RemoveProjectService(ctx, 1, ServicePostgres); err != nil {
		t.Fatalf("removing postgres: %v", err)
	}
	if err := repo.RemoveProjectService(ctx, 1, ServicePostgres); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected removing it again to be ErrNotFound, got %v", err)
	}
}

func TestAddProjectServiceChecksLicenceAndArchitecture(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepository{}
	svc := NewService(repo, &fakeContainerManager{}).(*service)
	svc.now = func() time.Time { return time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC) }

	if _, err := svc.AddProjectService(ctx, 1, "redis", CreateOptions{UserID: 1}); !errors.Is(err, ErrInvalidType) {
		t.Fatalf("expected an unknown type to be refused, got %v", err)
	}
	if _, err := svc.AddProjectService(ctx, 1, ServiceSQLServer, CreateOptions{UserID: 1}); !errors.Is(err, ErrLicenseNotAccepted) {
		t.Fatalf("expected sqlserver without acceptance to be refused, got %v", err)
	}
	ps, err := svc.AddProjectService(ctx, 1, ServiceSQLServer, CreateOptions{UserID: 7, AcceptLicense: true})
	if err != nil {
		t.Fatalf("adding sqlserver: %v", err)
	}
	a := ps.LicenseAcceptance
	if a == nil || a.UserID != 7 || a.TermsURL != definitions[ServiceSQLServer].license.URL || a.Image != definitions[ServiceSQLServer].defaults.Image {
		t.Fatalf("expected the acceptance to be recorded for user 7, got %+v", a)
	}
	if _, err := svc.AddProjectService(ctx, 1, ServiceSQLServer, CreateOptions{UserID: 7, AcceptLicense: true}); !errors.Is(err, ErrDuplicateType) {
		t.Fatalf("expected a duplicate to be refused, got %v", err)
	}

	arm := NewService(&fakeRepository{}, &fakeContainerManager{arch: "arm64"})
	if _, err := arm.AddProjectService(ctx, 1, ServiceSQLServer, CreateOptions{UserID: 1, AcceptLicense: true}); !errors.Is(err, ErrUnsupportedArch) {
		t.Fatalf("expected sqlserver on arm64 to be refused, got %v", err)
	}
}

func TestProvisionGivesEachWorkspaceItsOwnStartedServices(t *testing.T) {
	ctx := context.Background()
	acceptance := &LicenseAcceptance{UserID: 7, TermsURL: "https://example.com/terms", Image: "mssql:1"}
	repo := &fakeRepository{projectServices: []*ProjectService{
		{ProjectID: 1, ServiceType: ServicePostgres},
		{ProjectID: 1, ServiceType: ServiceSQLServer, LicenseAcceptance: acceptance},
		{ProjectID: 2, ServiceType: ServiceMongoDB},
	}}
	cm := &fakeContainerManager{}
	svc := NewService(repo, cm)

	for _, ws := range []int64{10, 11} {
		if err := svc.Provision(ctx, ws, 1, "devpad-net-x", true); err != nil {
			t.Fatalf("provisioning workspace %d: %v", ws, err)
		}
	}

	var names []string
	for _, spec := range cm.sidecars {
		names = append(names, spec.Name)
		if len(spec.Healthcheck) == 0 {
			t.Errorf("%s: created without a health check", spec.Name)
		}
	}
	slices.Sort(names)
	want := []string{"devpad-svc-10-postgres", "devpad-svc-10-sqlserver", "devpad-svc-11-postgres", "devpad-svc-11-sqlserver"}
	if !slices.Equal(names, want) {
		t.Fatalf("expected sidecars %v, got %v", want, names)
	}
	if len(cm.started) != 4 {
		t.Fatalf("expected 4 sidecars started, got %v", cm.started)
	}

	// Each copy has its own password and volume, and SQL Server's is
	// deployed under the project's acceptance.
	passwords := map[string]bool{}
	for _, ws := range []int64{10, 11} {
		services, _ := repo.ListByWorkspaceID(ctx, ws)
		for _, s := range services {
			if s.Status != StatusRunning {
				t.Errorf("workspace %d %s: expected running, got %s", ws, s.ServiceType, s.Status)
			}
			cfg := mustConfig(t, s)
			if passwords[cfg.DefaultPass] {
				t.Errorf("workspace %d %s: password shared with another copy", ws, s.ServiceType)
			}
			passwords[cfg.DefaultPass] = true
			if s.ServiceType == ServiceSQLServer && (cfg.LicenseAcceptance == nil || *cfg.LicenseAcceptance != *acceptance) {
				t.Errorf("workspace %d: expected sqlserver deployed under %+v, got %+v", ws, acceptance, cfg.LicenseAcceptance)
			}
		}
	}
	for _, spec := range cm.sidecars {
		if strings.HasSuffix(spec.Name, "sqlserver") && !slices.Contains(spec.Env, "ACCEPT_EULA=Y") {
			t.Errorf("%s: expected the licence accepted in its environment", spec.Name)
		}
	}

	// Provisioning again adds nothing.
	if err := svc.Provision(ctx, 10, 1, "devpad-net-x", true); err != nil {
		t.Fatalf("provisioning again: %v", err)
	}
	if len(cm.sidecars) != 4 {
		t.Fatalf("expected provisioning again to add nothing, got %d sidecars", len(cm.sidecars))
	}
}

func TestProvisionWithoutStartCreatesContainersForTheirEnvironment(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepository{projectServices: []*ProjectService{{ProjectID: 1, ServiceType: ServicePostgres}}}
	cm := &fakeContainerManager{}
	svc := NewService(repo, cm)

	if err := svc.Provision(ctx, 10, 1, "devpad-net-10", false); err != nil {
		t.Fatalf("provisioning: %v", err)
	}
	if len(cm.started) != 0 || cm.healthChecks != 0 {
		t.Fatalf("expected nothing started or waited for, got started %v and %d health checks", cm.started, cm.healthChecks)
	}
	services, _ := repo.ListByWorkspaceID(ctx, 10)
	if len(services) != 1 || services[0].Status != StatusStopped || services[0].ContainerID == "" {
		t.Fatalf("expected one stopped postgres with a container, got %+v", services)
	}

	// A stopped service with a container is still in the environment, so a
	// container made now can reach it once it starts.
	env, err := svc.EnvVars(ctx, 10, "devpad-net-10")
	if err != nil {
		t.Fatalf("env vars: %v", err)
	}
	if !slices.Contains(env, "POSTGRES_HOST=devpad-svc-10-postgres") {
		t.Fatalf("expected postgres in the environment, got %v", env)
	}
}

func TestAwaitReadyWaitsForServicesToBeHealthy(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepository{projectServices: []*ProjectService{{ProjectID: 1, ServiceType: ServicePostgres}}}
	cm := &fakeContainerManager{health: map[string][]containerpkg.SidecarHealth{
		"devpad-svc-10-postgres-container": {
			{Running: false, Status: "starting"},
			{Running: true, Status: "starting"},
			{Running: true, Status: "starting"},
			{Running: true, Status: "healthy"},
		},
	}}
	svc := NewService(repo, cm).(*service)
	svc.readyPoll = time.Millisecond

	if err := svc.Provision(ctx, 10, 1, "devpad-net-10", true); err != nil {
		t.Fatalf("provisioning: %v", err)
	}
	if cm.healthChecks != 0 {
		t.Fatalf("expected provisioning not to wait, got %d health checks", cm.healthChecks)
	}
	if err := svc.AwaitReady(ctx, 10); err != nil {
		t.Fatalf("awaiting: %v", err)
	}
	if cm.healthChecks != 4 {
		t.Fatalf("expected to wait until healthy (4 checks), got %d", cm.healthChecks)
	}
}

func TestAwaitReadyFailsWhenAServiceIsNotReady(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		// health is what each check reports, the last one from then on.
		health []containerpkg.SidecarHealth
		want   string
	}{
		{"unhealthy", []containerpkg.SidecarHealth{{Running: true, Status: "unhealthy"}}, "not accepting connections"},
		{"never ready", []containerpkg.SidecarHealth{{Running: true, Status: "starting"}}, "was not ready after"},
		// Docker restarting it means it exited, so it is not waited for.
		{"restarted", []containerpkg.SidecarHealth{
			{Running: true, Status: "starting"},
			{Running: false, Status: "starting", RestartCount: 1, ExitCode: 137},
			{Running: true, Status: "starting", RestartCount: 1},
		}, "stopped while starting, with exit code 137"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{projectServices: []*ProjectService{{ProjectID: 1, ServiceType: ServicePostgres}}}
			cm := &fakeContainerManager{health: map[string][]containerpkg.SidecarHealth{
				"devpad-svc-10-postgres-container": tt.health,
			}}
			svc := NewService(repo, cm).(*service)
			svc.readyPoll = time.Millisecond
			svc.readyTimeout = 20 * time.Millisecond

			if err := svc.Provision(ctx, 10, 1, "devpad-net-10", true); err != nil {
				t.Fatalf("provisioning: %v", err)
			}
			err := svc.AwaitReady(ctx, 10)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected an error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestAwaitReadyWaitsOnceForAllTheServices(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepository{projectServices: []*ProjectService{
		{ProjectID: 1, ServiceType: ServicePostgres},
		{ProjectID: 1, ServiceType: ServiceMariaDB},
	}}
	// Postgres gets ready late, and MariaDB never does.
	slow := make([]containerpkg.SidecarHealth, 20)
	for i := range slow {
		slow[i] = containerpkg.SidecarHealth{Running: true, Status: "starting"}
	}
	cm := &fakeContainerManager{health: map[string][]containerpkg.SidecarHealth{
		"devpad-svc-10-postgres-container": append(slow, containerpkg.SidecarHealth{Running: true, Status: "healthy"}),
		"devpad-svc-10-mariadb-container":  {{Running: true, Status: "starting"}},
	}}
	svc := NewService(repo, cm).(*service)
	svc.readyPoll = 10 * time.Millisecond
	svc.readyTimeout = 300 * time.Millisecond
	if err := svc.Provision(ctx, 10, 1, "devpad-net-10", true); err != nil {
		t.Fatalf("provisioning: %v", err)
	}

	began := time.Now()
	err := svc.AwaitReady(ctx, 10)
	if err == nil || !strings.Contains(err.Error(), "mariadb service was not ready") {
		t.Fatalf("expected MariaDB not ready, got %v", err)
	}
	// MariaDB is not given a deadline of its own after Postgres's wait,
	// which would make it at least 500ms.
	if took := time.Since(began); took > 450*time.Millisecond {
		t.Fatalf("expected one deadline for both, took %s", took)
	}
}

func TestPullImagesPullsWhatTheHostCanRun(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepository{projectServices: []*ProjectService{
		{ProjectID: 1, ServiceType: ServicePostgres},
		{ProjectID: 1, ServiceType: ServiceSQLServer},
	}}
	cm := &fakeContainerManager{arch: "arm64"}
	svc := NewService(repo, cm)

	if err := svc.PullImages(ctx, 1); err != nil {
		t.Fatalf("pulling: %v", err)
	}
	// SQL Server runs on amd64 only; Provision says so when it is needed.
	if want := []string{definitions[ServicePostgres].defaults.Image}; !slices.Equal(cm.pulled, want) {
		t.Fatalf("expected %v pulled, got %v", want, cm.pulled)
	}

	cm.pullErr = errors.New("registry unreachable")
	if err := svc.PullImages(ctx, 1); err == nil || !strings.Contains(err.Error(), "postgres service: registry unreachable") {
		t.Fatalf("expected the pull's failure, got %v", err)
	}
}

func TestProvisionRefusesAnOutdatedLicenceAcceptance(t *testing.T) {
	ctx := context.Background()
	license := definitions[ServiceSQLServer].license
	tests := []struct {
		name string
		// current is the terms' version now; accepted the version accepted,
		// 0 for an acceptance from before terms had versions.
		current, accepted int
		wantErr           error
	}{
		{"current", 1, 1, nil},
		{"from before versions", 1, 0, nil},
		{"changed since", 2, 1, ErrLicenseOutdated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			was := license.version
			license.version = tt.current
			t.Cleanup(func() { license.version = was })

			acceptance := &LicenseAcceptance{UserID: 7, TermsURL: license.URL, Image: "mssql:1", TermsVersion: tt.accepted}
			repo := &fakeRepository{projectServices: []*ProjectService{{ProjectID: 1, ServiceType: ServiceSQLServer, LicenseAcceptance: acceptance}}}
			cm := &fakeContainerManager{arch: "amd64"}
			svc := NewService(repo, cm)

			err := svc.Provision(ctx, 10, 1, "devpad-net-10", true)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
			if tt.wantErr != nil {
				if !strings.Contains(err.Error(), "add it again") {
					t.Fatalf("expected the error to say how to accept the new terms, got %v", err)
				}
				if cm.sidecarCreates != 0 {
					t.Fatalf("expected no container under the old terms, got %d", cm.sidecarCreates)
				}
			}
		})
	}
}

func TestAdoptWorkspaceServicesAddsThemToTheProject(t *testing.T) {
	ctx := context.Background()
	acceptance := &LicenseAcceptance{UserID: 7, TermsURL: "https://example.com/terms", Image: "mssql:1"}
	repo := &fakeRepository{projectServices: []*ProjectService{{ProjectID: 1, ServiceType: ServicePostgres}}}
	svc := NewService(repo, &fakeContainerManager{})
	if _, err := svc.Create(ctx, 10, ServicePostgres, CreateOptions{UserID: 7}); err != nil {
		t.Fatalf("creating postgres: %v", err)
	}
	if _, err := svc.Create(ctx, 10, ServiceSQLServer, CreateOptions{UserID: 7, AcceptLicense: true}); err != nil {
		t.Fatalf("creating sqlserver: %v", err)
	}

	if err := svc.AdoptWorkspaceServices(ctx, 10, 1); err != nil {
		t.Fatalf("adopting: %v", err)
	}
	// Again, as a resumed conversion would.
	if err := svc.AdoptWorkspaceServices(ctx, 10, 1); err != nil {
		t.Fatalf("adopting again: %v", err)
	}
	got, _ := svc.ProjectServices(ctx, 1)
	if len(got) != 2 {
		t.Fatalf("expected postgres and sqlserver, got %d services", len(got))
	}
	for _, ps := range got {
		if ps.ServiceType == ServiceSQLServer && (ps.LicenseAcceptance == nil || ps.LicenseAcceptance.UserID != acceptance.UserID) {
			t.Fatalf("expected sqlserver's acceptance to be adopted, got %+v", ps.LicenseAcceptance)
		}
	}
}

func mustConfig(t *testing.T, s *WorkspaceService) ServiceConfig {
	t.Helper()
	var cfg ServiceConfig
	if err := json.Unmarshal([]byte(s.Config), &cfg); err != nil {
		t.Fatalf("parsing %s config: %v", s.ServiceType, err)
	}
	return cfg
}
