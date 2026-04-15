package container

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

// Manager handles Docker container lifecycle operations.
type Manager interface {
	Create(ctx context.Context, name string) (containerID string, err error)
	Start(ctx context.Context, containerID string) error
	Stop(ctx context.Context, containerID string) error
	Remove(ctx context.Context, containerID string) error
	Exec(ctx context.Context, containerID string, cmd []string) (execID string, err error)
	ExecAttach(ctx context.Context, execID string) (HijackedResponse, error)
	ExecResize(ctx context.Context, execID string, height, width uint) error
}

// HijackedResponse wraps the Docker hijacked connection for exec.
type HijackedResponse struct {
	Conn   io.WriteCloser
	Reader io.Reader
	Closer func()
}

type manager struct {
	cli *client.Client
}

// NewManager creates a new container Manager using the default Docker client.
func NewManager() (Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &manager{cli: cli}, nil
}

func (m *manager) Create(ctx context.Context, name string) (string, error) {
	// Ensure the ubuntu:24.04 image is available
	if err := m.pullImageIfNeeded(ctx, "ubuntu:24.04"); err != nil {
		return "", fmt.Errorf("pulling image: %w", err)
	}

	containerName := fmt.Sprintf("devpad-ws-%s", name)

	resp, err := m.cli.ContainerCreate(ctx,
		&container.Config{
			Image: "ubuntu:24.04",
			Cmd:   []string{"sleep", "infinity"},
			Tty:   true,
			Labels: map[string]string{
				"devpad.managed": "true",
			},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
		nil, nil, containerName,
	)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}

	return resp.ID, nil
}

func (m *manager) Start(ctx context.Context, containerID string) error {
	if err := m.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}
	return nil
}

func (m *manager) Stop(ctx context.Context, containerID string) error {
	if err := m.cli.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}
	return nil
}

func (m *manager) Remove(ctx context.Context, containerID string) error {
	if err := m.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("removing container: %w", err)
	}
	return nil
}

func (m *manager) Exec(ctx context.Context, containerID string, cmd []string) (string, error) {
	resp, err := m.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
	})
	if err != nil {
		return "", fmt.Errorf("creating exec: %w", err)
	}
	return resp.ID, nil
}

func (m *manager) ExecAttach(ctx context.Context, execID string) (HijackedResponse, error) {
	resp, err := m.cli.ContainerExecAttach(ctx, execID, container.ExecAttachOptions{
		Tty: true,
	})
	if err != nil {
		return HijackedResponse{}, fmt.Errorf("attaching exec: %w", err)
	}
	return HijackedResponse{
		Conn:   resp.Conn,
		Reader: resp.Reader,
		Closer: resp.Close,
	}, nil
}

func (m *manager) ExecResize(ctx context.Context, execID string, height, width uint) error {
	if err := m.cli.ContainerExecResize(ctx, execID, container.ResizeOptions{
		Height: height,
		Width:  width,
	}); err != nil {
		return fmt.Errorf("resizing exec: %w", err)
	}
	return nil
}

func (m *manager) pullImageIfNeeded(ctx context.Context, imageName string) error {
	_, _, err := m.cli.ImageInspectWithRaw(ctx, imageName)
	if err == nil {
		return nil // image already exists
	}

	log.Printf("pulling image %s...", imageName)
	reader, err := m.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling %s: %w", imageName, err)
	}
	defer reader.Close()

	// Consume the pull output to completion
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return fmt.Errorf("reading pull output: %w", err)
	}

	log.Printf("image %s pulled successfully", imageName)
	return nil
}
