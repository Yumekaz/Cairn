package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitHostKeyCreatesPrivateKeyAndSupportsSecrets(t *testing.T) {
	dataDir := t.TempDir()

	if err := InitHostKey(dataDir); err != nil {
		t.Fatalf("InitHostKey failed: %v", err)
	}

	keyPath := filepath.Join(dataDir, "host.key")
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat host key: %v", err)
	}
	if keyInfo.Mode().Perm() != 0600 {
		t.Fatalf("host key permissions = %o, want 600", keyInfo.Mode().Perm())
	}
	if got, err := os.ReadFile(keyPath); err != nil || len(got) != 32 {
		t.Fatalf("host key length/read = %d/%v, want 32/nil", len(got), err)
	}

	ciphertext, err := EncryptSecret("private-value")
	if err != nil {
		t.Fatalf("EncryptSecret failed: %v", err)
	}
	plaintext, err := DecryptSecret(ciphertext)
	if err != nil {
		t.Fatalf("DecryptSecret failed: %v", err)
	}
	if plaintext != "private-value" {
		t.Fatalf("decrypted plaintext = %q, want private-value", plaintext)
	}
}

func TestInitHostKeyFailsClosedWithoutFallback(t *testing.T) {
	dataPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dataPath, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := InitHostKey(dataPath); err == nil {
		t.Fatal("InitHostKey unexpectedly succeeded with an unusable key directory")
	}
	if _, err := EncryptSecret("must not use fallback"); err == nil {
		t.Fatal("EncryptSecret succeeded without an initialized host key")
	}
}

func TestInitHostKeyRejectsMalformedExistingKey(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "host.key"), []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}

	err := InitHostKey(dataDir)
	if err == nil {
		t.Fatal("InitHostKey unexpectedly accepted a malformed existing key")
	}
	if !strings.Contains(err.Error(), "invalid length") {
		t.Fatalf("unexpected malformed-key error: %v", err)
	}
}
