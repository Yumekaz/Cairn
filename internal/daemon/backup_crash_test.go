package daemon

import (
	"bytes"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The helper executes the production archive and metadata path in a process
// that the parent kills only after observing pending metadata and archive bytes.
func TestBackupCrashHelper(t *testing.T) {
	dir := os.Getenv("CAIRN_BACKUP_CRASH_FIXTURE")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	st, err := store.NewStore(filepath.Join(dir, "cairn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	vol := &api.Volume{ID: "crash-volume", Name: "crash-volume", HostPath: filepath.Join(dir, "volume"), Status: "available", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := st.UpsertVolume(vol); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st, config: &config.DaemonConfig{BackupDir: filepath.Join(dir, "backups")}}
	_, err = s.performVolumeBackup(vol)
	t.Fatalf("backup must remain blocked until SIGKILL, returned %v", err)
}

func TestBackupProcessCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	volume := filepath.Join(dir, "volume")
	if err := os.MkdirAll(volume, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(volume, "a-data"), bytes.Repeat([]byte("durable-data\n"), 100000), 0600); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(volume, "z-barrier")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	// Initialize before starting the child to avoid concurrent schema creation.
	st, err := store.NewStore(filepath.Join(dir, "cairn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBackupCrashHelper$")
	cmd.Env = append(os.Environ(), "CAIRN_BACKUP_CRASH_FIXTURE="+dir)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	var interrupted *api.Backup
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := st.ListIncompleteBackups()
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 1 {
			if info, err := os.Stat(pending[0].BackupPath); err == nil && info.Size() > 0 {
				interrupted = pending[0]
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if interrupted == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("never saw pending archive bytes: %s", output.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("helper exited successfully instead of being killed")
	}
	s := &Server{store: st}
	s.failIncompleteBackupsOnStartup()
	got, err := st.GetBackup(interrupted.ID)
	if err != nil || got.Status != "failed" || got.FailureReason == "" {
		t.Fatalf("unsafe backup after recovery: %+v %v", got, err)
	}
	pending, err := st.ListIncompleteBackups()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after recovery: %+v %v", pending, err)
	}
	t.Log("SIGKILL after pending archive bytes; startup marked interrupted backup failed")
}
