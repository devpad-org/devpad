package sshkey

import (
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	privateKey, publicKey, err := Generate("test-comment")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if !strings.Contains(privateKey, "BEGIN OPENSSH PRIVATE KEY") {
		t.Error("expected private key to contain PEM header")
	}
	if !strings.Contains(privateKey, "END OPENSSH PRIVATE KEY") {
		t.Error("expected private key to contain PEM footer")
	}

	if !strings.HasPrefix(publicKey, "ssh-ed25519 ") {
		t.Error("expected public key to start with 'ssh-ed25519 '")
	}
	if !strings.Contains(publicKey, "test-comment") {
		t.Error("expected public key to contain comment")
	}
	if !strings.HasSuffix(publicKey, "\n") {
		t.Error("expected public key to end with newline")
	}
}

func TestGenerate_UniqueKeys(t *testing.T) {
	_, pub1, err := Generate("key1")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	_, pub2, err := Generate("key2")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if pub1 == pub2 {
		t.Error("expected different keys for different calls")
	}
}
