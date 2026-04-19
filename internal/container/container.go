package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// WorkspaceImage is the Docker image used for workspace containers.
// Build it from docker/workspace/Dockerfile.
const WorkspaceImage = "devpad-workspace:latest"

// ContainerStats holds resource usage metrics for a container.
type ContainerStats struct {
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryUsage   uint64  `json:"memoryUsage"`
	MemoryLimit   uint64  `json:"memoryLimit"`
	MemoryPercent float64 `json:"memoryPercent"`
	NetworkRx     uint64  `json:"networkRx"`
	NetworkTx     uint64  `json:"networkTx"`
	BlockRead     uint64  `json:"blockRead"`
	BlockWrite    uint64  `json:"blockWrite"`
	PIDs          uint64  `json:"pids"`
}

// Manager handles Docker container lifecycle operations.
type Manager interface {
	Create(ctx context.Context, name, volumeName, networkName string, env []string, memoryLimit, nanoCPUs int64) (containerID string, err error)
	Start(ctx context.Context, containerID string) error
	Stop(ctx context.Context, containerID string) error
	Restart(ctx context.Context, containerID string) error
	Remove(ctx context.Context, containerID string) error
	UpdateResources(ctx context.Context, containerID string, memoryLimit, nanoCPUs int64) error
	GetIP(ctx context.Context, containerID, networkName string) (string, error)
	GetEnv(ctx context.Context, containerID string) ([]string, error)
	Stats(ctx context.Context, containerID string) (*ContainerStats, error)
	CreateNetwork(ctx context.Context, name string) error
	RemoveNetwork(ctx context.Context, name string) error
	ConnectToNetwork(ctx context.Context, networkName, containerID string) error
	CreateVolume(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
	Exec(ctx context.Context, containerID string, cmd []string) (execID string, err error)
	ExecAttach(ctx context.Context, execID string) (HijackedResponse, error)
	ExecResize(ctx context.Context, execID string, height, width uint) error
	CopyFileToContainer(ctx context.Context, containerID, destPath string, fileContent []byte, mode int64) error
	BuildImage(ctx context.Context, dockerfileContent []byte, agentBinary []byte, version string) error
	CreateSidecar(ctx context.Context, name, imageName, volumeName, networkName string, env []string, port int) (containerID string, err error)
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

func (m *manager) Create(ctx context.Context, name, volumeName, networkName string, env []string, memoryLimit, nanoCPUs int64) (string, error) {
	containerName := fmt.Sprintf("devpad-ws-%s", name)

	var mounts []mount.Mount
	if volumeName != "" {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: volumeName,
			Target: "/workspace",
		})
	}

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		Mounts:        mounts,
		Resources: container.Resources{
			Memory:   memoryLimit,
			NanoCPUs: nanoCPUs,
		},
	}

	var networkingConfig *network.NetworkingConfig
	if networkName != "" {
		networkingConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				networkName: {},
			},
		}
	}

	resp, err := m.cli.ContainerCreate(ctx,
		&container.Config{
			Image: WorkspaceImage,
			Tty:   true,
			Env:   env,
			Labels: map[string]string{
				"devpad.managed": "true",
			},
		},
		hostConfig,
		networkingConfig, nil, containerName,
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

func (m *manager) Restart(ctx context.Context, containerID string) error {
	if err := m.cli.ContainerRestart(ctx, containerID, container.StopOptions{}); err != nil {
		return fmt.Errorf("restarting container: %w", err)
	}
	return nil
}

func (m *manager) CopyFileToContainer(ctx context.Context, containerID, destPath string, fileContent []byte, mode int64) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: destPath,
		Size: int64(len(fileContent)),
		Mode: mode,
	}); err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}
	if _, err := tw.Write(fileContent); err != nil {
		return fmt.Errorf("writing tar content: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}

	if err := m.cli.CopyToContainer(ctx, containerID, "/", &buf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copying file to container: %w", err)
	}
	return nil
}

func (m *manager) Remove(ctx context.Context, containerID string) error {
	if err := m.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("removing container: %w", err)
	}
	return nil
}

func (m *manager) UpdateResources(ctx context.Context, containerID string, memoryLimit, nanoCPUs int64) error {
	_, err := m.cli.ContainerUpdate(ctx, containerID, container.UpdateConfig{
		Resources: container.Resources{
			Memory:   memoryLimit,
			NanoCPUs: nanoCPUs,
		},
	})
	if err != nil {
		return fmt.Errorf("updating container resources: %w", err)
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

func (m *manager) GetIP(ctx context.Context, containerID, networkName string) (string, error) {
	info, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("inspecting container: %w", err)
	}

	// If a custom network is specified, look up the IP on that network.
	if networkName != "" && info.NetworkSettings.Networks != nil {
		if ep, ok := info.NetworkSettings.Networks[networkName]; ok && ep.IPAddress != "" {
			return ep.IPAddress, nil
		}
	}

	// Fall back to the default bridge IP for backward compatibility.
	ip := info.NetworkSettings.IPAddress
	if ip == "" {
		return "", fmt.Errorf("container has no IP address")
	}
	return ip, nil
}

func (m *manager) GetEnv(ctx context.Context, containerID string) ([]string, error) {
	info, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}
	return info.Config.Env, nil
}

func (m *manager) Stats(ctx context.Context, containerID string) (*ContainerStats, error) {
	resp, err := m.cli.ContainerStats(ctx, containerID, false)
	if err != nil {
		return nil, fmt.Errorf("fetching container stats: %w", err)
	}
	defer resp.Body.Close()

	var raw struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
			OnlineCPUs     uint32 `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		MemoryStats struct {
			Usage uint64 `json:"usage"`
			Limit uint64 `json:"limit"`
			Stats struct {
				InactiveFile uint64 `json:"inactive_file"`
			} `json:"stats"`
		} `json:"memory_stats"`
		Networks map[string]struct {
			RxBytes uint64 `json:"rx_bytes"`
			TxBytes uint64 `json:"tx_bytes"`
		} `json:"networks"`
		BlkioStats struct {
			IoServiceBytesRecursive []struct {
				Op    string `json:"op"`
				Value uint64 `json:"value"`
			} `json:"io_service_bytes_recursive"`
		} `json:"blkio_stats"`
		PidsStats struct {
			Current uint64 `json:"current"`
		} `json:"pids_stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding container stats: %w", err)
	}

	// Calculate CPU percentage
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemCPUUsage - raw.PreCPUStats.SystemCPUUsage)
	var cpuPercent float64
	if sysDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / sysDelta) * float64(raw.CPUStats.OnlineCPUs) * 100.0
	}

	// Memory usage minus cache
	memUsage := raw.MemoryStats.Usage - raw.MemoryStats.Stats.InactiveFile
	var memPercent float64
	if raw.MemoryStats.Limit > 0 {
		memPercent = float64(memUsage) / float64(raw.MemoryStats.Limit) * 100.0
	}

	// Network totals
	var netRx, netTx uint64
	for _, iface := range raw.Networks {
		netRx += iface.RxBytes
		netTx += iface.TxBytes
	}

	// Block I/O totals
	var blockRead, blockWrite uint64
	for _, entry := range raw.BlkioStats.IoServiceBytesRecursive {
		switch entry.Op {
		case "read", "Read":
			blockRead += entry.Value
		case "write", "Write":
			blockWrite += entry.Value
		}
	}

	return &ContainerStats{
		CPUPercent:    cpuPercent,
		MemoryUsage:   memUsage,
		MemoryLimit:   raw.MemoryStats.Limit,
		MemoryPercent: memPercent,
		NetworkRx:     netRx,
		NetworkTx:     netTx,
		BlockRead:     blockRead,
		BlockWrite:    blockWrite,
		PIDs:          raw.PidsStats.Current,
	}, nil
}

func (m *manager) CreateNetwork(ctx context.Context, name string) error {
	_, err := m.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			"devpad.managed": "true",
		},
	})
	if err != nil {
		return fmt.Errorf("creating network: %w", err)
	}
	return nil
}

func (m *manager) RemoveNetwork(ctx context.Context, name string) error {
	if err := m.cli.NetworkRemove(ctx, name); err != nil {
		return fmt.Errorf("removing network: %w", err)
	}
	return nil
}

func (m *manager) ConnectToNetwork(ctx context.Context, networkName, containerID string) error {
	if err := m.cli.NetworkConnect(ctx, networkName, containerID, nil); err != nil {
		return fmt.Errorf("connecting container to network: %w", err)
	}
	return nil
}

func (m *manager) CreateVolume(ctx context.Context, name string) error {
	_, err := m.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name: name,
		Labels: map[string]string{
			"devpad.managed": "true",
		},
	})
	if err != nil {
		return fmt.Errorf("creating volume: %w", err)
	}
	return nil
}

func (m *manager) RemoveVolume(ctx context.Context, name string) error {
	if err := m.cli.VolumeRemove(ctx, name, true); err != nil {
		return fmt.Errorf("removing volume: %w", err)
	}
	return nil
}

func (m *manager) BuildImage(ctx context.Context, dockerfileContent []byte, agentBinary []byte, version string) error {
	// Check if image already exists with the correct version label.
	inspect, _, err := m.cli.ImageInspectWithRaw(ctx, WorkspaceImage)
	if err == nil {
		if v, ok := inspect.Config.Labels["devpad.version"]; ok && v == version && version != "dev" {
			log.Printf("workspace image %s already up to date (version %s)", WorkspaceImage, version)
			return nil
		}
	}

	log.Printf("building workspace image %s (version %s)...", WorkspaceImage, version)

	// Create a tar archive as the build context containing the Dockerfile
	// and the agent binary.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Add Dockerfile
	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Size: int64(len(dockerfileContent)),
		Mode: 0644,
	}); err != nil {
		return fmt.Errorf("writing Dockerfile tar header: %w", err)
	}
	if _, err := tw.Write(dockerfileContent); err != nil {
		return fmt.Errorf("writing Dockerfile tar content: %w", err)
	}

	// Add agent binary
	if err := tw.WriteHeader(&tar.Header{
		Name: "devpad-agent",
		Size: int64(len(agentBinary)),
		Mode: 0755,
	}); err != nil {
		return fmt.Errorf("writing agent tar header: %w", err)
	}
	if _, err := tw.Write(agentBinary); err != nil {
		return fmt.Errorf("writing agent tar content: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}

	resp, err := m.cli.ImageBuild(ctx, &buf, types.ImageBuildOptions{
		Tags:       []string{WorkspaceImage},
		Dockerfile: "Dockerfile",
		Remove:     true,
		Labels: map[string]string{
			"devpad.version": version,
		},
	})
	if err != nil {
		return fmt.Errorf("building workspace image: %w", err)
	}
	defer resp.Body.Close()

	// Consume the build output to completion and check for errors.
	decoder := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("reading build output: %w", err)
		}
		if msg.Error != "" {
			return fmt.Errorf("docker build error: %s", msg.Error)
		}
	}

	log.Printf("workspace image %s built successfully", WorkspaceImage)
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

func (m *manager) CreateSidecar(ctx context.Context, name, imageName, volumeName, networkName string, env []string, port int) (string, error) {
	if err := m.pullImageIfNeeded(ctx, imageName); err != nil {
		return "", fmt.Errorf("pulling sidecar image: %w", err)
	}

	var mounts []mount.Mount
	if volumeName != "" {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: volumeName,
			Target: "/data",
		})
	}

	var networkingConfig *network.NetworkingConfig
	if networkName != "" {
		networkingConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				networkName: {},
			},
		}
	}

	resp, err := m.cli.ContainerCreate(ctx,
		&container.Config{
			Image: imageName,
			Env:   env,
			Labels: map[string]string{
				"devpad.managed": "true",
				"devpad.sidecar": "true",
			},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
			Mounts:        mounts,
		},
		networkingConfig, nil, name,
	)
	if err != nil {
		return "", fmt.Errorf("creating sidecar container: %w", err)
	}

	return resp.ID, nil
}
