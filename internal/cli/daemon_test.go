package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/daemon"
)

// Every PID written by these tests belongs to an explicitly spawned fixture.
// Helpers intercept SIGTERM; no host process or guessed PID is ever signaled.
func TestDaemonPIDFileStopHelper(t *testing.T) {
	mode := os.Getenv("CAIRN_PIDFILE_STOP_HELPER")
	if mode == "" {
		return
	}
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	var owner *daemon.ManagePIDFile
	if mode == "owner" {
		owner = daemon.NewPIDFile(os.Getenv("CAIRN_PIDFILE_STOP_PATH"))
		if err := owner.Acquire(); err != nil {
			t.Fatal(err)
		}
		defer owner.Release()
	}
	commands := make(chan string)
	go func() {
		defer close(commands)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			commands <- scanner.Text()
		}
	}()
	fmt.Println("ready")
	for {
		select {
		case <-signals:
			fmt.Println("signaled")
		case command, ok := <-commands:
			if !ok || command == "exit" {
				return
			}
			switch command {
			case "probe":
				fmt.Println("quiet")
			case "release":
				if owner == nil {
					t.Fatal("fixture has no ownership to release")
				}
				if err := owner.Release(); err != nil {
					t.Fatal(err)
				}
				fmt.Println("released")
			}
		}
	}
}

type pidFileStopFixture struct {
	cmd   *exec.Cmd
	input io.WriteCloser
	lines chan string
}

func startPIDFileStopFixture(t *testing.T, mode, path string) *pidFileStopFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDaemonPIDFileStopHelper$")
	cmd.Env = append(os.Environ(), "CAIRN_PIDFILE_STOP_HELPER="+mode, "CAIRN_PIDFILE_STOP_PATH="+path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	f := &pidFileStopFixture{cmd: cmd, input: input, lines: make(chan string, 32)}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(f.lines)
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			f.lines <- scanner.Text()
		}
	}()
	t.Cleanup(func() {
		_ = input.Close()
		// EOF makes helpers exit, releasing locks even after a failed assertion.
		// The deadline can kill only the exact child started by this fixture.
		<-readDone
		err := cmd.Wait()
		cancel()
		if err != nil {
			t.Errorf("PID-file fixture exit: %v: %s", err, stderr.String())
		}
	})
	f.expect(t, "ready")
	return f
}

func (f *pidFileStopFixture) expect(t *testing.T, want string) {
	t.Helper()
	select {
	case got, ok := <-f.lines:
		if !ok || got != want {
			t.Fatalf("fixture response: got %q (open=%v), want %q", got, ok, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for fixture response %q", want)
	}
}

func (f *pidFileStopFixture) send(t *testing.T, command string) {
	t.Helper()
	if _, err := fmt.Fprintln(f.input, command); err != nil {
		t.Fatal(err)
	}
}

func stopTestPIDFile(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".cairn")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "cairnd.pid")
}

func TestDaemonStopRefusesUnownedLivePID(t *testing.T) {
	path := stopTestPIDFile(t)
	unrelated := startPIDFileStopFixture(t, "unowned", path)
	if err := os.WriteFile(path, []byte(strconv.Itoa(unrelated.cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := daemonStopCmd.RunE(daemonStopCmd, nil); err == nil || !strings.Contains(err.Error(), "no daemon owns") {
		t.Fatalf("stop must refuse stale live PID without signaling: %v", err)
	}
	unrelated.send(t, "probe")
	unrelated.expect(t, "quiet")
}

func TestDaemonStopRequiresPIDToOwnCanonicalLock(t *testing.T) {
	path := stopTestPIDFile(t)
	// Even a real flock on a different inode is not ownership of this file.
	unrelated := startPIDFileStopFixture(t, "owner", filepath.Join(filepath.Dir(path), "other.pid"))
	owner := startPIDFileStopFixture(t, "owner", path)
	if err := os.WriteFile(path, []byte(strconv.Itoa(unrelated.cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := daemonStopCmd.RunE(daemonStopCmd, nil); err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("stop trusted a live PID that did not hold this inode's lock: %v", err)
	}
	for _, f := range []*pidFileStopFixture{unrelated, owner} {
		f.send(t, "probe")
		f.expect(t, "quiet")
	}
}

func TestDaemonStopRefusesSymlink(t *testing.T) {
	path := stopTestPIDFile(t)
	unrelated := startPIDFileStopFixture(t, "owner", filepath.Join(filepath.Dir(path), "target.pid"))
	if err := os.Symlink("target.pid", path); err != nil {
		t.Fatal(err)
	}
	if err := daemonStopCmd.RunE(daemonStopCmd, nil); err == nil {
		t.Fatal("stop followed a symlink")
	}
	unrelated.send(t, "probe")
	unrelated.expect(t, "quiet")
}

func TestDaemonStopRejectsMissingOrNonRegularFile(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		_ = stopTestPIDFile(t)
		if err := daemonStopCmd.RunE(daemonStopCmd, nil); err == nil || !strings.Contains(err.Error(), "no daemon PID file found") {
			t.Fatalf("missing PID file: %v", err)
		}
	})
	t.Run("fifo", func(t *testing.T) {
		path := stopTestPIDFile(t)
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		if err := daemonStopCmd.RunE(daemonStopCmd, nil); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("FIFO PID file: %v", err)
		}
	})
}

func TestDaemonStopRejectsInvalidPID(t *testing.T) {
	for _, text := range []string{"", "broken", "0", "-1", "1", "2147483648", strings.Repeat("9", 40)} {
		t.Run(strconv.Quote(text), func(t *testing.T) {
			path := stopTestPIDFile(t)
			owner := startPIDFileStopFixture(t, "owner", path)
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			if err := daemonStopCmd.RunE(daemonStopCmd, nil); err == nil || !strings.Contains(err.Error(), "invalid PID") {
				t.Fatalf("invalid PID must be refused before signaling: %v", err)
			}
			owner.send(t, "probe")
			owner.expect(t, "quiet")
		})
	}
}

func TestDaemonStopWaitsForLockRelease(t *testing.T) {
	path := stopTestPIDFile(t)
	owner := startPIDFileStopFixture(t, "owner", path)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- daemonStopCmd.RunE(daemonStopCmd, nil) }()
	owner.expect(t, "signaled")
	select {
	case err := <-stopped:
		t.Fatalf("stop returned before owner released lock: %v", err)
	default:
	}
	owner.send(t, "release")
	owner.expect(t, "released")
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop waited for pathname disappearance instead of lock release")
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("stop did not preserve canonical inode: %v", err)
	}
	// The helper is deliberately still alive. Completion depends on the lock.
	owner.send(t, "probe")
	owner.expect(t, "quiet")
	successor := daemon.NewPIDFile(path)
	if err := successor.Acquire(); err != nil {
		t.Fatalf("stop leaked its probe lock: %v", err)
	}
	defer successor.Release()
}

func TestDaemonStopTimesOutWhileOwnerKeepsLock(t *testing.T) {
	path := stopTestPIDFile(t)
	owner := startPIDFileStopFixture(t, "owner", path)
	stopped := make(chan error, 1)
	go func() { stopped <- daemonStopCmd.RunE(daemonStopCmd, nil) }()
	owner.expect(t, "signaled")
	select {
	case err := <-stopped:
		if err == nil || !strings.Contains(err.Error(), "within timeout") {
			t.Fatalf("stop succeeded while owner kept lock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not bound lock-release wait")
	}
	contender := daemon.NewPIDFile(path)
	if err := contender.Acquire(); err == nil {
		_ = contender.Release()
		t.Fatal("timed-out stop released another process's lock")
	}
}
