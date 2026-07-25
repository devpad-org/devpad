package container

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"
)

// SelfContainerIDEnv pins the ID of the container devpad itself runs in,
// bypassing autodetection. Only needed when devpad runs inside a container
// whose ID cannot be discovered from /proc or the hostname.
const SelfContainerIDEnv = "DEVPAD_CONTAINER_ID"

// selfDetectTimeout bounds the Docker calls used to identify devpad's own
// container. Detection result is cached for the process lifetime.
const selfDetectTimeout = 5 * time.Second

// containerIDPattern matches the 64-character hex container IDs that appear in
// /proc/self/mountinfo and /proc/self/cgroup inside a container.
var containerIDPattern = regexp.MustCompile(`[0-9a-f]{64}`)

// EnsureSelfAttached connects the devpad container to a workspace network so
// it can reach the workspace agent by container IP.
//
// Docker isolates bridge networks from one another, so a devpad running in a
// container on its own network cannot route to a workspace on devpad-net-N —
// packets are dropped and agent requests fail with an i/o timeout. Joining the
// network fixes that. When devpad runs directly on the host it already owns the
// bridge interface and this is a no-op.
//
// Successful attachments are cached, so repeated calls cost nothing.
func (m *manager) EnsureSelfAttached(ctx context.Context, networkName string) error {
	if networkName == "" {
		return nil
	}
	selfID := m.selfContainerID()
	if selfID == "" {
		return nil
	}

	m.attachMu.Lock()
	defer m.attachMu.Unlock()
	if m.attached[networkName] {
		return nil
	}

	// A previous devpad run may have already joined this network.
	info, err := m.cli.ContainerInspect(ctx, selfID)
	if err != nil {
		return fmt.Errorf("inspecting devpad container: %w", err)
	}
	if _, ok := info.NetworkSettings.Networks[networkName]; ok {
		m.attached[networkName] = true
		return nil
	}

	if err := m.cli.NetworkConnect(ctx, networkName, selfID, nil); err != nil {
		if !isAlreadyConnected(err) {
			return fmt.Errorf("connecting devpad to network %s: %w", networkName, err)
		}
	}
	m.attached[networkName] = true
	log.Printf("container: devpad joined workspace network %s", networkName)
	return nil
}

// DetachSelf disconnects the devpad container from a workspace network. It must
// be called before removing the network, which Docker refuses while endpoints
// remain attached. It is a no-op when devpad runs directly on the host.
func (m *manager) DetachSelf(ctx context.Context, networkName string) error {
	if networkName == "" {
		return nil
	}
	selfID := m.selfContainerID()
	if selfID == "" {
		return nil
	}

	m.attachMu.Lock()
	defer m.attachMu.Unlock()
	delete(m.attached, networkName)

	if err := m.cli.NetworkDisconnect(ctx, networkName, selfID, true); err != nil {
		if isNotConnected(err) {
			return nil
		}
		return fmt.Errorf("disconnecting devpad from network %s: %w", networkName, err)
	}
	return nil
}

// selfContainerID returns the ID of the container devpad runs in, or "" when it
// runs directly on the host. The result is detected once and cached.
func (m *manager) selfContainerID() string {
	m.selfMu.Lock()
	defer m.selfMu.Unlock()
	if !m.selfResolved {
		m.selfID = m.detectSelfContainerID()
		m.selfResolved = true
	}
	return m.selfID
}

// detectSelfContainerID tries each candidate ID against the Docker daemon and
// returns the first one that resolves to a real container. Detection uses its
// own timeout rather than a caller's context so that a cancelled request cannot
// poison the cached process-wide result.
func (m *manager) detectSelfContainerID() string {
	ctx, cancel := context.WithTimeout(context.Background(), selfDetectTimeout)
	defer cancel()

	for _, candidate := range selfIDCandidates() {
		info, err := m.cli.ContainerInspect(ctx, candidate)
		if err != nil {
			continue
		}
		// A devpad-managed container is a workspace or sidecar, never devpad
		// itself. Mistaking one for self would make devpad disconnect a
		// workspace from its own network, so reject it outright.
		if info.Config != nil && info.Config.Labels["devpad.managed"] == "true" {
			log.Printf("container: ignoring self-ID candidate %s — it is a devpad-managed workspace container", candidate)
			continue
		}
		log.Printf("container: devpad is running in container %s; workspace networks will be joined on demand", info.ID[:12])
		return info.ID
	}

	if runningInContainer() {
		log.Printf("container: devpad appears to be running inside a container, but its own container ID could not be determined — "+
			"workspace agents will be unreachable. Set %s to the devpad container's ID or name.", SelfContainerIDEnv)
	}
	return ""
}

// selfIDCandidates lists possible identifiers for devpad's own container, most
// reliable first. Everything except an explicit override is gated on actually
// being containerized: on the host, /proc/self/mountinfo is full of other
// containers' IDs (overlay and shm mounts) and the hostname may collide with a
// container name, so guessing there would identify the wrong container.
func selfIDCandidates() []string {
	return containerIDCandidates(runningInContainer())
}

// containerIDCandidates builds the candidate list for a known containerization
// state. Split out from selfIDCandidates so tests can exercise both states.
func containerIDCandidates(containerized bool) []string {
	var candidates []string
	if id := strings.TrimSpace(os.Getenv(SelfContainerIDEnv)); id != "" {
		candidates = append(candidates, id)
	}
	if !containerized {
		return candidates
	}

	candidates = append(candidates, mountinfoSelfIDs()...)
	candidates = append(candidates, scanFileForContainerIDs("/proc/self/cgroup")...)
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		// Docker defaults a container's hostname to its short ID.
		candidates = append(candidates, hostname)
	}
	return dedupe(candidates)
}

// selfMountPoints are the paths Docker bind-mounts from
// /var/lib/docker/containers/<id>/ into a container. Their presence in
// /proc/self/mountinfo identifies *our own* container ID; matching IDs anywhere
// else in mountinfo would pick up unrelated containers when devpad runs on the
// Docker host.
var selfMountPoints = map[string]bool{
	"/etc/hostname":    true,
	"/etc/hosts":       true,
	"/etc/resolv.conf": true,
}

// mountinfoSelfIDs extracts container IDs from the mountinfo entries that
// describe the current container's own injected files. It works under both
// cgroup v1 and v2, unlike /proc/self/cgroup.
func mountinfoSelfIDs() []string {
	return mountinfoSelfIDsFrom("/proc/self/mountinfo")
}

func mountinfoSelfIDsFrom(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var ids []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Fields: mountID parentID major:minor root mountPoint ...
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 || !selfMountPoints[fields[4]] {
			continue
		}
		ids = append(ids, containerIDPattern.FindAllString(fields[3], -1)...)
	}
	return dedupe(ids)
}

// scanFileForContainerIDs extracts 64-hex container IDs from a /proc file.
func scanFileForContainerIDs(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var ids []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		ids = append(ids, containerIDPattern.FindAllString(scanner.Text(), -1)...)
	}
	return dedupe(ids)
}

func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := values[:0]
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// runningInContainer reports whether devpad itself is containerized. It gates
// self-ID guessing, so it must not fire on a plain Docker host: the markers
// below only exist inside a container.
func runningInContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return len(mountinfoSelfIDs()) > 0
}

// isAlreadyConnected reports whether a NetworkConnect error means the endpoint
// was already there — the benign outcome when two devpad instances (or a
// restarted one) race to join the same network.
func isAlreadyConnected(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "already exists in network") ||
		strings.Contains(msg, "is already attached to network")
}

// isNotConnected reports whether a NetworkDisconnect error means there was
// nothing to disconnect, which is success as far as callers are concerned.
func isNotConnected(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "is not connected to network") ||
		strings.Contains(msg, "No such network") ||
		strings.Contains(msg, "No such container")
}
