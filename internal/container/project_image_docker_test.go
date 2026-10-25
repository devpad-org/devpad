package container

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestBuildProjectImageWithDocker builds against a real Docker daemon, which
// must already have devpad's workspace image. It runs only when
// DEVPAD_DOCKER_TEST is set.
func TestBuildProjectImageWithDocker(t *testing.T) {
	if os.Getenv("DEVPAD_DOCKER_TEST") == "" {
		t.Skip("set DEVPAD_DOCKER_TEST to build against the local Docker daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	m, err := NewManager()
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	if _, err := m.ImageID(ctx, WorkspaceImage); err != nil {
		t.Skipf("no workspace image to build on: %v", err)
	}

	suffix := make([]byte, 4)
	rand.Read(suffix)
	id := hex.EncodeToString(suffix)
	network := "devpad-build-test-" + id
	if err := m.CreateNetwork(ctx, network); err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { m.RemoveNetwork(context.Background(), network) })
	label := "devpad.test=" + id
	opts := BuildOptions{
		NetworkMode: network,
		Memory:      1 << 30,
		NanoCPUs:    1_000_000_000,
		Labels:      map[string]string{"devpad.test": id},
	}

	tag := "devpad-project-test:" + id
	dockerfile := "FROM " + WorkspaceImage + "\nUSER root\nRUN echo built-" + id + " > /etc/devpad-test && id -un\nUSER devpad\nWORKDIR /workspace\nENTRYPOINT [\"/usr/local/bin/devpad-agent\"]\nCMD []\n"
	output, err := m.BuildProjectImage(ctx, tag, []byte(dockerfile), opts)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	t.Cleanup(func() { m.RemoveImage(context.Background(), tag) })
	if !strings.Contains(output, "root") {
		t.Fatalf("expected the RUN step's output, got:\n%s", output)
	}
	if _, err := m.ImageID(ctx, tag); err != nil {
		t.Fatalf("built image missing: %v", err)
	}
	images, err := m.ListImages(ctx, label)
	if err != nil || len(images) != 1 || len(images[0].Tags) != 1 || images[0].Tags[0] != tag {
		t.Fatalf("expected the image found by its label: %+v %v", images, err)
	}

	// A container made from it runs devpad's agent.
	containerID, err := m.Create(ctx, "test-"+id, tag, "", network, []string{"AGENT_AUTH_TOKEN=test"}, 512<<20, 500_000_000)
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	t.Cleanup(func() { m.Remove(context.Background(), containerID) })
	if err := m.Start(ctx, containerID); err != nil {
		t.Fatalf("start container: %v", err)
	}
	ip, err := m.GetIP(ctx, containerID, network)
	if err != nil {
		t.Fatalf("container IP: %v", err)
	}
	healthy := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline) && !healthy; time.Sleep(200 * time.Millisecond) {
		resp, err := http.Get("http://" + ip + ":9100/healthz")
		if err == nil {
			healthy = resp.StatusCode == http.StatusOK
			resp.Body.Close()
		}
	}
	if !healthy {
		t.Fatalf("the agent never answered in a container made from %s", tag)
	}
	if err := m.Remove(ctx, containerID); err != nil {
		t.Fatalf("remove container: %v", err)
	}

	failing := "FROM " + WorkspaceImage + "\nRUN echo about-to-fail && false\n"
	output, err = m.BuildProjectImage(ctx, "devpad-project-test:fail-"+id, []byte(failing), opts)
	if err == nil {
		t.Fatal("expected a failing RUN step to fail the build")
	}
	if !strings.Contains(output, "about-to-fail") || !strings.Contains(err.Error(), "non-zero") {
		t.Fatalf("expected the step's output and why it failed: %v\n%s", err, output)
	}

	if err := m.RemoveImage(ctx, tag); err != nil {
		t.Fatalf("remove image: %v", err)
	}
	if images, _ := m.ListImages(ctx, label); len(images) != 0 {
		t.Fatalf("expected the image gone: %+v", images)
	}
}
