package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/loginpath"
)

func TestInstallLoginPathRestoresTheImagesPath(t *testing.T) {
	loginPathFile = filepath.Join(t.TempDir(), "00-devpad-path.sh")
	t.Cleanup(func() { loginPathFile = loginpath.File })

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	const imagePath = "/opt/it's here/bin:/usr/bin:/bin"
	t.Setenv("PATH", imagePath)

	if err := installLoginPath(context.Background()); err != nil {
		t.Fatalf("installLoginPath: %v", err)
	}

	// As in a login shell: the profile resets PATH, runs the script, and
	// ~/.profile then adds to what the script left.
	script := `PATH=/reset/by/profile; . "$1"; PATH="/home/bin:$PATH"; printf %s "$PATH"`
	cmd := exec.Command(bash, "-c", script, "bash", loginPathFile)
	cmd.Env = []string{"PATH=" + imagePath}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the script: %v: %s", err, out)
	}
	if want := "/home/bin:" + imagePath; string(out) != want {
		t.Fatalf("PATH = %q, want %q", out, want)
	}
}

func TestInstallLoginPathGivesUpOnAStuckSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write the script without sudo")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep is not installed")
	}
	// Killing this sudo leaves its child holding stderr open.
	bin := t.TempDir()
	sudo := "#!/bin/sh\n" + sleep + " 5 &\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte(sudo), 0o755); err != nil {
		t.Fatal(err)
	}
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	loginPathFile = filepath.Join(readOnly, "00-devpad-path.sh")
	t.Cleanup(func() { loginPathFile = loginpath.File })
	t.Setenv("PATH", bin)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := installLoginPath(ctx); err == nil {
		t.Fatal("installLoginPath succeeded with a stuck sudo")
	}
	if elapsed := time.Since(start); elapsed >= 4*time.Second {
		t.Fatalf("installLoginPath took %s, waiting for sudo's child", elapsed)
	}
}

func TestInstallLoginPathLeavesAnUpToDateScript(t *testing.T) {
	loginPathFile = filepath.Join(t.TempDir(), "00-devpad-path.sh")
	t.Cleanup(func() { loginPathFile = loginpath.File })
	t.Setenv("PATH", "/usr/bin:/bin")

	if err := os.WriteFile(loginPathFile, loginpath.Script("/usr/bin:/bin"), 0o444); err != nil {
		t.Fatal(err)
	}
	// Read-only, so rewriting it would fall through to sudo and fail.
	if err := installLoginPath(context.Background()); err != nil {
		t.Fatalf("installLoginPath: %v", err)
	}
	got, err := os.ReadFile(loginPathFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "PATH='/usr/bin:/bin'") {
		t.Fatalf("script = %q", got)
	}
}
