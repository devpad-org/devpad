package wsservice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	containerpkg "github.com/devpad-org/devpad/internal/container"
)

// TestJobServicesWithDocker gives a pipeline job a Postgres on a real Docker
// daemon, which must already have devpad's workspace image, and reaches it
// from the job's container. It runs only when DEVPAD_DOCKER_TEST is set,
// and everything it makes is labelled with a pipeline id of its own and
// removed.
func TestJobServicesWithDocker(t *testing.T) {
	if os.Getenv("DEVPAD_DOCKER_TEST") == "" {
		t.Skip("set DEVPAD_DOCKER_TEST to run against the local Docker daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cm, err := containerpkg.NewManager()
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	if _, err := cm.ImageID(ctx, containerpkg.WorkspaceImage); err != nil {
		t.Skipf("no workspace image: %v", err)
	}
	suffix := make([]byte, 4)
	rand.Read(suffix)
	job := containerpkg.Job{Pipeline: "devpad-test-" + hex.EncodeToString(suffix), Name: "db"}
	t.Cleanup(func() {
		if err := cm.RemoveJobResources(context.Background(), containerpkg.Job{Pipeline: job.Pipeline}); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	repo := &fakeRepository{projectServices: []*ProjectService{{ProjectID: 1, ServiceType: ServicePostgres}}}
	svc := NewService(repo, cm)
	if err := cm.CreateJobNetwork(ctx, job); err != nil {
		t.Fatalf("network: %v", err)
	}
	services, err := svc.StartJobServices(ctx, JobServicesRequest{ProjectID: 1, Types: []ServiceType{ServicePostgres}, Job: job})
	if err != nil {
		t.Fatalf("services: %v", err)
	}
	id, err := cm.StartJobContainer(ctx, containerpkg.JobContainerSpec{Job: job, Env: services.Env, Lifetime: time.Minute})
	if err != nil {
		t.Fatalf("container: %v", err)
	}
	var out bytes.Buffer
	res, err := cm.ExecJob(ctx, id, containerpkg.ExecSpec{Cmd: containerpkg.JobScript(`exec 3<>"/dev/tcp/$POSTGRES_HOST/$POSTGRES_PORT"; echo reached "$POSTGRES_HOST"`)}, &out)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("reaching postgres from the job: %+v %v\n%s", res, err, out.String())
	}

	if err := svc.RemoveJobServices(ctx, services); err != nil {
		t.Fatalf("removing: %v", err)
	}
	resources, err := cm.ListJobResources(ctx, job)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range resources {
		if r.Sidecar {
			t.Fatalf("expected the service gone, found %+v", r)
		}
	}
}
