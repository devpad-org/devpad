package container

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

// TestJobContainerWithDocker runs a pipeline job's container on a real
// Docker daemon, which must already have devpad's workspace image. It runs
// only when DEVPAD_DOCKER_TEST is set, and everything it makes is labelled
// with a pipeline id of its own and removed.
func TestJobContainerWithDocker(t *testing.T) {
	if os.Getenv("DEVPAD_DOCKER_TEST") == "" {
		t.Skip("set DEVPAD_DOCKER_TEST to run against the local Docker daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	m := mgr.(*manager)
	if _, err := m.ImageID(ctx, WorkspaceImage); err != nil {
		t.Skipf("no workspace image: %v", err)
	}
	suffix := make([]byte, 4)
	rand.Read(suffix)
	pipeline := "devpad-test-" + hex.EncodeToString(suffix)
	t.Cleanup(func() {
		if err := m.RemoveJobResources(context.Background(), Job{Pipeline: pipeline}); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	job := Job{Pipeline: pipeline, Name: "unit_tests"}
	if err := m.CreateJobNetwork(ctx, job); err != nil {
		t.Fatalf("network: %v", err)
	}
	spec := JobContainerSpec{
		Job: job, Env: []string{"POSTGRES_HOST=postgres"},
		Memory: 512 << 20, NanoCPUs: 1_000_000_000, Pids: 256, Lifetime: 2 * time.Minute,
	}
	id, err := m.StartJobContainer(ctx, spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// It is made as a job's container: no agent, devpad's user, limits, no
	// restart, its labels, and on its own network only.
	info, err := m.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	c := info.Container
	if c.Config.User != JobUser || strings.Join(c.Config.Entrypoint, " ") != "sleep" || c.Config.Labels[LabelJob] != "unit_tests" {
		t.Errorf("unexpected config: user %q entrypoint %v labels %v", c.Config.User, c.Config.Entrypoint, c.Config.Labels)
	}
	hc := c.HostConfig
	if hc.Memory != spec.Memory || hc.MemorySwap != spec.Memory || hc.NanoCPUs != spec.NanoCPUs || hc.PidsLimit == nil || *hc.PidsLimit != spec.Pids {
		t.Errorf("unexpected limits: %+v", hc.Resources)
	}
	if !hc.RestartPolicy.IsNone() || hc.Init == nil || !*hc.Init {
		t.Errorf("unexpected restart policy %v or init %v", hc.RestartPolicy, hc.Init)
	}
	if len(c.NetworkSettings.Networks) != 1 || c.NetworkSettings.Networks[job.NetworkName()] == nil {
		t.Errorf("expected only the job's network, got %v", c.NetworkSettings.Networks)
	}
	for _, kv := range c.Config.Env {
		if strings.HasPrefix(kv, "AGENT_AUTH_TOKEN=") {
			t.Errorf("the job's container got the agent's token")
		}
	}

	run := func(ctx context.Context, script string) (int, string, error) {
		var out bytes.Buffer
		code, err := m.ExecJob(ctx, id, ExecSpec{Cmd: JobScript(script), WorkingDir: "/workspace"}, &out)
		return code, out.String(), err
	}

	// A file copied in, read as devpad from a login shell that keeps the
	// image's PATH; stdout and stderr both reach the output.
	content := "bundle bytes"
	archive := FileArchive("pipeline.bundle", 0o644, int64(len(content)), strings.NewReader(content))
	if err := m.CopyToContainer(ctx, id, "/tmp", archive); err != nil {
		t.Fatalf("copy: %v", err)
	}
	archive.Close()
	imagePath := ""
	for _, kv := range c.Config.Env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			imagePath = v
		}
	}
	code, out, err := run(ctx, `id -un; pwd; cat /tmp/pipeline.bundle; echo; echo "$POSTGRES_HOST"; echo "PATH=$PATH"; echo oops >&2; exit 3`)
	if err != nil || code != 3 {
		t.Fatalf("exec: %d %v\n%s", code, err, out)
	}
	for _, want := range []string{"devpad\n", "/workspace\n", content, "postgres\n", "oops\n", "PATH=" + imagePath + "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the output:\n%s", want, out)
		}
	}
	// A job's script stops at the first command that fails, a pipeline's
	// too, and exits with its own status.
	for script, want := range map[string]int{
		"true":                     0,
		"echo ok; exit 0":          0,
		"false; echo never":        1,
		"(exit 5); echo never":     5,
		"false | true; echo never": 1,
	} {
		if code, out, err := run(ctx, script); err != nil || code != want || strings.Contains(out, "never") {
			t.Errorf("%q: exit %d, want %d: %v\n%s", script, code, want, err, out)
		}
	}

	// A process left in the background does not keep the command running.
	began := time.Now()
	if code, out, err := run(ctx, "sleep 60 & echo left"); err != nil || code != 0 || !strings.Contains(out, "left") {
		t.Fatalf("exec with a background process: %d %v %s", code, err, out)
	}
	if took := time.Since(began); took > 20*time.Second {
		t.Fatalf("waited %s for a background process", took)
	}

	// Running out of time kills the container, and everything in it.
	timedOut := errors.New("timed out")
	short, cancelShort := context.WithTimeoutCause(ctx, 2*time.Second, timedOut)
	defer cancelShort()
	began = time.Now()
	code, _, err = run(short, "echo start; sleep 60; echo never")
	if !errors.Is(err, timedOut) || code != -1 {
		t.Fatalf("expected the timeout's cause, got %d %v", code, err)
	}
	if took := time.Since(began); took > 30*time.Second {
		t.Fatalf("took %s to stop", took)
	}
	info, err = m.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil || info.Container.State.Running {
		t.Fatalf("expected the container killed: %v %+v", err, info.Container.State)
	}

	// Its services and its network are found by its labels, and removed.
	resources, err := m.ListJobResources(ctx, Job{Pipeline: pipeline})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	kinds := map[JobResourceKind]int{}
	for _, r := range resources {
		kinds[r.Kind]++
		if r.Job != job {
			t.Errorf("resource %s labelled as %+v", r.Name, r.Job)
		}
	}
	if kinds[JobContainer] != 1 || kinds[JobNetwork] != 1 {
		t.Fatalf("expected one container and one network, got %+v", resources)
	}
	if err := m.RemoveJobResources(ctx, Job{Pipeline: pipeline}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if resources, err := m.ListJobResources(ctx, Job{Pipeline: pipeline}); err != nil || len(resources) != 0 {
		t.Fatalf("expected nothing left: %+v %v", resources, err)
	}

	// A container whose time is up stops by itself.
	if err := m.CreateJobNetwork(ctx, job); err != nil {
		t.Fatalf("network again: %v", err)
	}
	spec.Lifetime = time.Second
	id, err = m.StartJobContainer(ctx, spec)
	if err != nil {
		t.Fatalf("start again: %v", err)
	}
	stopped := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline) && !stopped; time.Sleep(200 * time.Millisecond) {
		info, err := m.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		stopped = err == nil && !info.Container.State.Running
	}
	if !stopped {
		t.Fatal("the container outlived its lifetime")
	}
}
