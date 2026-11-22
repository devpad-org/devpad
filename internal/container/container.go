package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
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

// ImageInfo is a local image and the tags it goes by.
type ImageInfo struct {
	ID   string
	Tags []string
}

// BuildOptions bounds an image build. Its RUN steps run as containers on
// NetworkMode, within Memory and NanoCPUs.
type BuildOptions struct {
	NetworkMode string
	Memory      int64
	NanoCPUs    int64
	Labels      map[string]string
}

// CreateOptions describes a workspace container.
type CreateOptions struct {
	// Name is the container's name without the "devpad-ws-" prefix.
	Name string
	// Image is the image to run; empty means WorkspaceImage.
	Image string
	// Volume, when set, is mounted at /workspace.
	Volume string
	// Network, when set, is the network the container joins.
	Network     string
	Env         []string
	MemoryLimit int64
	NanoCPUs    int64
}

// Manager handles Docker container lifecycle operations.
type Manager interface {
	// Create makes a workspace container.
	Create(ctx context.Context, opts CreateOptions) (containerID string, err error)
	// Rename gives a container a new name.
	Rename(ctx context.Context, containerID, name string) error
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
	EnsureSelfAttached(ctx context.Context, networkName string) error
	DetachSelf(ctx context.Context, networkName string) error
	CreateVolume(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
	Exec(ctx context.Context, containerID string, cmd []string) (execID string, err error)
	ExecAttach(ctx context.Context, execID string) (HijackedResponse, error)
	ExecResize(ctx context.Context, execID string, height, width uint) error
	CopyFileToContainer(ctx context.Context, containerID, destPath string, fileContent []byte, mode int64) error
	BuildImage(ctx context.Context, dockerfileContent []byte, agentBinary []byte, version string) error
	// BuildProjectImage builds tag from a Dockerfile with no other build
	// context, and returns the end of the build's output.
	BuildProjectImage(ctx context.Context, tag string, dockerfile []byte, opts BuildOptions) (output string, err error)
	// ImageID returns the ID of a local image.
	ImageID(ctx context.Context, ref string) (string, error)
	// ListImages lists the local images carrying a label, given as key=value.
	ListImages(ctx context.Context, label string) ([]ImageInfo, error)
	// RemoveImage removes a tag, and the image once nothing else names it.
	// Docker refuses while a container uses the image.
	RemoveImage(ctx context.Context, ref string) error
	// PullImage pulls an image unless it is already local.
	PullImage(ctx context.Context, ref string) error
	CreateSidecar(ctx context.Context, spec SidecarSpec) (containerID string, err error)
	// RemoveSidecar removes a sidecar container and the anonymous volumes
	// its image declared. A sidecar's data is in its named volume, so an
	// anonymous one holds nothing worth keeping and would otherwise leak.
	// One already gone is removed.
	RemoveSidecar(ctx context.Context, containerID string) error
	// SidecarHealth reports whether a sidecar is running and what its
	// health check says.
	SidecarHealth(ctx context.Context, containerID string) (SidecarHealth, error)
	// DaemonArch returns the Docker daemon's CPU architecture in Go's
	// naming, such as amd64 or arm64.
	DaemonArch(ctx context.Context) (string, error)
}

// SidecarHealth is a sidecar's state as Docker reports it.
type SidecarHealth struct {
	Running bool
	// Status is Docker's health status: "starting", "healthy" or
	// "unhealthy", or "none" for a container without a health check.
	Status string
	// RestartCount is how many times Docker has restarted the container
	// after it exited, and ExitCode how its last run ended.
	RestartCount int
	ExitCode     int
}

// SidecarSpec describes a sidecar service container.
type SidecarSpec struct {
	Name    string
	Image   string
	Network string
	Env     []string
	// Volume, when set, is mounted at DataDir: the directory the image keeps
	// its data in, so that the data lives in the volume and not in an
	// anonymous one the image declares.
	Volume  string
	DataDir string
	// Healthcheck, when set, is the test Docker runs in the container to
	// tell when the service accepts connections, in Docker's form:
	// {"CMD", args...} or {"CMD-SHELL", command}.
	Healthcheck []string
	// NetworkAlias, when set, is another name the sidecar goes by on
	// Network.
	NetworkAlias string
	// Labels are added to devpad's own, such as a pipeline job's.
	Labels map[string]string
	// NoRestart leaves a sidecar that exits stopped, as a job's services
	// are: Docker otherwise restarts it unless it was stopped.
	NoRestart bool
	// Limits, when set, bound the sidecar, as JobServiceLimits do a job's
	// services.
	Limits Limits
}

// SidecarHealthStartPeriod is how long a sidecar has to pass its first
// health check. Checks run every second meanwhile, where failures do not
// count, then rarely: they are for knowing when a new service is ready,
// not for watching it. Whoever waits for a new sidecar to be ready should
// wait this long, since a check passing later is seen only a minute on.
const SidecarHealthStartPeriod = 4 * time.Minute

const (
	sidecarHealthStartInterval = time.Second
	sidecarHealthInterval      = time.Minute
	sidecarHealthTimeout       = 10 * time.Second
	sidecarHealthRetries       = 3
)

// HijackedResponse wraps the Docker hijacked connection for exec.
type HijackedResponse struct {
	Conn   io.WriteCloser
	Reader io.Reader
	Closer func()
}

type manager struct {
	cli *client.Client

	// selfID identifies the container devpad itself runs in, empty when it runs
	// on the host. See self.go; resolved once, guarded by selfMu.
	selfMu       sync.Mutex
	selfID       string
	selfResolved bool

	// attached caches the workspace networks devpad has already joined.
	attachMu sync.Mutex
	attached map[string]bool
}

// NewManager creates a new container Manager using the default Docker
// client, which runs pipeline jobs too.
func NewManager() (Docker, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &manager{cli: cli, attached: make(map[string]bool)}, nil
}

func (m *manager) Create(ctx context.Context, opts CreateOptions) (string, error) {
	containerName := fmt.Sprintf("devpad-ws-%s", opts.Name)
	image := opts.Image
	if image == "" {
		image = WorkspaceImage
	}

	var mounts []mount.Mount
	if opts.Volume != "" {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: opts.Volume,
			Target: "/workspace",
		})
	}

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		Mounts:        mounts,
		Resources: container.Resources{
			Memory:   opts.MemoryLimit,
			NanoCPUs: opts.NanoCPUs,
		},
	}

	var networkingConfig *network.NetworkingConfig
	if opts.Network != "" {
		networkingConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				opts.Network: {},
			},
		}
	}

	resp, err := m.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: image,
			Tty:   true,
			Env:   opts.Env,
			Labels: map[string]string{
				"devpad.managed": "true",
			},
		},
		HostConfig:       hostConfig,
		NetworkingConfig: networkingConfig,
		Name:             containerName,
	})
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}

	return resp.ID, nil
}

func (m *manager) Rename(ctx context.Context, containerID, name string) error {
	if _, err := m.cli.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: name}); err != nil {
		return fmt.Errorf("renaming container: %w", err)
	}
	return nil
}

func (m *manager) Start(ctx context.Context, containerID string) error {
	if _, err := m.cli.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}
	return nil
}

func (m *manager) Stop(ctx context.Context, containerID string) error {
	if _, err := m.cli.ContainerStop(ctx, containerID, client.ContainerStopOptions{}); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}
	return nil
}

func (m *manager) Restart(ctx context.Context, containerID string) error {
	if _, err := m.cli.ContainerRestart(ctx, containerID, client.ContainerRestartOptions{}); err != nil {
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

	if _, err := m.cli.CopyToContainer(ctx, containerID, client.CopyToContainerOptions{
		DestinationPath: "/",
		Content:         &buf,
	}); err != nil {
		return fmt.Errorf("copying file to container: %w", err)
	}
	return nil
}

func (m *manager) Remove(ctx context.Context, containerID string) error {
	if _, err := m.cli.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("removing container: %w", err)
	}
	return nil
}

func (m *manager) RemoveSidecar(ctx context.Context, containerID string) error {
	if _, err := m.cli.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !isNotFound(err) {
		return fmt.Errorf("removing sidecar container: %w", err)
	}
	return nil
}

func (m *manager) UpdateResources(ctx context.Context, containerID string, memoryLimit, nanoCPUs int64) error {
	_, err := m.cli.ContainerUpdate(ctx, containerID, client.ContainerUpdateOptions{
		Resources: &container.Resources{
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
	resp, err := m.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          true,
	})
	if err != nil {
		return "", fmt.Errorf("creating exec: %w", err)
	}
	return resp.ID, nil
}

func (m *manager) ExecAttach(ctx context.Context, execID string) (HijackedResponse, error) {
	resp, err := m.cli.ExecAttach(ctx, execID, client.ExecAttachOptions{
		TTY: true,
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
	if _, err := m.cli.ExecResize(ctx, execID, client.ExecResizeOptions{
		Height: height,
		Width:  width,
	}); err != nil {
		return fmt.Errorf("resizing exec: %w", err)
	}
	return nil
}

func (m *manager) GetIP(ctx context.Context, containerID, networkName string) (string, error) {
	res, err := m.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspecting container: %w", err)
	}
	info := res.Container

	// If a custom network is specified, look up the IP on that network, falling
	// back to the default bridge IP for backward compatibility.
	for _, name := range []string{networkName, "bridge"} {
		if name == "" {
			continue
		}
		if ep, ok := info.NetworkSettings.Networks[name]; ok && ep != nil && ep.IPAddress.IsValid() {
			return ep.IPAddress.String(), nil
		}
	}
	return "", fmt.Errorf("container has no IP address")
}

func (m *manager) GetEnv(ctx context.Context, containerID string) ([]string, error) {
	res, err := m.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}
	return res.Container.Config.Env, nil
}

func (m *manager) Stats(ctx context.Context, containerID string) (*ContainerStats, error) {
	// The previous sample fills precpu_stats, which the CPU percentage needs.
	resp, err := m.cli.ContainerStats(ctx, containerID, client.ContainerStatsOptions{IncludePreviousSample: true})
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
	_, err := m.cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{
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
	if _, err := m.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil {
		return fmt.Errorf("removing network: %w", err)
	}
	return nil
}

func (m *manager) ConnectToNetwork(ctx context.Context, networkName, containerID string) error {
	if _, err := m.cli.NetworkConnect(ctx, networkName, client.NetworkConnectOptions{Container: containerID}); err != nil {
		return fmt.Errorf("connecting container to network: %w", err)
	}
	return nil
}

func (m *manager) CreateVolume(ctx context.Context, name string) error {
	_, err := m.cli.VolumeCreate(ctx, client.VolumeCreateOptions{
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
	if _, err := m.cli.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("removing volume: %w", err)
	}
	return nil
}

func (m *manager) BuildImage(ctx context.Context, dockerfileContent []byte, agentBinary []byte, version string) error {
	// Check if image already exists with the correct version label.
	inspect, err := m.cli.ImageInspect(ctx, WorkspaceImage)
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

	resp, err := m.cli.ImageBuild(ctx, &buf, client.ImageBuildOptions{
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

func (m *manager) PullImage(ctx context.Context, ref string) error {
	return m.pullImageIfNeeded(ctx, ref)
}

func (m *manager) pullImageIfNeeded(ctx context.Context, imageName string) error {
	_, err := m.cli.ImageInspect(ctx, imageName)
	if err == nil {
		return nil // image already exists
	}

	log.Printf("pulling image %s...", imageName)
	reader, err := m.cli.ImagePull(ctx, imageName, client.ImagePullOptions{})
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

func (m *manager) CreateSidecar(ctx context.Context, spec SidecarSpec) (string, error) {
	if spec.NetworkAlias != "" && spec.Network == "" {
		return "", fmt.Errorf("sidecar %s has a network alias but no network", spec.Name)
	}
	if err := m.pullImageIfNeeded(ctx, spec.Image); err != nil {
		return "", fmt.Errorf("pulling sidecar image: %w", err)
	}

	var mounts []mount.Mount
	if spec.Volume != "" {
		if spec.DataDir == "" {
			return "", fmt.Errorf("sidecar %s has a volume but no data directory", spec.Name)
		}
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: spec.Volume,
			Target: spec.DataDir,
		})
	}

	var networkingConfig *network.NetworkingConfig
	if spec.Network != "" {
		endpoint := &network.EndpointSettings{}
		if spec.NetworkAlias != "" {
			endpoint.Aliases = []string{spec.NetworkAlias}
		}
		networkingConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				spec.Network: endpoint,
			},
		}
	}

	labels := map[string]string{}
	maps.Copy(labels, spec.Labels)
	labels[labelManaged] = "true"
	labels[labelSidecar] = "true"
	restart := container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	if spec.NoRestart {
		restart = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	}

	var healthcheck *container.HealthConfig
	if len(spec.Healthcheck) > 0 {
		healthcheck = &container.HealthConfig{
			Test:          spec.Healthcheck,
			Interval:      sidecarHealthInterval,
			Timeout:       sidecarHealthTimeout,
			StartPeriod:   SidecarHealthStartPeriod,
			StartInterval: sidecarHealthStartInterval,
			Retries:       sidecarHealthRetries,
		}
	}

	resp, err := m.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:       spec.Image,
			Env:         spec.Env,
			Healthcheck: healthcheck,
			Labels:      labels,
		},
		HostConfig: &container.HostConfig{
			RestartPolicy: restart,
			Mounts:        mounts,
			Resources:     spec.Limits.resources(),
		},
		NetworkingConfig: networkingConfig,
		Name:             spec.Name,
	})
	if err != nil {
		return "", fmt.Errorf("creating sidecar container: %w", err)
	}

	return resp.ID, nil
}

func (m *manager) SidecarHealth(ctx context.Context, containerID string) (SidecarHealth, error) {
	res, err := m.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return SidecarHealth{}, fmt.Errorf("inspecting sidecar: %w", err)
	}
	h := SidecarHealth{Status: string(container.NoHealthcheck)}
	h.RestartCount = res.Container.RestartCount
	if st := res.Container.State; st != nil {
		h.Running = st.Running
		h.ExitCode = st.ExitCode
		if st.Health != nil {
			h.Status = string(st.Health.Status)
		}
	}
	return h, nil
}

func (m *manager) DaemonArch(ctx context.Context) (string, error) {
	v, err := m.cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", fmt.Errorf("reading docker version: %w", err)
	}
	return v.Arch, nil
}
