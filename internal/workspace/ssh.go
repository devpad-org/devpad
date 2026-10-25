package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/encrypt"
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

	c, err := s.getAgent(ctx, ws.UserID, ws.ID)
	if err != nil {
		return fmt.Errorf("getting agent client: %w", err)
	}

	if err := WriteSSHKeys(ctx, c, user, s.cipher); err != nil {
		return err
	}
	log.Printf("workspace %d: SSH keys injected", ws.ID)
	return nil
}

// WriteSSHKeys writes the user's SSH identity, known_hosts, and ssh config
// into the container behind c. It does nothing if the user has no key. Only
// call it for containers that never run untrusted code.
func WriteSSHKeys(ctx context.Context, c *agent.Client, user *auth.User, cipher *encrypt.Cipher) error {
	if user == nil || user.SSHPrivateKey == "" {
		return nil
	}
	files, err := sshFiles(user, cipher)
	if err != nil {
		return err
	}

	type command struct{ desc, cmd string }
	commands := []command{{"creating .ssh directory", "mkdir -p ~/.ssh && chmod 700 ~/.ssh"}}
	for _, f := range files {
		commands = append(commands, command{
			"writing " + f.name,
			fmt.Sprintf("cat > ~/.ssh/%s << 'DEVPAD_SSH_EOF'\n%sDEVPAD_SSH_EOF\nchmod %s ~/.ssh/%s", f.name, f.content, f.mode, f.name),
		})
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
	return nil
}

// sshFile is a file WriteSSHKeys writes into ~/.ssh.
type sshFile struct {
	name    string
	content string
	mode    string
}

// sshFiles returns what WriteSSHKeys writes for a user with a key.
func sshFiles(user *auth.User, cipher *encrypt.Cipher) ([]sshFile, error) {
	privateKey, err := cipher.Decrypt(user.SSHPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting SSH private key: %w", err)
	}
	return []sshFile{
		{"id_ed25519", privateKey, "600"},
		{"id_ed25519.pub", user.SSHPublicKey, "644"},
		{"known_hosts", knownHosts, "644"},
		{"config", sshConfig, "644"},
	}, nil
}

// managedSSHFiles hashes the files WriteSSHKeys would write for user, so
// they can be removed only where they still hold exactly that. Without a key
// on record, only the fixed known_hosts and config can be recognised.
func managedSSHFiles(user *auth.User, cipher *encrypt.Cipher) ([]agent.ManagedSSHFile, error) {
	files := []sshFile{
		{name: "known_hosts", content: knownHosts},
		{name: "config", content: sshConfig},
	}
	if user != nil && user.SSHPrivateKey != "" {
		var err error
		if files, err = sshFiles(user, cipher); err != nil {
			return nil, err
		}
	}
	managed := make([]agent.ManagedSSHFile, 0, len(files))
	for _, f := range files {
		sum := sha256.Sum256([]byte(f.content))
		managed = append(managed, agent.ManagedSSHFile{Name: f.name, SHA256: hex.EncodeToString(sum[:])})
	}
	return managed, nil
}
