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
	content := "bundle bytes"
	spec := JobContainerSpec{
		Job:      job,
		Env:      []string{"POSTGRES_HOST=postgres"},
		Files:    []JobFile{{Path: "/tmp/pipeline.bundle", Mode: 0o644, Size: int64(len(content)), Content: strings.NewReader(content)}},
		Limits:   Limits{Memory: 256 << 20, NanoCPUs: 1_000_000_000, Pids: 256},
		Lifetime: 2 * time.Minute,
	}
	id, err := m.StartJobContainer(ctx, spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// It is made as a job's container: no agent, devpad's user, limits, no
	// restart, no way to root, its labels, and on its own network only.
	info, err := m.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	c := info.Container
	if c.Config.User != JobUser || strings.Join(c.Config.Entrypoint, " ") != "sleep" || c.Config.Labels[LabelJob] != "unit_tests" {
		t.Errorf("unexpected config: user %q entrypoint %v labels %v", c.Config.User, c.Config.Entrypoint, c.Config.Labels)
	}
	hc := c.HostConfig
	if hc.Memory != spec.Limits.Memory || hc.MemorySwap != spec.Limits.Memory || hc.NanoCPUs != spec.Limits.NanoCPUs || hc.PidsLimit == nil || *hc.PidsLimit != spec.Limits.Pids {
		t.Errorf("unexpected limits: %+v", hc.Resources)
	}
	if !hc.RestartPolicy.IsNone() || hc.Init == nil || !*hc.Init {
		t.Errorf("unexpected restart policy %v or init %v", hc.RestartPolicy, hc.Init)
	}
	if strings.Join(hc.CapDrop, ",") != "ALL" || strings.Join(hc.SecurityOpt, ",") != "no-new-privileges:true" {
		t.Errorf("expected no capabilities and no new privileges, got %v %v", hc.CapDrop, hc.SecurityOpt)
	}
	if len(c.NetworkSettings.Networks) != 1 || c.NetworkSettings.Networks[job.NetworkName()] == nil {
		t.Errorf("expected only the job's network, got %v", c.NetworkSettings.Networks)
	}
	for _, kv := range c.Config.Env {
		if strings.HasPrefix(kv, "AGENT_AUTH_TOKEN=") {
			t.Errorf("the job's container got the agent's token")
		}
	}

	run := func(ctx context.Context, script string) (ExecResult, string, error) {
		var out bytes.Buffer
		res, err := m.ExecJob(ctx, id, ExecSpec{Cmd: JobScript(script), WorkingDir: "/workspace"}, &out)
		return res, out.String(), err
	}

	// The file put in before it started, read as devpad from a login shell
	// that keeps the image's PATH; stdout and stderr both reach the output.
	imagePath := ""
	for _, kv := range c.Config.Env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			imagePath = v
		}
	}
	res, out, err := run(ctx, `id -un; pwd; cat /tmp/pipeline.bundle; echo; echo "$POSTGRES_HOST"; echo "PATH=$PATH"; echo oops >&2; exit 3`)
	if err != nil || res.ExitCode != 3 || res.OOMKilled {
		t.Fatalf("exec: %+v %v\n%s", res, err, out)
	}
	for _, want := range []string{"devpad\n", "/workspace\n", content, "postgres\n", "oops\n", "PATH=" + imagePath + "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the output:\n%s", want, out)
		}
	}

	// A job's script stops at the first command that fails, a pipeline's
	// too, and exits with its own status; one starting with a dash is
	// still the command.
	for script, want := range map[string]int{
		"true":                     0,
		"echo ok; exit 0":          0,
		"false; echo never":        1,
		"(exit 5); echo never":     5,
		"false | true; echo never": 1,
		"-x":                       127,
		"--version":                127,
	} {
		if res, out, err := run(ctx, script); err != nil || res.ExitCode != want || strings.Contains(out, "never") {
			t.Errorf("%q: exit %d, want %d: %v\n%s", script, res.ExitCode, want, err, out)
		}
	}

	// The work a job does still works without root: git, scripts, many
	// processes. sudo is refused, and no process has a capability.
	work := `cd /workspace && git init -q && git -c user.name=ci -c user.email=ci@example.com commit -q --allow-empty -m one && git log --oneline | wc -l
printf '#!/bin/sh\necho ran\n' > /tmp/s.sh && chmod +x /tmp/s.sh && /tmp/s.sh
for i in $(seq 1 50); do (true) & done; wait
grep CapEff /proc/self/status
if sudo -n true 2>/dev/null; then echo got-root; fi`
	if res, out, err := run(ctx, work); err != nil || res.ExitCode != 0 || !strings.Contains(out, "1\nran\n") || !strings.Contains(out, "CapEff:\t0000000000000000") || strings.Contains(out, "got-root") {
		t.Fatalf("unprivileged work: %+v %v\n%s", res, err, out)
	}

	// A process left in the background does not keep the command running.
	began := time.Now()
	if res, out, err := run(ctx, "sleep 60 & echo left"); err != nil || res.ExitCode != 0 || !strings.Contains(out, "left") {
		t.Fatalf("exec with a background process: %+v %v %s", res, err, out)
	}
	if took := time.Since(began); took > 20*time.Second {
		t.Fatalf("waited %s for a background process", took)
	}

	// Running out of memory is told apart from failing.
	res, out, err = run(ctx, "head -c 1G /dev/zero | tail > /dev/null")
	if err != nil || res.ExitCode != 137 || !res.OOMKilled {
		t.Fatalf("expected an out-of-memory kill: %+v %v\n%s", res, err, out)
	}

	// Running out of time kills the container, and everything in it.
	timedOut := errors.New("timed out")
	short, cancelShort := context.WithTimeoutCause(ctx, 2*time.Second, timedOut)
	defer cancelShort()
	began = time.Now()
	res, _, err = run(short, "echo start; sleep 60; echo never")
	if !errors.Is(err, timedOut) || res.ExitCode != -1 {
		t.Fatalf("expected the timeout's cause, got %+v %v", res, err)
	}
	if took := time.Since(began); took > 30*time.Second {
		t.Fatalf("took %s to stop", took)
	}
	info, err = m.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil || info.Container.State.Running {
		t.Fatalf("expected the container killed: %v %+v", err, info.Container.State)
	}

	// A service of the job's, limited, never restarted, and reachable by
	// its alias on the job's network.
	sidecarID, err := m.CreateSidecar(ctx, SidecarSpec{
		Name: job.ServiceName("svc"), Image: WorkspaceImage, Network: job.NetworkName(), NetworkAlias: "svc",
		Labels: job.Labels(), NoRestart: true, Limits: DefaultJobServiceLimits(),
	})
	if err != nil {
		t.Fatalf("sidecar: %v", err)
	}
	info, err = m.cli.ContainerInspect(ctx, sidecarID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("inspect sidecar: %v", err)
	}
	shc := info.Container.HostConfig
	if shc.Memory != DefaultJobServiceLimits().Memory || shc.NanoCPUs != DefaultJobServiceLimits().NanoCPUs || shc.PidsLimit == nil || *shc.PidsLimit != DefaultJobServiceLimits().Pids || !shc.RestartPolicy.IsNone() {
		t.Errorf("unexpected sidecar host config: %+v %v", shc.Resources, shc.RestartPolicy)
	}
	if ep := info.Container.NetworkSettings.Networks[job.NetworkName()]; ep == nil || !strings.Contains(strings.Join(ep.Aliases, ","), "svc") {
		t.Errorf("expected the alias on the job's network, got %+v", ep)
	}

	// Its services and its network are found by their labels and names,
	// and removed.
	resources, err := m.ListJobResources(ctx, Job{Pipeline: pipeline})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	kinds := map[JobResourceKind]int{}
	sidecars := 0
	for _, r := range resources {
		kinds[r.Kind]++
		if r.Sidecar {
			sidecars++
		}
		if r.Job != job {
			t.Errorf("resource %s labelled as %+v", r.Name, r.Job)
		}
	}
	if kinds[JobResourceContainer] != 2 || sidecars != 1 || kinds[JobResourceNetwork] != 1 {
		t.Fatalf("expected the job's container, its service and its network, got %+v", resources)
	}
	if err := m.RemoveJobResources(ctx, Job{Pipeline: pipeline}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if resources, err := m.ListJobResources(ctx, Job{Pipeline: pipeline}); err != nil || len(resources) != 0 {
		t.Fatalf("expected nothing left: %+v %v", resources, err)
	}
	if err := m.RemoveSidecar(ctx, sidecarID); err != nil {
		t.Fatalf("removing a sidecar already gone: %v", err)
	}

	// A container whose time is up stops by itself, ending what runs in it.
	if err := m.CreateJobNetwork(ctx, job); err != nil {
		t.Fatalf("network again: %v", err)
	}
	spec.Files = nil
	spec.Lifetime = 2 * time.Second
	id, err = m.StartJobContainer(ctx, spec)
	if err != nil {
		t.Fatalf("start again: %v", err)
	}
	if res, out, err := run(ctx, "sleep 30"); err != nil || res.ExitCode != 137 || res.OOMKilled {
		t.Fatalf("expected the lifetime to end the command: %+v %v\n%s", res, err, out)
	}
}
