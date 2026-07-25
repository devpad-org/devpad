package container

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const testContainerID = "a42464c1265f0c3b2d1e4a5b6c7d8e9f0a1b2c3d4e5f60718293a4b5c6d7e8f9"

// TestMountinfoSelfIDs covers the distinction that matters most: inside a
// container, mountinfo names our own ID; on the Docker host the same file is
// full of *other* containers' IDs, which must never be mistaken for self.
func TestMountinfoSelfIDs(t *testing.T) {
	const otherID = "b53575d2376e1d4c3e2f5b6c7d8e9f0a1b2c3d4e5f60718293a4b5c6d7e8f9a0"

	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "inside a container",
			content: "1234 1000 8:1 /var/lib/docker/containers/" + testContainerID + "/hostname /etc/hostname rw,noatime - ext4 /dev/sda1 rw\n" +
				"1235 1000 8:1 /var/lib/docker/containers/" + testContainerID + "/resolv.conf /etc/resolv.conf rw - ext4 /dev/sda1 rw\n",
			want: []string{testContainerID},
		},
		{
			name: "on the docker host",
			content: "100 50 0:70 / /var/lib/docker/overlay2/" + otherID + "/merged rw - overlay overlay rw\n" +
				"101 50 0:71 / /var/lib/docker/containers/" + otherID + "/mounts/shm rw - tmpfs shm rw\n",
			want: nil,
		},
		{
			name:    "malformed line",
			content: "short line\n",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mountinfo")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("writing fixture: %v", err)
			}

			got := mountinfoSelfIDsFrom(path)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("id %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestScanFileForContainerIDs(t *testing.T) {
	const id = testContainerID

	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "cgroup v1",
			content: "12:pids:/docker/" + id + "\n11:cpu:/docker/" + id + "\n",
			want:    []string{id},
		},
		{
			name:    "cgroup v2 systemd scope",
			content: "0::/system.slice/docker-" + id + ".scope\n",
			want:    []string{id},
		},
		{
			name:    "host cgroup has no container id",
			content: "0::/init.scope\n11:pids:/\n",
			want:    nil,
		},
		{
			name:    "short hex is not a container id",
			content: "0::/system.slice/docker-deadbeef.scope\n",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "procfile")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("writing fixture: %v", err)
			}

			got := scanFileForContainerIDs(path)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("id %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestScanFileForContainerIDsMissingFile(t *testing.T) {
	if got := scanFileForContainerIDs(filepath.Join(t.TempDir(), "absent")); got != nil {
		t.Errorf("got %v, want nil for a missing file", got)
	}
}

func TestContainerIDCandidatesPrefersEnvOverride(t *testing.T) {
	t.Setenv(SelfContainerIDEnv, "  my-devpad  ")

	for _, containerized := range []bool{true, false} {
		candidates := containerIDCandidates(containerized)
		if len(candidates) == 0 {
			t.Fatalf("containerized=%v: expected at least the env candidate", containerized)
		}
		if candidates[0] != "my-devpad" {
			t.Errorf("containerized=%v: got %q as first candidate, want trimmed env value", containerized, candidates[0])
		}
	}
}

func TestContainerIDCandidatesIncludesHostnameInContainer(t *testing.T) {
	t.Setenv(SelfContainerIDEnv, "")

	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("hostname unavailable")
	}

	found := false
	for _, c := range containerIDCandidates(true) {
		if c == hostname {
			found = true
		}
	}
	if !found {
		t.Errorf("hostname %q missing from candidates inside a container", hostname)
	}
}

// TestContainerIDCandidatesEmptyOnHost guards the failure mode that would make
// devpad disconnect a workspace from its own network: without an explicit
// override, a host install must produce no self-ID guesses at all.
func TestContainerIDCandidatesEmptyOnHost(t *testing.T) {
	t.Setenv(SelfContainerIDEnv, "")

	if got := containerIDCandidates(false); len(got) != 0 {
		t.Errorf("got %v, want no candidates when running on the host", got)
	}
}

func TestDedupe(t *testing.T) {
	got := dedupe([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIsAlreadyConnected(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "docker endpoint conflict",
			err:  errors.New("Error response from daemon: endpoint with name devpad already exists in network devpad-net-1"),
			want: true,
		},
		{
			name: "unrelated failure",
			err:  errors.New("Error response from daemon: network devpad-net-1 not found"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAlreadyConnected(tt.err); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsNotConnected(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "not attached",
			err:  errors.New("Error response from daemon: container abc is not connected to network devpad-net-1"),
			want: true,
		},
		{
			name: "network already gone",
			err:  errors.New("Error response from daemon: No such network: devpad-net-1"),
			want: true,
		},
		{
			name: "real failure",
			err:  errors.New("Cannot connect to the Docker daemon"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNotConnected(tt.err); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSelfAttachNoopsOnHost verifies the host path: with no detectable self
// container, attach and detach must do nothing rather than fail.
func TestSelfAttachNoopsOnHost(t *testing.T) {
	m := &manager{attached: make(map[string]bool)}
	m.selfResolved = true // pretend detection ran and found nothing
	m.selfID = ""

	if err := m.EnsureSelfAttached(t.Context(), "devpad-net-1"); err != nil {
		t.Errorf("EnsureSelfAttached: got %v, want nil", err)
	}
	if err := m.DetachSelf(t.Context(), "devpad-net-1"); err != nil {
		t.Errorf("DetachSelf: got %v, want nil", err)
	}
	if len(m.attached) != 0 {
		t.Errorf("attached cache should stay empty on the host, got %v", m.attached)
	}
}

// TestSelfAttachEmptyNetworkName covers legacy workspaces that have no network.
func TestSelfAttachEmptyNetworkName(t *testing.T) {
	m := &manager{attached: make(map[string]bool)}

	if err := m.EnsureSelfAttached(t.Context(), ""); err != nil {
		t.Errorf("EnsureSelfAttached: got %v, want nil", err)
	}
	if err := m.DetachSelf(t.Context(), ""); err != nil {
		t.Errorf("DetachSelf: got %v, want nil", err)
	}
}
