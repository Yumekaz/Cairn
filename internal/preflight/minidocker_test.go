package preflight

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeMiniDockerMissingSocket(t *testing.T) {
	ctx := context.Background()
	res := ProbeMiniDocker(ctx, filepath.Join(t.TempDir(), "no-such.sock"))
	if res.OK {
		t.Fatal("expected probe to fail for missing socket")
	}
	if res.Message == "" {
		t.Fatal("expected error message")
	}
	if res.Hint == "" {
		t.Fatal("expected operator hint")
	}
}

func TestProbeMiniDockerHealthySocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "mini-docker.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_ = os.Chmod(sock, 0o666)

	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	// Give listener a moment
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res := ProbeMiniDocker(ctx, sock)
	if !res.OK {
		t.Fatalf("expected healthy probe, got: %s (%s)", res.Message, res.Hint)
	}
	if res.SocketPath != sock {
		t.Fatalf("socket path: %s", res.SocketPath)
	}
}

func TestDiscoverRootfsUsesEnv(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "busybox"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAIRN_ROOTFS", dir)
	got := DiscoverRootfs()
	if got != dir {
		t.Fatalf("want %s got %s", dir, got)
	}
}
