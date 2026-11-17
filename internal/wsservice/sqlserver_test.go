package wsservice

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestCreateSQLServerRequiresLicenseAcceptance(t *testing.T) {
	repo := &fakeRepository{}
	manager := &fakeContainerManager{}
	svc := NewService(repo, manager)

	_, err := svc.Create(context.Background(), 42, ServiceSQLServer, CreateOptions{UserID: 3})
	if !errors.Is(err, ErrLicenseNotAccepted) {
		t.Fatalf("expected ErrLicenseNotAccepted, got %v", err)
	}
	if len(repo.services) != 0 || len(manager.volumes) != 0 {
		t.Fatalf("expected nothing created, got services %v and volumes %v", repo.services, manager.volumes)
	}
}

func TestCreateSQLServerRecordsWhoAcceptedTheLicense(t *testing.T) {
	repo := &fakeRepository{}
	manager := &fakeContainerManager{sidecarID: "mssql-container"}
	svc := NewService(repo, manager).(*service)
	accepted := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return accepted }

	created, err := svc.Create(context.Background(), 42, ServiceSQLServer, CreateOptions{UserID: 3, AcceptLicense: true})
	if err != nil {
		t.Fatalf("creating service: %v", err)
	}

	var cfg ServiceConfig
	if err := json.Unmarshal([]byte(created.Config), &cfg); err != nil {
		t.Fatalf("parsing config: %v", err)
	}
	want := LicenseAcceptance{
		UserID:     3,
		AcceptedAt: accepted,
		TermsURL:   "https://go.microsoft.com/fwlink/?LinkId=746388",
		Image:      "mcr.microsoft.com/mssql/server:2025-latest",
	}
	if cfg.LicenseAcceptance == nil || *cfg.LicenseAcceptance != want {
		t.Fatalf("expected licence acceptance %+v, got %+v", want, cfg.LicenseAcceptance)
	}

	if _, err := svc.Start(context.Background(), created.ID, "devpad-net-42"); err != nil {
		t.Fatalf("starting service: %v", err)
	}
	spec := manager.sidecar
	if spec.Image != "mcr.microsoft.com/mssql/server:2025-latest" || spec.DataDir != "/var/opt/mssql" || spec.Volume != "devpad-svc-42-sqlserver" {
		t.Fatalf("unexpected sidecar spec: %+v", spec)
	}
	pass := cfg.DefaultPass
	assertStringSet(t, spec.Env, []string{
		"ACCEPT_EULA=Y",
		"MSSQL_PID=EnterpriseDeveloper",
		"MSSQL_SA_PASSWORD=" + pass,
		"MSSQL_DB=devpad",
		"MSSQL_USER=devpad",
		"MSSQL_PASSWORD=" + pass,
		"MSSQL_MEMORY_LIMIT_MB=2048",
	})

	env, err := svc.EnvVars(context.Background(), 42, "devpad-net-42")
	if err != nil {
		t.Fatalf("getting env vars: %v", err)
	}
	assertStringSet(t, env, []string{
		"SQLSERVER_HOST=devpad-svc-42-sqlserver",
		"SQLSERVER_PORT=1433",
		"SQLSERVER_USER=devpad",
		"SQLSERVER_PASSWORD=" + pass,
		"SQLSERVER_DATABASE=devpad",
	})
}

func TestStartSQLServerWithoutAcceptedLicenseCreatesNoContainer(t *testing.T) {
	repo := &fakeRepository{
		services: map[int64]*WorkspaceService{
			1: {
				ID:          1,
				WorkspaceID: 42,
				ServiceType: ServiceSQLServer,
				VolumeName:  "devpad-svc-42-sqlserver",
				Status:      StatusStopped,
				Config:      `{"image":"mcr.microsoft.com/mssql/server:2025-latest","port":1433,"defaultPass":"Pass1234"}`,
			},
		},
	}
	manager := &fakeContainerManager{sidecarID: "mssql-container"}

	_, err := NewService(repo, manager).Start(context.Background(), 1, "devpad-net-42")
	if !errors.Is(err, ErrLicenseNotAccepted) {
		t.Fatalf("expected ErrLicenseNotAccepted, got %v", err)
	}
	if manager.sidecarCreates != 0 {
		t.Fatalf("expected no container created, got %d", manager.sidecarCreates)
	}
}

func TestSQLServerNeedsAnAMD64DockerHost(t *testing.T) {
	repo := &fakeRepository{}
	manager := &fakeContainerManager{arch: "arm64"}
	svc := NewService(repo, manager)

	_, err := svc.Create(context.Background(), 42, ServiceSQLServer, CreateOptions{UserID: 3, AcceptLicense: true})
	if !errors.Is(err, ErrUnsupportedArch) {
		t.Fatalf("expected ErrUnsupportedArch, got %v", err)
	}
	if len(manager.volumes) != 0 {
		t.Fatalf("expected no volume created, got %v", manager.volumes)
	}

	if got := unavailableTypes(svc.Catalog(context.Background())); !slices.Equal(got, []ServiceType{ServiceSQLServer}) {
		t.Fatalf("expected only sqlserver unavailable, got %v", got)
	}

	if _, err := svc.Create(context.Background(), 42, ServicePostgres, CreateOptions{UserID: 3}); err != nil {
		t.Fatalf("expected postgres on arm64 to be created, got %v", err)
	}
	if got := unavailableTypes(NewService(repo, &fakeContainerManager{}).Catalog(context.Background())); len(got) != 0 {
		t.Fatalf("expected nothing unavailable on amd64, got %v", got)
	}
	if manager.archCalls != 1 {
		t.Fatalf("expected Docker asked for its architecture once, got %d", manager.archCalls)
	}
}

func TestContainerEnvAcceptsLicenceTermsOnlyOnceAccepted(t *testing.T) {
	def := definitions[ServiceSQLServer]
	cfg := ServiceConfig{DefaultUser: "u", DefaultPass: "p", DefaultDB: "d"}

	if _, err := def.containerEnv(cfg); !errors.Is(err, ErrLicenseNotAccepted) {
		t.Fatalf("expected ErrLicenseNotAccepted without an acceptance, got %v", err)
	}

	cfg.LicenseAcceptance = &LicenseAcceptance{UserID: 3}
	env, err := def.containerEnv(cfg)
	if err != nil {
		t.Fatalf("building env: %v", err)
	}
	if !slices.Contains(env, "ACCEPT_EULA=Y") {
		t.Fatalf("expected the accepted terms in the env, got %v", env)
	}

	env, err = definitions[ServicePostgres].containerEnv(ServiceConfig{})
	if err != nil || slices.Contains(env, "ACCEPT_EULA=Y") {
		t.Fatalf("expected postgres's env without terms, got %v, %v", env, err)
	}
}
