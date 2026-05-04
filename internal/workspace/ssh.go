package workspace

import (
	"context"
	"fmt"
	"log"
)

// knownHosts contains SSH host keys for common Git hosting providers.
const knownHosts = `github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
gitlab.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAfuCHKVTjquxvt6CM6tdG4SLp1Btn/nOeHHE5UOzRdf
bitbucket.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIazEu89wgQZ4bqs3d63QSMzYVa0MuJ2e2gKTKqu+UUO
`

// sshConfig is placed at ~/.ssh/config to auto-accept new host keys
// for known Git hosting providers and use the Devpad-managed identity file.
const sshConfig = `Host github.com gitlab.com bitbucket.org
    IdentityFile ~/.ssh/id_ed25519
    IdentitiesOnly yes
    StrictHostKeyChecking accept-new
`

// injectSSHKeys writes the user's SSH private key, known_hosts, and ssh config
// into the workspace container via the agent's file write API. This is
// best-effort: a failure should not prevent the workspace from starting.
func (s *service) injectSSHKeys(ctx context.Context, ws *Workspace) error {
	user, err := s.users.GetByID(ctx, ws.UserID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil || user.SSHPrivateKey == "" {
		return nil
	}

	privateKey, err := s.cipher.Decrypt(user.SSHPrivateKey)
	if err != nil {
		return fmt.Errorf("decrypting SSH private key: %w", err)
	}

	c, err := s.getAgent(ctx, ws.UserID, ws.ID)
	if err != nil {
		return fmt.Errorf("getting agent client: %w", err)
	}

	commands := []struct {
		desc string
		cmd  string
	}{
		{"creating .ssh directory", "mkdir -p ~/.ssh && chmod 700 ~/.ssh"},
		{"writing private key", fmt.Sprintf("cat > ~/.ssh/id_ed25519 << 'DEVPAD_SSH_EOF'\n%sDEVPAD_SSH_EOF\nchmod 600 ~/.ssh/id_ed25519", privateKey)},
		{"writing public key", fmt.Sprintf("cat > ~/.ssh/id_ed25519.pub << 'DEVPAD_SSH_EOF'\n%sDEVPAD_SSH_EOF\nchmod 644 ~/.ssh/id_ed25519.pub", user.SSHPublicKey)},
		{"writing known_hosts", fmt.Sprintf("cat > ~/.ssh/known_hosts << 'DEVPAD_SSH_EOF'\n%sDEVPAD_SSH_EOF\nchmod 644 ~/.ssh/known_hosts", knownHosts)},
		{"writing ssh config", fmt.Sprintf("cat > ~/.ssh/config << 'DEVPAD_SSH_EOF'\n%sDEVPAD_SSH_EOF\nchmod 644 ~/.ssh/config", sshConfig)},
	}

	for _, cmd := range commands {
		result, err := c.RunCommand(ctx, cmd.cmd)
		if err != nil {
			return fmt.Errorf("%s: %w", cmd.desc, err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("%s: exit code %d: %s", cmd.desc, result.ExitCode, result.Error)
		}
	}

	log.Printf("workspace %d: SSH keys injected", ws.ID)
	return nil
}
