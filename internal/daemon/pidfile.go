package daemon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// ManagePIDFile holds a kernel lock for the daemon's lifetime. The persisted PID
// is only a hint: after a crash or reboot it may identify an unrelated process.
type ManagePIDFile struct {
	path string
	mu   sync.Mutex
	file *os.File
}

// NewPIDFile helper.
func NewPIDFile(path string) *ManagePIDFile {
	return &ManagePIDFile{path: path}
}

// Acquire locks the canonical inode before updating its PID, without truncating
// a competing owner's file. Independent opens conflict even in the same process.
func (p *ManagePIDFile) Acquire() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file != nil {
		return fmt.Errorf("daemon PID file is already acquired")
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0755); err != nil {
		return err
	}
	f, err := openPIDFile(p.path, unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		return err
	}
	// Close also drops the flock on every failure path.
	defer func() {
		if p.file != f {
			_ = f.Close()
		}
	}()
	if err := lockPIDFile(f); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("daemon PID file is already locked: %w", err)
		}
		return err
	}
	if err := checkPIDFilePath(f); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		return err
	}
	p.file = f
	return nil
}

// Release closes only this owner's descriptor. Never unlink the file: a waiter
// may already have it open, and unlinking would permit two locked inodes at once.
// Leaving stale PID text is safe because only the kernel lock confers ownership.
func (p *ManagePIDFile) Release() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return nil
	}
	f := p.file
	p.file = nil
	return f.Close()
}

func openPIDFile(path string, flags int) (*os.File, error) {
	// NONBLOCK prevents a substituted FIFO from hanging open before fstat.
	fd, err := unix.Open(path, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0644)
	if err != nil {
		return nil, fmt.Errorf("open daemon PID file: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	if err := checkPIDFilePath(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func checkPIDFilePath(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("daemon PID file is not a regular file")
	}
	canonical, err := os.Lstat(f.Name())
	if err != nil {
		return err
	}
	if !canonical.Mode().IsRegular() || !os.SameFile(info, canonical) {
		return fmt.Errorf("daemon PID file pathname changed")
	}
	return nil
}

func lockPIDFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

// StopPIDFile signals only a proven lock owner through a stable Linux pidfd.
// It fails closed if ownership or pidfd support cannot be established; neither
// kill(pid, 0) nor a bare numeric kill is a safe fallback. The data directory must
// be trusted: external removal/replacement of a live lock file is unsupported.
func StopPIDFile(path string, timeout time.Duration) error {
	f, err := openPIDFile(path, unix.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockPIDFile(f); err == nil {
		return fmt.Errorf("no daemon owns PID file %s (stale PID ignored)", path)
	} else if !errors.Is(err, unix.EWOULDBLOCK) {
		return fmt.Errorf("check daemon PID file lock: %w", err)
	}

	// Bound the read, and exclude process-group/special PIDs before any syscall.
	var data [32]byte
	n, err := f.ReadAt(data[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	pid, err := strconv.ParseInt(strings.TrimSpace(string(data[:n])), 10, 32)
	if err != nil || pid <= 1 || n == len(data) {
		return fmt.Errorf("invalid PID in daemon PID file")
	}
	// Pin first, then prove that this PID has the canonical inode's actual
	// exclusive flock in its own fd table. An arbitrary busy lock is not proof.
	pidfd, err := unix.PidfdOpen(int(pid), 0)
	if err != nil {
		return fmt.Errorf("pin daemon PID: %w", err)
	}
	defer unix.Close(pidfd)
	owned, err := ownsPIDFileLock(int(pid), f)
	if err != nil {
		return fmt.Errorf("verify daemon PID file owner: %w", err)
	}
	if !owned {
		return fmt.Errorf("PID %d does not own daemon PID file lock; refusing to signal", pid)
	}
	if err := checkPIDFilePath(f); err != nil {
		return err
	}
	if err := unix.PidfdSendSignal(pidfd, unix.SIGTERM, nil, 0); err != nil {
		return fmt.Errorf("signal daemon PID file owner: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		if err := checkPIDFilePath(f); err != nil {
			return err
		}
		if err := lockPIDFile(f); err == nil {
			return nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("wait for daemon PID file lock release: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon did not release PID file lock within timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func ownsPIDFileLock(pid int, f *os.File) (bool, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		return false, err
	}
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fdinfo")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if os.IsNotExist(err) {
			continue // Unrelated descriptors may close during the scan.
		}
		if err != nil {
			return false, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if matchesPIDFileLock(line, pid, &stat) {
				return true, nil
			}
		}
	}
	return false, nil
}

func matchesPIDFileLock(line string, pid int, stat *unix.Stat_t) bool {
	// Kernel fdinfo format: lock: 1: FLOCK ADVISORY WRITE PID MAJ:MIN:INO 0 EOF
	// Ignore POSIX/OFD/read/blocked/range locks: none is our lifetime flock.
	fields := strings.Fields(line)
	if len(fields) != 9 || fields[0] != "lock:" || fields[2] != "FLOCK" ||
		fields[3] != "ADVISORY" || fields[4] != "WRITE" ||
		fields[5] != strconv.Itoa(pid) || fields[7] != "0" || fields[8] != "EOF" {
		return false
	}
	identity := strings.Split(fields[6], ":")
	if len(identity) != 3 {
		return false
	}
	major, e1 := strconv.ParseUint(identity[0], 16, 32)
	minor, e2 := strconv.ParseUint(identity[1], 16, 32)
	ino, e3 := strconv.ParseUint(identity[2], 10, 64)
	return e1 == nil && e2 == nil && e3 == nil &&
		uint32(major) == unix.Major(stat.Dev) && uint32(minor) == unix.Minor(stat.Dev) && ino == stat.Ino
}
