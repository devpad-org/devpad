package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// Generate creates a new ED25519 SSH key pair. It returns the OpenSSH-formatted
// private key and the authorized_keys-formatted public key (with the given comment).
func Generate(comment string) (privateKey, publicKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generating ed25519 key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("converting to ssh public key: %w", err)
	}

	pubKeyBytes := ssh.MarshalAuthorizedKey(sshPub)
	// MarshalAuthorizedKey includes a trailing newline; trim and append comment.
	pubLine := fmt.Sprintf("%s %s\n", trimNewline(string(pubKeyBytes)), comment)

	privPEM, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", fmt.Errorf("marshaling private key: %w", err)
	}

	privKeyBytes := pem.EncodeToMemory(privPEM)

	return string(privKeyBytes), pubLine, nil
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
