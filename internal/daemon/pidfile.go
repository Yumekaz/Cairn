package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// ManagePIDFile handles creation, validation, and deletion of the daemon PID file.
type ManagePIDFile struct {
	path string
}

// NewPIDFile helper.
func NewPIDFile(path string) *ManagePIDFile {
	return &ManagePIDFile{path: path}
}

// Acquire writes the current PID to the PID file if it is not locked by another running daemon.
func (p *ManagePIDFile) Acquire() error {
	// Ensure directory exists
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Check if already exists
	if data, err := os.ReadFile(p.path); err == nil {
		oldPID, err := strconv.Atoi(string(data))
		if err == nil {
			// Check if process is still running
			process, err := os.FindProcess(oldPID)
			if err == nil {
				// Sending signal 0 check if process is alive (Unix specific)
				if err := process.Signal(syscall.Signal(0)); err == nil {
					return fmt.Errorf("daemon is already running with PID %d", oldPID)
				}
			}
		}
	}

	// Write current PID
	pid := os.Getpid()
	if err := os.WriteFile(p.path, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return err
	}

	return nil
}

// Release removes the PID file.
func (p *ManagePIDFile) Release() error {
	return os.Remove(p.path)
}
