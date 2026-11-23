package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/loginpath"
)

// Commands and setup scripts run in a login shell, which would otherwise lose
// the image's PATH; see package loginpath.

// loginPathFile is the script that restores the image's PATH.
var loginPathFile = loginpath.File

// loginPathWaitDelay is how long a killed sudo's children may hold its stderr
// open before startup stops waiting for them.
const loginPathWaitDelay = time.Second

// installLoginPath writes the script for the agent's own PATH, which is the
// image's. /etc/profile.d belongs to root, so it falls back to sudo, which the
// workspace user has.
func installLoginPath(ctx context.Context) error {
	path := os.Getenv("PATH")
	if path == "" {
		return nil
	}
	script := loginpath.Script(path)
	if current, err := os.ReadFile(loginPathFile); err == nil && bytes.Equal(current, script) {
		return nil
	}
	if err := os.WriteFile(loginPathFile, script, 0o644); err == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "sudo", "-n", "tee", loginPathFile)
	cmd.Stdin = bytes.NewReader(script)
	cmd.WaitDelay = loginPathWaitDelay
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("writing %s: %w: %s", loginPathFile, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
