package wsservice

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	containerpkg "github.com/devpad-org/devpad/internal/container"
)

func TestStartJobServicesGivesTheJobFreshCopies(t *testing.T) {
	ctx := context.Background()
	acceptance := &LicenseAcceptance{UserID: 7, TermsURL: "https://example.com/terms", Image: "mssql:1"}
	repo := &fakeRepository{projectServices: []*ProjectService{
		{ProjectID: 1, ServiceType: ServicePostgres},
		{ProjectID: 1, ServiceType: ServiceSQLServer, LicenseAcceptance: acceptance},
		{ProjectID: 1, ServiceType: ServiceMongoDB},
	}}
	cm := &fakeContainerManager{health: map[string][]containerpkg.SidecarHealth{
		"devpad-job-7.backend.postgres-container": {{Running: true, Status: "starting"}, {Running: true, Status: "healthy"}},
	}}
	svc := NewService(repo, cm).(*service)
	svc.readyPoll = time.Millisecond
	job := containerpkg.Job{Pipeline: "7", Name: "backend"}

	got, err := svc.StartJobServices(ctx, JobServicesRequest{ProjectID: 1, Types: []ServiceType{ServicePostgres, ServiceSQLServer}, Job: job})
	if err != nil {
		t.Fatalf("starting: %v", err)
	}

	// Only what the job names, each on the job's network under its type,
	// labelled as the job's, never restarted and with no volume.
	if len(cm.sidecars) != 2 {
		t.Fatalf("expected two services, got %+v", cm.sidecars)
	}
	for _, spec := range cm.sidecars {
		typ := spec.NetworkAlias
		if spec.Name != job.ServiceName(typ) || spec.Network != job.NetworkName() || !spec.NoRestart || spec.Volume != "" {
			t.Errorf("%s: unexpected spec %+v", typ, spec)
		}
		if spec.Labels[containerpkg.LabelPipeline] != "7" || spec.Labels[containerpkg.LabelJob] != "backend" {
			t.Errorf("%s: labels %v", typ, spec.Labels)
		}
		if len(spec.Healthcheck) == 0 {
			t.Errorf("%s: no health check", typ)
		}
	}
	if !slices.Contains(cm.sidecars[1].Env, "ACCEPT_EULA=Y") {
		t.Errorf("expected SQL Server deployed under the project's acceptance")
	}
	if !slices.Equal(cm.started, []string{"devpad-job-7.backend.postgres-container", "devpad-job-7.backend.sqlserver-container"}) {
		t.Errorf("started %v", cm.started)
	}
	// It waited for Postgres to be healthy.
	if cm.healthChecks < 3 {
		t.Errorf("expected the health waited for, got %d checks", cm.healthChecks)
	}
	// The job reaches them by their types.
	for _, want := range []string{"POSTGRES_HOST=postgres", "POSTGRES_PORT=5432", "SQLSERVER_HOST=sqlserver"} {
		if !slices.Contains(got.Env, want) {
			t.Errorf("expected %s in %v", want, got.Env)
		}
	}
	// A job is no workspace.
	if len(repo.services) != 0 || len(cm.volumes) != 0 {
		t.Fatalf("expected no workspace services or volumes, got %v %v", repo.services, cm.volumes)
	}

	// Another job of the same pipeline gets its own copies.
	other := containerpkg.Job{Pipeline: "7", Name: "frontend"}
	again, err := svc.StartJobServices(ctx, JobServicesRequest{ProjectID: 1, Types: []ServiceType{ServicePostgres}, Job: other})
	if err != nil {
		t.Fatalf("starting another job's: %v", err)
	}
	if cm.sidecars[2].Name != other.ServiceName("postgres") {
		t.Fatalf("expected a copy of the other job's, got %+v", cm.sidecars[2])
	}
	if first, second := postgresPassword(got.Env), postgresPassword(again.Env); first == "" || first == second {
		t.Fatalf("expected each copy its own password, got %q and %q", first, second)
	}

	// Tearing one job's down leaves the other's.
	if err := svc.RemoveJobServices(ctx, job); err != nil {
		t.Fatalf("removing: %v", err)
	}
	if !slices.Equal(cm.removedSidecar, []string{"devpad-job-7.backend.postgres-container", "devpad-job-7.backend.sqlserver-container"}) {
		t.Fatalf("removed %v", cm.removedSidecar)
	}
}

func postgresPassword(env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "POSTGRES_PASSWORD="); ok {
			return v
		}
	}
	return ""
}

func TestStartJobServicesRefusesBeforeStartingAny(t *testing.T) {
	ctx := context.Background()
	license := definitions[ServiceSQLServer].license
	accepted := &LicenseAcceptance{UserID: 7, TermsURL: license.URL, Image: "mssql:1", TermsVersion: license.version}
	tests := []struct {
		name    string
		types   []ServiceType
		arch    string
		terms   int
		wantErr error
		want    string
	}{
		{name: "not the project's", types: []ServiceType{ServicePostgres, ServiceMariaDB}, wantErr: ErrNotProjectService, want: "mariadb"},
		{name: "unknown", types: []ServiceType{"redis"}, wantErr: ErrInvalidType, want: "redis"},
		{name: "named twice", types: []ServiceType{ServicePostgres, ServicePostgres}, want: "twice"},
		{name: "wrong architecture", types: []ServiceType{ServicePostgres, ServiceSQLServer}, arch: "arm64", wantErr: ErrUnsupportedArch},
		{name: "terms changed", types: []ServiceType{ServicePostgres, ServiceSQLServer}, terms: license.version + 1, wantErr: ErrLicenseOutdated, want: "add it again"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.terms != 0 {
				was := license.version
				license.version = tt.terms
				t.Cleanup(func() { license.version = was })
			}
			repo := &fakeRepository{projectServices: []*ProjectService{
				{ProjectID: 1, ServiceType: ServicePostgres},
				{ProjectID: 1, ServiceType: ServiceSQLServer, LicenseAcceptance: accepted},
			}}
			cm := &fakeContainerManager{arch: tt.arch}
			svc := NewService(repo, cm)
			_, err := svc.StartJobServices(ctx, JobServicesRequest{ProjectID: 1, Types: tt.types, Job: containerpkg.Job{Pipeline: "7", Name: "test"}})
			if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %v mentioning %q, got %v", tt.wantErr, tt.want, err)
			}
			if cm.sidecarCreates != 0 {
				t.Fatalf("expected nothing created, got %d", cm.sidecarCreates)
			}
		})
	}
}

func TestStartJobServicesRemovesThemWhenOneIsNotReady(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepository{projectServices: []*ProjectService{
		{ProjectID: 1, ServiceType: ServicePostgres},
		{ProjectID: 1, ServiceType: ServiceMariaDB},
	}}
	cm := &fakeContainerManager{health: map[string][]containerpkg.SidecarHealth{
		"devpad-job-7.test.mariadb-container": {{Running: true, Status: "unhealthy"}},
	}}
	svc := NewService(repo, cm).(*service)
	svc.readyPoll = time.Millisecond
	job := containerpkg.Job{Pipeline: "7", Name: "test"}

	_, err := svc.StartJobServices(ctx, JobServicesRequest{ProjectID: 1, Types: []ServiceType{ServicePostgres, ServiceMariaDB}, Job: job})
	if err == nil || !strings.Contains(err.Error(), "mariadb service started but is not accepting connections") {
		t.Fatalf("expected MariaDB not ready, got %v", err)
	}
	slices.Sort(cm.removedSidecar)
	if !slices.Equal(cm.removedSidecar, []string{"devpad-job-7.test.mariadb-container", "devpad-job-7.test.postgres-container"}) {
		t.Fatalf("expected both removed, got %v", cm.removedSidecar)
	}
}

func TestStartJobServicesWithNoneStartsNothing(t *testing.T) {
	cm := &fakeContainerManager{}
	svc := NewService(&fakeRepository{}, cm)
	got, err := svc.StartJobServices(context.Background(), JobServicesRequest{ProjectID: 1, Job: containerpkg.Job{Pipeline: "7", Name: "lint"}})
	if err != nil || len(got.Env) != 0 || cm.sidecarCreates != 0 {
		t.Fatalf("expected nothing, got %+v %v", got, err)
	}
	if _, err := svc.StartJobServices(context.Background(), JobServicesRequest{ProjectID: 1, Job: containerpkg.Job{Pipeline: "7", Name: "Bad.Name"}}); err == nil {
		t.Fatal("expected a job Docker cannot name refused")
	}
}

func TestRemoveJobServicesLeavesTheJobsOtherResources(t *testing.T) {
	job := containerpkg.Job{Pipeline: "7", Name: "test"}
	cm := &fakeContainerManager{jobResources: []containerpkg.JobResource{
		{Kind: containerpkg.JobContainer, ID: "job-container", Job: job},
		{Kind: containerpkg.JobNetwork, ID: "job-network", Job: job},
		{Kind: containerpkg.JobContainer, ID: "svc", Job: job, Sidecar: true},
		{Kind: containerpkg.JobContainer, ID: "other-svc", Job: containerpkg.Job{Pipeline: "8", Name: "test"}, Sidecar: true},
	}}
	if err := NewService(&fakeRepository{}, cm).RemoveJobServices(context.Background(), job); err != nil {
		t.Fatalf("removing: %v", err)
	}
	if !slices.Equal(cm.removedSidecar, []string{"svc"}) || len(cm.removed) != 0 {
		t.Fatalf("expected only the job's service removed, got sidecars %v containers %v", cm.removedSidecar, cm.removed)
	}
}
