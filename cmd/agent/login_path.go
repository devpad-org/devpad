package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Commands and setup scripts run in a login shell, whose /etc/profile replaces
// PATH with Debian's default, so a project environment's ENV PATH would reach
// terminals but not them. /etc/profile runs the scripts in /etc/profile.d after
// that and before ~/.profile, so one there puts the image's PATH back and
// whatever ~/.profile adds still goes in front of it.

// loginPathFile is the script that restores the image's PATH.
var loginPathFile = "/etc/profile.d/00-devpad-path.sh"

// loginPathScript returns a script setting PATH to path.
func loginPathScript(path string) []byte {
	quoted := "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	return []byte("# Written by devpad's agent: the PATH of this workspace's image.\nPATH=" + quoted + "\nexport PATH\n")
}

// installLoginPath writes the script for the agent's own PATH, which is the
// image's. /etc/profile.d belongs to root, so it falls back to sudo, which the
// workspace user has.
func installLoginPath(ctx context.Context) error {
	path := os.Getenv("PATH")
	if path == "" {
		return nil
	}
	script := loginPathScript(path)
	if current, err := os.ReadFile(loginPathFile); err == nil && bytes.Equal(current, script) {
		return nil
	}
	if err := os.WriteFile(loginPathFile, script, 0o644); err == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "sudo", "-n", "tee", loginPathFile)
	cmd.Stdin = bytes.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("writing %s: %w: %s", loginPathFile, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
