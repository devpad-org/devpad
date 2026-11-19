package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallLoginPathRestoresTheImagesPath(t *testing.T) {
	loginPathFile = filepath.Join(t.TempDir(), "00-devpad-path.sh")
	t.Cleanup(func() { loginPathFile = "/etc/profile.d/00-devpad-path.sh" })

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

func TestInstallLoginPathLeavesAnUpToDateScript(t *testing.T) {
	loginPathFile = filepath.Join(t.TempDir(), "00-devpad-path.sh")
	t.Cleanup(func() { loginPathFile = "/etc/profile.d/00-devpad-path.sh" })
	t.Setenv("PATH", "/usr/bin:/bin")

	if err := os.WriteFile(loginPathFile, loginPathScript("/usr/bin:/bin"), 0o444); err != nil {
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
