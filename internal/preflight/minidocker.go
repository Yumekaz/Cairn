// Package preflight probes local runtime dependencies before deploy.
package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// MiniDockerResult is the outcome of a Mini-Docker socket probe.
type MiniDockerResult struct {
	OK         bool
	SocketPath string
	Message    string
	Hint       string
}

// DefaultMiniDockerSockets returns likely socket paths for this user.
func DefaultMiniDockerSockets() []string {
	var paths []string
	if v := os.Getenv("MINI_DOCKER_SOCKET"); v != "" {
		paths = append(paths, v)
	}
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "mini-docker", "mini-docker.sock"))
	}
	// Build /run/user/<uid>/... from the current process uid — never assume 1000.
	uidSock := filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "mini-docker", "mini-docker.sock")
	paths = append(paths,
		uidSock,
		"/var/run/mini-docker/mini-docker.sock",
		"/var/run/mini-docker.sock",
	)
	// de-dupe
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// ProbeMiniDocker checks that a Unix socket exists and answers GET /containers/json.
// If socketPath is empty, tries DefaultMiniDockerSockets in order.
func ProbeMiniDocker(ctx context.Context, socketPath string) MiniDockerResult {
	candidates := []string{}
	if socketPath != "" {
		candidates = append(candidates, socketPath)
	} else {
		candidates = DefaultMiniDockerSockets()
	}

	var last MiniDockerResult
	for _, sock := range candidates {
		last = probeOne(ctx, sock)
		if last.OK {
			return last
		}
	}
	if last.Message == "" {
		last.Message = "Mini-Docker socket not found"
		last.Hint = "Start a single Mini-Docker daemon, e.g.\n  sudo python3 -m mini_docker daemon --socket $XDG_RUNTIME_DIR/mini-docker/mini-docker.sock --socket-mode 666\nEnsure only one daemon owns the socket (dual daemons cause EOF on create)."
	}
	return last
}

func probeOne(ctx context.Context, socketPath string) MiniDockerResult {
	res := MiniDockerResult{SocketPath: socketPath}
	info, err := os.Stat(socketPath)
	if err != nil {
		res.Message = fmt.Sprintf("socket not available at %s: %v", socketPath, err)
		res.Hint = "Start Mini-Docker daemon and verify socket path in ~/.cairn/cairnd-config.yaml (mini_docker_socket)."
		return res
	}
	// On Linux, sockets may not always report ModeSocket via Go; still try dial.
	_ = info

	dialer := net.Dialer{Timeout: 2 * time.Second}
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/containers/json", nil)
	if err != nil {
		res.Message = err.Error()
		return res
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Message = fmt.Sprintf("cannot talk to Mini-Docker at %s: %v", socketPath, err)
		res.Hint = "Socket may be stale, permission-denied, or owned by a crashed dual-daemon setup. Remove the stale socket and start exactly one daemon with --socket-mode 666 (or group-writable)."
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		res.Message = fmt.Sprintf("Mini-Docker returned HTTP %d for /containers/json", resp.StatusCode)
		res.Hint = "Daemon is listening but API is unhealthy; restart Mini-Docker."
		return res
	}

	// Optional: ensure JSON array/object
	var any json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&any)

	res.OK = true
	res.Message = fmt.Sprintf("Mini-Docker OK at %s", socketPath)
	return res
}

// DiscoverRootfs finds a Mini-Docker rootfs directory without hardcoding a home path.
func DiscoverRootfs() string {
	if v := os.Getenv("CAIRN_ROOTFS"); v != "" {
		if looksLikeRootfs(v) {
			return v
		}
	}
	if v := os.Getenv("MINI_DOCKER_ROOTFS"); v != "" {
		if looksLikeRootfs(v) {
			return v
		}
	}
	// Relative to cwd and parents
	wd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(wd, "rootfs"),
		filepath.Join(wd, "Mini-Docker", "rootfs"),
		filepath.Join(wd, "..", "Mini-Docker", "rootfs"),
		filepath.Join(wd, "..", "..", "Mini-Docker", "rootfs"),
		filepath.Join(wd, "..", "..", "..", "Mini-Docker", "rootfs"),
	}
	// Also try beside common Desktop layout without hardcoding username:
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates, filepath.Join(home, "Desktop", "Mini-Docker", "rootfs"))
		candidates = append(candidates, filepath.Join(home, "mini-docker", "rootfs"))
	}
	for _, c := range candidates {
		if looksLikeRootfs(c) {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs
			}
			return c
		}
	}
	return ""
}

func looksLikeRootfs(path string) bool {
	// busybox or sh under bin is enough for the counter-api example
	if st, err := os.Stat(filepath.Join(path, "bin", "busybox")); err == nil && !st.IsDir() {
		return true
	}
	if st, err := os.Stat(filepath.Join(path, "bin", "sh")); err == nil && !st.IsDir() {
		return true
	}
	return false
}
