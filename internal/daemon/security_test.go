package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCairnSocketDirectoryAndSocketArePrivate(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "cairn")
	socketPath := filepath.Join(dataDir, "cairnd.sock")

	if err := ensureSocketDirectory(socketPath, dataDir); err != nil {
		t.Fatalf("ensureSocketDirectory failed: %v", err)
	}
	dirInfo, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("stat socket directory: %v", err)
	}
	if dirInfo.Mode().Perm() != 0700 {
		t.Fatalf("socket directory permissions = %o, want 700", dirInfo.Mode().Perm())
	}

	// The restricted test environment cannot create AF_UNIX listeners, but the
	// production hardening operation is an ordinary chmod on the bound path.
	// Use a socket-path fixture to exercise that operation and its mode check.
	if err := os.WriteFile(socketPath, []byte("socket fixture"), 0666); err != nil {
		t.Fatalf("create socket-path fixture: %v", err)
	}
	defer os.Remove(socketPath)

	if err := secureUnixSocket(socketPath); err != nil {
		t.Fatalf("secureUnixSocket failed: %v", err)
	}
	socketInfo, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if socketInfo.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions = %o, want 600", socketInfo.Mode().Perm())
	}
}

func TestCustomExistingSocketDirectoryIsNotRepermissioned(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	socketDir := filepath.Join(root, "operator-managed")
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := ensureSocketDirectory(filepath.Join(socketDir, "cairnd.sock"), dataDir); err != nil {
		t.Fatalf("ensureSocketDirectory failed: %v", err)
	}
	info, err := os.Stat(socketDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("custom socket directory permissions = %o, want unchanged 755", info.Mode().Perm())
	}
}
