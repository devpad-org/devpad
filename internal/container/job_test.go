package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJobNamesAndLabels(t *testing.T) {
	job := Job{Pipeline: "42", Name: "go_test"}
	if err := job.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := job.ContainerName(); got != "devpad-job-42.go_test" {
		t.Errorf("container name = %q", got)
	}
	if got := job.NetworkName(); got != "devpad-job-42.go_test" {
		t.Errorf("network name = %q", got)
	}
	if got := job.ServiceName("postgres"); got != "devpad-job-42.go_test.postgres" {
		t.Errorf("service name = %q", got)
	}
	// A job whose name ends as another job's service would has a name of
	// its own.
	other := Job{Pipeline: "42", Name: "go_test-postgres"}
	if other.ContainerName() == job.ServiceName("postgres") {
		t.Errorf("%s collides with a service of %s", other.Name, job.Name)
	}
	want := map[string]string{"devpad.managed": "true", "devpad.pipeline": "42", "devpad.job": "go_test"}
	if got := job.Labels(); len(got) != len(want) || got["devpad.pipeline"] != "42" || got["devpad.job"] != "go_test" || got["devpad.managed"] != "true" {
		t.Errorf("labels = %v", got)
	}

	for _, bad := range []Job{
		{Pipeline: "", Name: "a"},
		{Pipeline: "1", Name: ""},
		{Pipeline: "Upper", Name: "a"},
		{Pipeline: "1", Name: "a.b"},
		{Pipeline: "../1", Name: "a"},
		{Pipeline: "1", Name: "-a"},
		{Pipeline: "1", Name: strings.Repeat("a", 65)},
		{Pipeline: strings.Repeat("1", 65), Name: "a"},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v: expected it refused", bad)
		}
	}
}

func TestJobFiltersSelectByLabel(t *testing.T) {
	tests := []struct {
		sel  Job
		want []string
	}{
		{Job{}, []string{"devpad.pipeline"}},
		{Job{Pipeline: "7"}, []string{"devpad.pipeline=7"}},
		{Job{Pipeline: "7", Name: "lint"}, []string{"devpad.job=lint", "devpad.pipeline=7"}},
	}
	for _, tt := range tests {
		f, err := jobFilters(tt.sel)
		if err != nil {
			t.Fatalf("%+v: %v", tt.sel, err)
		}
		if got := slices.Sorted(maps.Keys(f["label"])); !slices.Equal(got, tt.want) {
			t.Errorf("%+v: labels %v, want %v", tt.sel, got, tt.want)
		}
	}
	for _, bad := range []Job{{Name: "lint"}, {Pipeline: "7=8"}, {Pipeline: "7", Name: "a,b"}} {
		if _, err := jobFilters(bad); err == nil {
			t.Errorf("%+v: expected it refused", bad)
		}
	}
}

func TestJobContainerSpecValidation(t *testing.T) {
	ok := JobContainerSpec{Job: Job{Pipeline: "1", Name: "a"}, Lifetime: time.Minute,
		Files: []JobFile{{Path: "/tmp/pipeline.bundle", Mode: 0o644, Content: strings.NewReader("")}}}
	if err := ok.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if ok.Limits != DefaultJobLimits() {
		t.Fatalf("expected the job limits by default, got %+v", ok.Limits)
	}
	for name, change := range map[string]func(*JobContainerSpec){
		"no memory":     func(s *JobContainerSpec) { s.Limits = Limits{NanoCPUs: 1, Pids: 1} },
		"no cpu":        func(s *JobContainerSpec) { s.Limits = Limits{Memory: 1, Pids: 1} },
		"no pids":       func(s *JobContainerSpec) { s.Limits = Limits{Memory: 1, NanoCPUs: 1} },
		"no lifetime":   func(s *JobContainerSpec) { s.Lifetime = 0 },
		"bad job":       func(s *JobContainerSpec) { s.Job.Name = "A" },
		"relative file": func(s *JobContainerSpec) { s.Files[0].Path = "tmp/x" },
		"unclean file":  func(s *JobContainerSpec) { s.Files[0].Path = "/tmp/../etc/x" },
		"root":          func(s *JobContainerSpec) { s.Files[0].Path = "/" },
		"no content":    func(s *JobContainerSpec) { s.Files[0].Content = nil },
	} {
		spec := ok
		spec.Files = []JobFile{ok.Files[0]}
		change(&spec)
		if spec.validate() == nil {
			t.Errorf("%s: expected it refused", name)
		}
	}
}

func TestJobResourcesAreTheJobsByName(t *testing.T) {
	labels := Job{Pipeline: "7", Name: "test"}.Labels()
	sidecar := Job{Pipeline: "7", Name: "test"}.Labels()
	sidecar[labelSidecar] = "true"
	tests := []struct {
		name   string
		kind   JobResourceKind
		labels map[string]string
		want   bool
	}{
		{"devpad-job-7.test", JobResourceContainer, labels, true},
		{"devpad-job-7.test.postgres", JobResourceContainer, sidecar, true},
		{"devpad-job-7.test", JobResourceNetwork, labels, true},
		// A workspace whose image carried the labels is not the job's.
		{"devpad-ws-12", JobResourceContainer, labels, false},
		{"devpad-job-7.test-other", JobResourceContainer, labels, false},
		{"devpad-job-8.test", JobResourceContainer, labels, false},
		{"devpad-net-12", JobResourceNetwork, labels, false},
		{"devpad-job-7.test.postgres", JobResourceNetwork, labels, false},
		{"devpad-job-7.test", JobResourceContainer, map[string]string{LabelPipeline: "7"}, false},
	}
	for _, tt := range tests {
		r, ok := jobResource(tt.kind, "id", tt.name, tt.labels)
		if ok != tt.want {
			t.Errorf("%s %s: listed %v, want %v", tt.kind, tt.name, ok, tt.want)
		}
		if ok && (r.Job != Job{Pipeline: "7", Name: "test"} || r.Sidecar != (tt.labels[labelSidecar] == "true")) {
			t.Errorf("%s: unexpected resource %+v", tt.name, r)
		}
	}
}

func TestJobNetworkErrorTellsARanOutPool(t *testing.T) {
	pool := errors.New("Error response from daemon: all predefined address pools have been fully subnetted: could not find an available, non-overlapping IPv4 address pool among the defaults to assign to the network")
	if err := jobNetworkError(pool); !errors.Is(err, ErrNoNetworkPool) || !errors.Is(err, pool) {
		t.Fatalf("expected ErrNoNetworkPool wrapping Docker's error, got %v", err)
	}
	if err := jobNetworkError(errors.New("network with name x already exists")); errors.Is(err, ErrNoNetworkPool) {
		t.Fatalf("expected another failure kept apart, got %v", err)
	}
}

func TestFileArchiveStreamsOneFile(t *testing.T) {
	content := strings.Repeat("bundle", 1000)
	archive := fileArchive("pipeline.bundle", 0o644, int64(len(content)), strings.NewReader(content))
	defer archive.Close()
	tr := tar.NewReader(archive)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("reading header: %v", err)
	}
	if hdr.Name != "pipeline.bundle" || hdr.Mode != 0o644 || hdr.Size != int64(len(content)) {
		t.Fatalf("unexpected header: %+v", hdr)
	}
	got, err := io.ReadAll(tr)
	if err != nil || string(got) != content {
		t.Fatalf("content: %v (%d bytes)", err, len(got))
	}
	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected one file, got %v", err)
	}

	// Content shorter than it was said to be fails the stream.
	short := fileArchive("x", 0o644, 10, strings.NewReader("abc"))
	defer short.Close()
	if _, err := io.ReadAll(short); err == nil {
		t.Fatal("expected a short file to fail the archive")
	}
}

// frame is one chunk of Docker's multiplexed exec output.
func frame(stream byte, data string) []byte {
	hdr := make([]byte, 8)
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(data)))
	return append(hdr, data...)
}

// fakeExec is an exec whose output a test writes and whose state it sets.
type fakeExec struct {
	mu      sync.Mutex
	state   execState
	inspect int
	killed  int
	closed  int
	r       *io.PipeReader
	w       *io.PipeWriter
}

func newFakeExec() *fakeExec {
	r, w := io.Pipe()
	return &fakeExec{r: r, w: w}
}

func (f *fakeExec) set(s execState) {
	f.mu.Lock()
	f.state = s
	f.mu.Unlock()
}

func (f *fakeExec) stream() execStream {
	return execStream{
		output: f.r,
		close: func() {
			f.mu.Lock()
			f.closed++
			f.mu.Unlock()
			f.r.Close()
		},
		inspect: func(context.Context) (execState, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.inspect++
			return f.state, nil
		},
		kill: func() error {
			f.mu.Lock()
			f.killed++
			f.mu.Unlock()
			// Killing the container ends its output.
			f.w.Close()
			return nil
		},
	}
}

// lockedBuffer is an output writer the test can read while followExec
// writes.
type lockedBuffer struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done bool
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		panic("written after followExec returned")
	}
	return b.buf.Write(p)
}

func (b *lockedBuffer) finish() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.done = true
	return b.buf.String()
}

func fastExecPolling(t *testing.T) {
	t.Helper()
	poll, grace := execPoll, execDrainGrace
	execPoll, execDrainGrace = 5*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { execPoll, execDrainGrace = poll, grace })
}

type execResult struct {
	code int
	err  error
}

func follow(ctx context.Context, f *fakeExec, out io.Writer) chan execResult {
	done := make(chan execResult, 1)
	go func() {
		code, err := followExec(ctx, f.stream(), out)
		done <- execResult{code, err}
	}()
	return done
}

func TestFollowExecReturnsTheExitCodeAndAllOutput(t *testing.T) {
	fastExecPolling(t)
	f := newFakeExec()
	// Docker answers the attach before the command starts: not running,
	// with no process, which must not read as exiting with 0.
	f.set(execState{})
	var out lockedBuffer
	done := follow(context.Background(), f, &out)

	f.w.Write(frame(1, "building\n"))
	f.w.Write(frame(2, "warning: x\n"))
	select {
	case r := <-done:
		t.Fatalf("returned before the command started: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	f.set(execState{running: true, pid: 9})
	f.w.Write(frame(1, "done\n"))
	f.set(execState{pid: 9, exitCode: 3})
	f.w.Close()

	r := <-done
	if r.err != nil || r.code != 3 {
		t.Fatalf("got %+v, want exit code 3", r)
	}
	if got := out.finish(); got != "building\nwarning: x\ndone\n" {
		t.Fatalf("output = %q", got)
	}
	if f.killed != 0 {
		t.Fatalf("expected nothing killed, got %d", f.killed)
	}
}

func TestFollowExecWaitsForACommandThatClosedItsOutput(t *testing.T) {
	fastExecPolling(t)
	f := newFakeExec()
	f.set(execState{running: true, pid: 9})
	var out lockedBuffer
	done := follow(context.Background(), f, &out)
	f.w.Write(frame(1, "bye\n"))
	f.w.Close()
	select {
	case r := <-done:
		t.Fatalf("returned while the command still ran: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	f.set(execState{pid: 9, exitCode: 0})
	if r := <-done; r.err != nil || r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	if got := out.finish(); got != "bye\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestFollowExecEndsWhenABackgroundProcessHoldsTheOutput(t *testing.T) {
	fastExecPolling(t)
	f := newFakeExec()
	f.set(execState{running: true, pid: 9})
	var out lockedBuffer
	done := follow(context.Background(), f, &out)
	f.w.Write(frame(1, "started a daemon\n"))
	// The command exits, but what it left running keeps the output open.
	f.set(execState{pid: 9, exitCode: 0})

	select {
	case r := <-done:
		if r.err != nil || r.code != 0 {
			t.Fatalf("got %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("still waiting on output the command no longer writes")
	}
	if got := out.finish(); got != "started a daemon\n" {
		t.Fatalf("output = %q", got)
	}
	if f.closed == 0 || f.killed != 0 {
		t.Fatalf("expected the stream closed and nothing killed, got closed %d killed %d", f.closed, f.killed)
	}
}

func TestFollowExecKillsTheContainerWhenStopped(t *testing.T) {
	fastExecPolling(t)
	timedOut := errors.New("the job ran out of time")
	ctx, cancel := context.WithCancelCause(context.Background())
	f := newFakeExec()
	f.set(execState{running: true, pid: 9})
	var out lockedBuffer
	done := follow(ctx, f, &out)
	f.w.Write(frame(1, "working\n"))
	time.Sleep(20 * time.Millisecond)
	cancel(timedOut)

	r := <-done
	if !errors.Is(r.err, timedOut) || r.code != -1 {
		t.Fatalf("got %+v, want the cause", r)
	}
	out.finish()
	if f.killed != 1 {
		t.Fatalf("expected the container killed once, got %d", f.killed)
	}
}

func TestFollowExecKillsTheContainerWhenItLosesTheOutput(t *testing.T) {
	fastExecPolling(t)
	f := newFakeExec()
	f.set(execState{running: true, pid: 9})
	var out lockedBuffer
	done := follow(context.Background(), f, &out)
	f.w.CloseWithError(errors.New("connection reset"))

	r := <-done
	if r.err == nil || !strings.Contains(r.err.Error(), "connection reset") {
		t.Fatalf("got %+v, want the stream's error", r)
	}
	out.finish()
	if f.killed != 1 {
		t.Fatalf("expected the container killed, got %d", f.killed)
	}
}

func TestFollowExecIgnoresAFailingWriter(t *testing.T) {
	fastExecPolling(t)
	f := newFakeExec()
	f.set(execState{running: true, pid: 9})
	done := follow(context.Background(), f, failingWriter{})
	f.w.Write(frame(1, "a lot of output\n"))
	f.w.Write(frame(1, "more\n"))
	f.set(execState{pid: 9, exitCode: 1})
	f.w.Close()
	if r := <-done; r.err != nil || r.code != 1 {
		t.Fatalf("got %+v", r)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("full") }
