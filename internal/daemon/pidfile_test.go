package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPIDFileAcquireIgnoresUnownedLivePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cairnd.pid")
	// This test process is alive, but it does not own the daemon lock.
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	p := NewPIDFile(path)
	if err := p.Acquire(); err != nil {
		t.Fatalf("persisted PID of an unrelated live process blocked acquisition: %v", err)
	}
	t.Cleanup(func() { _ = p.Release() })
}

func TestPIDFileReleasePreservesInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cairnd.pid")
	p := NewPIDFile(path)
	if err := p.Acquire(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Release() })
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// An already-open contender must not end up locking an abandoned inode.
	contender, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	if err := p.Release(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("release removed the canonical lock inode: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("release replaced the canonical lock inode")
	}
	if err := lockPIDFile(contender); err != nil {
		t.Fatal(err)
	}
	successor := NewPIDFile(path)
	if err := successor.Acquire(); err == nil {
		_ = successor.Release()
		t.Fatal("new acquisition bypassed the already-open contender's inode lock")
	}
	if err := unix.Flock(int(contender.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := successor.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer successor.Release()
	if err := p.Release(); err != nil {
		t.Fatal(err)
	}
	other := NewPIDFile(path)
	if err := other.Acquire(); err == nil {
		_ = other.Release()
		t.Fatal("repeated old-owner release unlocked the successor")
	}
}

func TestPIDFileAcquireRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "unrelated-file")
	want := "must not overwrite this file"
	if err := os.WriteFile(target, []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cairnd.pid")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	p := NewPIDFile(path)
	err := p.Acquire()
	if err == nil {
		_ = p.Release()
		t.Error("acquired a symlink instead of refusing it")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil || string(got) != want {
		t.Fatalf("symlink target changed: got %q, error %v", got, readErr)
	}
}

func TestPIDFileContenderCannotReleaseOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cairnd.pid")
	owner, contender := NewPIDFile(path), NewPIDFile(path)
	if err := owner.Acquire(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Release() })
	if err := contender.Acquire(); err == nil {
		_ = contender.Release()
		t.Fatal("second owner acquired the PID file")
	}
	if err := contender.Release(); err != nil {
		t.Fatal(err)
	}
	third := NewPIDFile(path)
	if err := third.Acquire(); err == nil {
		_ = third.Release()
		t.Fatal("failed contender released the real owner's lock")
	}
}

func TestPIDFileLockNotPIDTextIsOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cairnd.pid")
	owner := NewPIDFile(path)
	if err := owner.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer owner.Release()
	if err := owner.Acquire(); err == nil {
		t.Fatal("same manager acquired twice")
	}
	want := "corrupt PID hint"
	if err := os.WriteFile(path, []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	contender := NewPIDFile(path)
	if err := contender.Acquire(); err == nil {
		_ = contender.Release()
		t.Fatal("corrupt PID text bypassed the owner's kernel lock")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Fatalf("failed contender modified the PID hint: %q, %v", got, err)
	}
	flags, err := unix.FcntlInt(owner.file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("owner fd must be close-on-exec: flags=%d err=%v", flags, err)
	}
}

func TestPIDFileAcquireRejectsNonRegular(t *testing.T) {
	for _, kind := range []string{"directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cairnd.pid")
			var err error
			if kind == "directory" {
				err = os.Mkdir(path, 0700)
			} else {
				err = unix.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			p := NewPIDFile(path)
			if err := p.Acquire(); err == nil {
				_ = p.Release()
				t.Fatal("acquired a non-regular PID file")
			}
		})
	}
}

func TestPIDFileConcurrentAcquireRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cairnd.pid")
	var active atomic.Int32
	var owners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 12 {
		wg.Go(func() {
			<-start
			for range 60 {
				p := NewPIDFile(path)
				if err := p.Acquire(); err != nil {
					if !errors.Is(err, unix.EWOULDBLOCK) {
						t.Errorf("acquire: %v", err)
					}
					if err := p.Release(); err != nil {
						t.Errorf("non-owner release: %v", err)
					}
					continue
				}
				owners.Add(1)
				if got := active.Add(1); got != 1 {
					t.Errorf("%d concurrent owners", got)
				}
				// A separate open must conflict throughout this ownership interval.
				f, err := os.OpenFile(path, os.O_RDWR, 0)
				if err != nil {
					t.Errorf("canonical inode missing: %v", err)
				} else {
					if err := lockPIDFile(f); !errors.Is(err, unix.EWOULDBLOCK) {
						t.Errorf("owner lock bypassed: %v", err)
					}
					_ = f.Close()
				}
				active.Add(-1)
				if err := p.Release(); err != nil {
					t.Errorf("owner release: %v", err)
				}
			}
		})
	}
	close(start)
	wg.Wait()
	if owners.Load() == 0 {
		t.Fatal("no contender acquired ownership")
	}
}

func TestPIDFileConcurrentManagerCalls(t *testing.T) {
	p := NewPIDFile(filepath.Join(t.TempDir(), "cairnd.pid"))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 30 {
				_ = p.Acquire()
				if err := p.Release(); err != nil {
					t.Errorf("release: %v", err)
				}
			}
		})
	}
	wg.Wait()
	_ = p.Release()
}

func TestPIDFileProcessHelper(t *testing.T) {
	path := os.Getenv("CAIRN_PIDFILE_PROCESS_TEST")
	if path == "" {
		return
	}
	p := NewPIDFile(path)
	if err := p.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer p.Release()
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestPIDFileProcessExitReleasesLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cairnd.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPIDFileProcessHelper$")
	cmd.Env = append(os.Environ(), "CAIRN_PIDFILE_PROCESS_TEST="+path)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		// Only the child created by this fixture can be signaled here.
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("helper never acquired lock: %q %v", line, err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	contender := NewPIDFile(path)
	if err := contender.Acquire(); err == nil {
		_ = contender.Release()
		t.Fatal("second process acquired a live owner's lock")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err == nil {
		t.Fatal("helper survived fixture SIGKILL")
	}
	if err := contender.Acquire(); err != nil {
		t.Fatalf("process death did not release kernel lock: %v", err)
	}
	defer contender.Release()
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("crash recovery replaced lock inode: %v", err)
	}
}

func TestPIDFileKernelOwnerEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cairnd.pid")
	p := NewPIDFile(path)
	if err := p.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer p.Release()
	f, err := openPIDFile(path, unix.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if owned, err := ownsPIDFileLock(os.Getpid(), f); err != nil || !owned {
		t.Fatalf("missing kernel owner evidence: owned=%v err=%v", owned, err)
	}
	if err := p.Release(); err != nil {
		t.Fatal(err)
	}
	if owned, err := ownsPIDFileLock(os.Getpid(), f); err != nil || owned {
		t.Fatalf("released file still proves ownership: owned=%v err=%v", owned, err)
	}
}

func TestPIDFileLockEvidenceParser(t *testing.T) {
	stat := unix.Stat_t{Dev: unix.Mkdev(0, 19), Ino: 11691}
	good := "lock:\t1: FLOCK  ADVISORY  WRITE 359 00:13:11691 0 EOF"
	if !matchesPIDFileLock(good, 359, &stat) {
		t.Fatal("valid kernel lock evidence rejected")
	}
	for _, bad := range []string{
		strings.Replace(good, "359", "360", 1),
		strings.Replace(good, "FLOCK", "POSIX", 1),
		strings.Replace(good, "FLOCK", "OFDLCK", 1),
		strings.Replace(good, "ADVISORY", "MANDATORY", 1),
		strings.Replace(good, "WRITE", "READ", 1),
		strings.Replace(good, "11691", "11692", 1),
		strings.Replace(good, "00:13", "00:14", 1),
		strings.Replace(good, "00:13", "01:13", 1),
		strings.Replace(good, "0 EOF", "1 EOF", 1),
		strings.Replace(good, "0 EOF", "0 42", 1),
		strings.Replace(good, "FLOCK", "-> FLOCK", 1),
		"lock: garbage", "",
	} {
		if matchesPIDFileLock(bad, 359, &stat) {
			t.Errorf("accepted invalid owner evidence %q", bad)
		}
	}
}
