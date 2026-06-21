package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// RotatableFileWriter is a thread-safe writer that automatically rotates files based on size limits.
type RotatableFileWriter struct {
	mu          sync.Mutex
	logFilePath string
	file        *os.File
	maxSize     int64
	backups     int
}

// NewRotatableFileWriter creates a new RotatableFileWriter instance.
func NewRotatableFileWriter(logFilePath string, maxSize int64, backups int) *RotatableFileWriter {
	return &RotatableFileWriter{
		logFilePath: logFilePath,
		maxSize:     maxSize,
		backups:     backups,
	}
}

// Write writes to the log file, performing rotation if the file exceeds maxSize.
func (w *RotatableFileWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		if err := w.openFile(); err != nil {
			return 0, err
		}
	}

	info, err := w.file.Stat()
	if err == nil && info.Size() >= w.maxSize {
		if err := w.rotate(); err != nil {
			log.Printf("logging: failed to rotate logs: %v", err)
		}
	}

	return w.file.Write(p)
}

// Close closes the underlying log file.
func (w *RotatableFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

func (w *RotatableFileWriter) openFile() error {
	dir := filepath.Dir(w.logFilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(w.logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

func (w *RotatableFileWriter) rotate() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}

	// Shift backups: e.g. cairnd.log.2 -> cairnd.log.3
	for i := w.backups - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", w.logFilePath, i)
		newPath := fmt.Sprintf("%s.%d", w.logFilePath, i+1)
		if _, err := os.Stat(oldPath); err == nil {
			_ = os.Rename(oldPath, newPath)
		}
	}

	// Rename current log file to cairnd.log.1
	backupPath := w.logFilePath + ".1"
	if _, err := os.Stat(w.logFilePath); err == nil {
		if err := os.Rename(w.logFilePath, backupPath); err != nil {
			_ = w.openFile() // try to reopen the file on failure
			return err
		}
	}

	return w.openFile()
}
