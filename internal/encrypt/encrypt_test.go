package encrypt

import (
	"testing"
)

func TestCipher_RoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	c, err := NewCipher(key)
	if err != nil {
		t.Fatalf("creating cipher: %v", err)
	}

	plaintext := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk...\n-----END OPENSSH PRIVATE KEY-----\n"

	encrypted, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypting: %v", err)
	}

	if encrypted == plaintext {
		t.Error("encrypted should differ from plaintext")
	}

	decrypted, err := c.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("decrypting: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestCipher_EmptyString(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewCipher(key)

	encrypted, err := c.Encrypt("")
	if err != nil {
		t.Fatalf("encrypting empty: %v", err)
	}
	if encrypted != "" {
		t.Errorf("expected empty string, got %q", encrypted)
	}

	decrypted, err := c.Decrypt("")
	if err != nil {
		t.Fatalf("decrypting empty: %v", err)
	}
	if decrypted != "" {
		t.Errorf("expected empty string, got %q", decrypted)
	}
}

func TestCipher_DifferentCiphertexts(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewCipher(key)

	e1, _ := c.Encrypt("same text")
	e2, _ := c.Encrypt("same text")

	if e1 == e2 {
		t.Error("same plaintext should produce different ciphertexts (random nonce)")
	}
}

func TestCipher_WrongKey(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()

	c1, _ := NewCipher(key1)
	c2, _ := NewCipher(key2)

	encrypted, _ := c1.Encrypt("secret data")

	_, err := c2.Decrypt(encrypted)
	if err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}

func TestNewCipher_InvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"too short", "abcd"},
		{"not hex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
		{"wrong length", "aabbccdd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCipher(tt.key)
			if err == nil {
				t.Error("expected error for invalid key")
			}
		})
	}
}
