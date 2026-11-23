package container

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// ExecSpec is a command run in a job's container, as JobUser.
type ExecSpec struct {
	Cmd []string
	// WorkingDir is where it runs; empty means the container's own.
	WorkingDir string
	// Env is added to the container's environment for this command.
	Env []string
}

// ExecResult is how a command in a job's container ended.
type ExecResult struct {
	ExitCode int
	// OOMKilled says the container ran out of memory since it started, so
	// the kernel killed a process of it, which is likely why a command that
	// failed did: 137 for the process itself, or whatever a parent made of
	// its child being killed.
	OOMKilled bool
}

// oomSettle is how long ExecJob gives Docker to record an out-of-memory kill
// that a failed command's exit may have overtaken, and oomInspectTimeout
// bounds asking.
var oomSettle = 250 * time.Millisecond

const oomInspectTimeout = 10 * time.Second

// JobScript is the command that runs script as a pipeline job does: with
// bash, in a login shell's environment (so with the image's PATH and the
// variables ~/.profile exports, though not the functions it defines),
// stopping at the first command that fails, a pipeline's included, and
// exiting with the script's own status. The login shell only sets up the
// environment and then makes way for a plain bash: a login shell runs
// ~/.bash_logout when the script calls exit, under its errexit, which on
// Debian's turns exit 0 or exit 3 into 1. The script is always the command,
// even when it starts with a dash.
func JobScript(script string) []string {
	return []string{"bash", "-lc", `exec bash -eo pipefail -c -- "$1"`, "bash", script}
}

var (
	// execPoll is how often ExecJob asks Docker whether the command has
	// exited, besides as soon as its output ends.
	execPoll = 500 * time.Millisecond
	// execDrainGrace is how long ExecJob reads output after the command has
	// exited, which a process it left in the background can hold open.
	execDrainGrace = 2 * time.Second
)

// execKillTimeout bounds killing a container whose command is being
// stopped.
const execKillTimeout = 30 * time.Second

// ExecJob runs a command in a job's container and waits for it to exit,
// writing what it prints, stdout and stderr together, each in order, to out
// as it comes, and returns how it ended. out is never written after ExecJob
// returns, and its errors are ignored, so a failing writer cannot stall the
// command.
//
// Docker cannot stop an exec, so when ctx ends first ExecJob kills the whole
// container, which ends the command and everything it started, and returns
// context.Cause(ctx); the container is then only good for removing. It does
// the same when it loses the command's output while the command may still
// be running.
func (m *manager) ExecJob(ctx context.Context, containerID string, spec ExecSpec, out io.Writer) (ExecResult, error) {
	code, err := m.execJob(ctx, containerID, spec, out)
	if err != nil {
		return ExecResult{ExitCode: -1}, err
	}
	return ExecResult{ExitCode: code, OOMKilled: m.oomKilled(ctx, containerID, code)}, nil
}

// oomKilled reports whether Docker has recorded the container running out
// of memory, whichever of its processes was killed. It records it from an
// event of its own, which can come after the command's exit, so for a
// command that failed it looks again a moment later. The command's exit
// code is known either way, so a container Docker cannot say anything about
// counts as not killed. It asks on a context of its own, since the job's may
// have ended with the command.
func (m *manager) oomKilled(ctx context.Context, containerID string, exitCode int) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), oomInspectTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		info, err := m.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
		if err != nil {
			return false
		}
		if st := info.Container.State; st != nil && st.OOMKilled {
			return true
		}
		if exitCode == 0 || attempt > 0 {
			return false
		}
		select {
		case <-time.After(oomSettle):
		case <-ctx.Done():
			return false
		}
	}
}

func (m *manager) execJob(ctx context.Context, containerID string, spec ExecSpec, out io.Writer) (int, error) {
	if len(spec.Cmd) == 0 {
		return -1, errors.New("running a command in a job: no command")
	}
	created, err := m.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		User:         JobUser,
		WorkingDir:   spec.WorkingDir,
		Env:          spec.Env,
		Cmd:          spec.Cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		if ctx.Err() != nil {
			return -1, context.Cause(ctx)
		}
		return -1, fmt.Errorf("creating the command: %w", err)
	}
	kill := func() error {
		killCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), execKillTimeout)
		defer cancel()
		if _, err := m.cli.ContainerKill(killCtx, containerID, client.ContainerKillOptions{Signal: "KILL"}); err != nil && !isNotFound(err) {
			return fmt.Errorf("killing the job's container: %w", err)
		}
		return nil
	}
	attached, err := m.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		// It may have started all the same.
		killErr := kill()
		if ctx.Err() != nil {
			return -1, context.Cause(ctx)
		}
		return -1, errors.Join(fmt.Errorf("starting the command: %w", err), killErr)
	}
	return followExec(ctx, execStream{
		output: attached.Reader,
		close:  attached.Close,
		inspect: func(ctx context.Context) (execState, error) {
			res, err := m.cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
			if err != nil {
				return execState{}, err
			}
			return execState{running: res.Running, pid: res.PID, exitCode: res.ExitCode}, nil
		},
		kill: kill,
	}, out)
}

// execState is how Docker reports an exec.
type execState struct {
	running  bool
	pid      int
	exitCode int
}

// execStream is a started exec: its multiplexed output, and what ExecJob
// asks Docker of it.
type execStream struct {
	output  io.Reader
	close   func()
	inspect func(ctx context.Context) (execState, error)
	kill    func() error
}

// followExec copies an exec's output to out until it exits, and returns its
// exit code; when ctx ends first it kills the container.
func followExec(ctx context.Context, s execStream, out io.Writer) (int, error) {
	copied := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(ignoreErrors{out}, ignoreErrors{out}, s.output)
		copied <- err
	}()
	streamEnded := false
	// stop ends the stream and waits until nothing more is written to out.
	stop := func() {
		s.close()
		if !streamEnded {
			<-copied
			streamEnded = true
		}
	}
	abandon := func(err error) (int, error) {
		killErr := s.kill()
		stop()
		if ctx.Err() != nil {
			return -1, context.Cause(ctx)
		}
		return -1, errors.Join(err, killErr)
	}

	ticker := time.NewTicker(execPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return abandon(nil)
		case err := <-copied:
			streamEnded = true
			if err != nil {
				// The command may still run with nowhere to write.
				return abandon(fmt.Errorf("reading the command's output: %w", err))
			}
		case <-ticker.C:
		}
		state, err := s.inspect(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return abandon(nil)
			}
			return abandon(fmt.Errorf("checking on the command: %w", err))
		}
		// Docker answers the attach before it starts the command, so one
		// that is not running and has no process yet, while its output is
		// still open, has not started: it has not exited with 0.
		if state.running || (!streamEnded && state.pid == 0) {
			continue
		}
		if !streamEnded {
			// Something it started in the background may hold its output.
			select {
			case <-copied:
				streamEnded = true
			case <-time.After(execDrainGrace):
			case <-ctx.Done():
			}
		}
		stop()
		return state.exitCode, nil
	}
}

// ignoreErrors writes to w, dropping what w refuses.
type ignoreErrors struct{ w io.Writer }

func (i ignoreErrors) Write(p []byte) (int, error) {
	i.w.Write(p)
	return len(p), nil
}

// fileArchive is a tar stream holding one file, name, of size bytes read
// from content, made as it is read so that a large file is never held in
// memory. Reading it fails if content holds fewer bytes. The caller closes
// it, which stops reading content.
func fileArchive(name string, mode, size int64, content io.Reader) io.ReadCloser {
	r, w := io.Pipe()
	go func() {
		tw := tar.NewWriter(w)
		err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: size, Typeflag: tar.TypeReg})
		if err == nil {
			if _, err = io.CopyN(tw, content, size); errors.Is(err, io.EOF) {
				// Short content would otherwise end the stream as if whole.
				err = io.ErrUnexpectedEOF
			}
		}
		if err == nil {
			err = tw.Close()
		}
		w.CloseWithError(err)
	}()
	return r
}
