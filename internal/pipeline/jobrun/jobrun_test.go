package jobrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/wsservice"
)

// fakeRuntime records what a job asks of Docker; run says how its script
// ends.
type fakeRuntime struct {
	mu        sync.Mutex
	calls     []string
	noPool    int
	startErr  error
	cloneHang bool
	spec      container.JobContainerSpec
	bundle    string
	run       func(ctx context.Context, out io.Writer) (container.ExecResult, error)
}

func (f *fakeRuntime) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeRuntime) CreateJobNetwork(_ context.Context, job container.Job) error {
	f.record("network")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noPool > 0 {
		f.noPool--
		return fmt.Errorf("creating the job's network: %w: no pool", container.ErrNoNetworkPool)
	}
	return nil
}

func (f *fakeRuntime) StartJobContainer(_ context.Context, spec container.JobContainerSpec) (string, error) {
	f.record("container")
	if f.startErr != nil {
		return "", f.startErr
	}
	b, err := io.ReadAll(spec.Files[0].Content)
	if err != nil {
		return "", err
	}
	f.spec, f.bundle = spec, string(b)
	return "c1", nil
}

func (f *fakeRuntime) ExecJob(ctx context.Context, _ string, spec container.ExecSpec, out io.Writer) (container.ExecResult, error) {
	if spec.WorkingDir != "/workspace" || len(spec.Env) != 0 {
		return container.ExecResult{ExitCode: -1}, fmt.Errorf("ran %v in %q with %v", spec.Cmd, spec.WorkingDir, spec.Env)
	}
	if spec.Cmd[0] == "git" {
		f.record(strings.Join(spec.Cmd[:2], " "))
		if f.cloneHang {
			<-ctx.Done()
			return container.ExecResult{ExitCode: -1}, context.Cause(ctx)
		}
		return container.ExecResult{}, nil
	}
	f.record("run " + spec.Cmd[len(spec.Cmd)-1])
	return f.run(ctx, out)
}

func (f *fakeRuntime) RemoveJobResources(_ context.Context, sel container.Job) error {
	f.record("remove " + sel.Name)
	return nil
}

type fakeServices struct {
	runtime *fakeRuntime
	err     error
}

func (f *fakeServices) StartJobServices(_ context.Context, req wsservice.JobServicesRequest) (*wsservice.JobServices, error) {
	f.runtime.record(fmt.Sprintf("services %v", req.Types))
	if f.err != nil {
		return nil, f.err
	}
	return &wsservice.JobServices{Env: []string{"POSTGRES_HOST=postgres"}}, nil
}

func (f *fakeServices) RemoveJobServices(context.Context, *wsservice.JobServices) error {
	f.runtime.record("remove services")
	return nil
}

func newRunner(t *testing.T, run func(ctx context.Context, out io.Writer) (container.ExecResult, error)) (*Runner, *fakeRuntime, *fakeServices, Spec) {
	t.Helper()
	rt := &fakeRuntime{run: run}
	svc := &fakeServices{runtime: rt}
	path := filepath.Join(t.TempDir(), "pipeline.bundle")
	if err := os.WriteFile(path, []byte("the bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		Job:       container.Job{Pipeline: "0123456789abcdef", Name: "test"},
		ProjectID: 7,
		Run:       "go test ./...",
		Timeout:   time.Minute,
		Services:  []string{"postgres"},
		Bundle:    Bundle{Path: path, Size: 10},
	}
	r := New(rt, svc, Config{NetworkRetry: time.Millisecond, NetworkWait: 50 * time.Millisecond, CloneTimeout: 100 * time.Millisecond})
	return r, rt, svc, spec
}

func exits(code int, oom bool) func(context.Context, io.Writer) (container.ExecResult, error) {
	return func(_ context.Context, out io.Writer) (container.ExecResult, error) {
		fmt.Fprintln(out, "ok")
		return container.ExecResult{ExitCode: code, OOMKilled: oom}, nil
	}
}

func TestRunSetsUpRunsAndRemovesAJob(t *testing.T) {
	r, rt, _, spec := newRunner(t, exits(0, false))
	var out bytes.Buffer
	res := r.Run(context.Background(), spec, &out)
	if res.Outcome != Passed || res.ExitCode == nil || *res.ExitCode != 0 || res.Error != "" {
		t.Fatalf("result: %+v", res)
	}
	want := []string{"network", "services [postgres]", "container", "git init", "git fetch", "git checkout", "run go test ./...", "remove services", "remove test"}
	if strings.Join(rt.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls:\n%v\nwant\n%v", rt.calls, want)
	}
	if rt.bundle != "the bundle" || rt.spec.Files[0].Path != "/tmp/pipeline.bundle" || rt.spec.Files[0].Size != 10 {
		t.Errorf("bundle: %q %+v", rt.bundle, rt.spec.Files[0])
	}
	if rt.spec.Image != container.WorkspaceImage || rt.spec.Limits != container.DefaultJobLimits() || strings.Join(rt.spec.Env, ",") != "POSTGRES_HOST=postgres" {
		t.Errorf("spec: %+v", rt.spec)
	}
	if rt.spec.Lifetime != 100*time.Millisecond+time.Minute+lifetimeMargin {
		t.Errorf("lifetime %s", rt.spec.Lifetime)
	}
	for _, line := range []string{"devpad: creating the job's network\n", "devpad: starting postgres and waiting until it is ready\n", "ok\n", "devpad: the job passed\n"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("expected %q in:\n%s", line, out.String())
		}
	}
}

func TestRunTellsHowAScriptEnded(t *testing.T) {
	tests := []struct {
		name    string
		run     func(context.Context, io.Writer) (container.ExecResult, error)
		outcome Outcome
		code    int
		err     string
	}{
		{"failed", exits(2, false), Failed, 2, "the job exited with 2"},
		{"out of memory", exits(137, true), OutOfMemory, 137, "the job ran out of memory (4 GB) and exited with 137"},
		{"exit 0 though something was killed", exits(0, true), Passed, 0, ""},
		{"lost", func(context.Context, io.Writer) (container.ExecResult, error) {
			return container.ExecResult{ExitCode: -1}, errors.New("reading the command's output: EOF")
		}, Error, -1, "running the job: reading the command's output: EOF"},
	}
	for _, tc := range tests {
		r, _, _, spec := newRunner(t, tc.run)
		res := r.Run(context.Background(), spec, io.Discard)
		code := -1
		if res.ExitCode != nil {
			code = *res.ExitCode
		}
		if res.Outcome != tc.outcome || code != tc.code || res.Error != tc.err {
			t.Errorf("%s: %+v (exit %d)", tc.name, res, code)
		}
	}
}

func TestRunStopsAScriptAtItsTimeout(t *testing.T) {
	r, rt, _, spec := newRunner(t, func(ctx context.Context, _ io.Writer) (container.ExecResult, error) {
		<-ctx.Done()
		return container.ExecResult{ExitCode: -1}, context.Cause(ctx)
	})
	spec.Timeout = 20 * time.Millisecond
	res := r.Run(context.Background(), spec, io.Discard)
	if res.Outcome != TimedOut || res.ExitCode != nil || res.Error != "the job ran for longer than its timeout of 20ms" {
		t.Fatalf("result: %+v", res)
	}
	if last := rt.calls[len(rt.calls)-1]; last != "remove test" {
		t.Fatalf("not cleaned up: %v", rt.calls)
	}
}

func TestRunIsStoppedWithItsContext(t *testing.T) {
	why := errors.New("cancelled by the user")
	ctx, cancel := context.WithCancelCause(context.Background())
	r, rt, _, spec := newRunner(t, func(ctx context.Context, _ io.Writer) (container.ExecResult, error) {
		cancel(why)
		<-ctx.Done()
		return container.ExecResult{ExitCode: -1}, context.Cause(ctx)
	})
	var out bytes.Buffer
	res := r.Run(ctx, spec, &out)
	if res.Outcome != Stopped || res.Error != why.Error() {
		t.Fatalf("result: %+v", res)
	}
	if !strings.HasSuffix(out.String(), "devpad: the job was stopped: cancelled by the user\n") {
		t.Errorf("output:\n%s", out.String())
	}
	if last := rt.calls[len(rt.calls)-1]; last != "remove test" {
		t.Fatalf("not cleaned up: %v", rt.calls)
	}
}

func TestRunReportsWhatCouldNotBeSetUp(t *testing.T) {
	t.Run("a service the project lacks", func(t *testing.T) {
		r, rt, svc, spec := newRunner(t, exits(0, false))
		svc.err = fmt.Errorf("redis: %w", wsservice.ErrNotProjectService)
		res := r.Run(context.Background(), spec, io.Discard)
		if res.Outcome != Error || res.Error != "redis: the project has no such service: add it to the project's services, or take it out of the job" {
			t.Fatalf("result: %+v", res)
		}
		if strings.Join(rt.calls, "|") != "network|services [postgres]|remove test" {
			t.Fatalf("calls: %v", rt.calls)
		}
	})
	t.Run("no network pool for too long", func(t *testing.T) {
		r, rt, _, spec := newRunner(t, exits(0, false))
		rt.noPool = 1 << 30
		var out bytes.Buffer
		res := r.Run(context.Background(), spec, &out)
		if res.Outcome != Error || !strings.Contains(res.Error, "no address pool left") || !strings.Contains(res.Error, "default-address-pools") {
			t.Fatalf("result: %+v", res)
		}
		if strings.Count(out.String(), "waiting for one to be freed") != 1 {
			t.Errorf("output:\n%s", out.String())
		}
	})
	t.Run("a container Docker cannot start", func(t *testing.T) {
		r, rt, _, spec := newRunner(t, exits(0, false))
		rt.startErr = errors.New("creating the job's container: no such image")
		res := r.Run(context.Background(), spec, io.Discard)
		if res.Outcome != Error || res.Error != "creating the job's container: no such image" {
			t.Fatalf("result: %+v", res)
		}
		if strings.Join(rt.calls[len(rt.calls)-2:], "|") != "remove services|remove test" {
			t.Fatalf("calls: %v", rt.calls)
		}
	})
	t.Run("a clone that takes too long", func(t *testing.T) {
		r, rt, _, spec := newRunner(t, exits(0, false))
		rt.cloneHang = true
		res := r.Run(context.Background(), spec, io.Discard)
		if res.Outcome != Error || res.Error != "cloning the commit under test took over 100ms" {
			t.Fatalf("result: %+v", res)
		}
	})
	t.Run("nothing to clone", func(t *testing.T) {
		r, rt, _, spec := newRunner(t, exits(0, false))
		spec.Bundle = Bundle{}
		if res := r.Run(context.Background(), spec, io.Discard); res.Outcome != Error || len(rt.calls) != 0 {
			t.Fatalf("result: %+v, calls %v", res, rt.calls)
		}
	})
}

func TestCloneNamesNothingOfTheRepo(t *testing.T) {
	for _, cmd := range cloneCommands("0123456789abcdef") {
		for _, arg := range cmd {
			if strings.ContainsAny(arg, "$`;|&()'\" ") {
				t.Fatalf("%v carries %q", cmd, arg)
			}
		}
	}
}
