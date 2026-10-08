package daemon

import (
	"bytes"
	"crypto/rand"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/store"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoundedBackupENOSPC(t *testing.T) {
	root := os.Getenv("CAIRN_TEST_ENOSPC_DIR")
	if root == "" {
		t.Skip("requires an explicitly bounded disposable tmpfs")
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(root, &fs); err != nil {
		t.Fatal(err)
	}
	if fs.Type != unix.TMPFS_MAGIC || uint64(fs.Blocks)*uint64(fs.Bsize) > 16*1024*1024 {
		t.Fatal("refusing to fill anything other than a <=16MiB disposable tmpfs")
	}
	backupRoot, err := os.MkdirTemp(root, "cairn-enospc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(backupRoot) })
	dir := t.TempDir()
	st, err := store.NewStore(filepath.Join(dir, "cairn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	vol := &api.Volume{ID: "enospc-volume", Name: "enospc-volume", HostPath: filepath.Join(dir, "volume"), Status: "available", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := os.MkdirAll(vol.HostPath, 0700); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 256*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vol.HostPath, "record"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertVolume(vol); err != nil {
		t.Fatal(err)
	}
	// Exhaust only this verified tiny tmpfs, never the host's /tmp or disk.
	free := uint64(fs.Bavail) * uint64(fs.Bsize)
	if free < 64*1024 {
		t.Fatal("fixture is already full")
	}
	filler := filepath.Join(backupRoot, "filler")
	if err := os.WriteFile(filler, make([]byte, int(free-32*1024)), 0600); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: st, config: &config.DaemonConfig{BackupDir: backupRoot}}
	if _, err := server.performVolumeBackup(vol); err == nil || !strings.Contains(err.Error(), "no space left") {
		t.Fatalf("want actual ENOSPC, got %v", err)
	}
	backups, err := st.ListBackups(vol.ID)
	if err != nil || len(backups) != 1 || backups[0].Status != "failed" || backups[0].FailureReason == "" {
		t.Fatalf("false backup status: %+v %v", backups, err)
	}
	got, err := os.ReadFile(filepath.Join(vol.HostPath, "record"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("failed backup changed source data")
	}
	if err := os.Remove(filler); err != nil {
		t.Fatal(err)
	}
	// Legacy volume backup IDs have one-second precision.
	time.Sleep(time.Second)
	if backup, err := server.performVolumeBackup(vol); err != nil || backup.Status != "success" {
		t.Fatalf("backup did not recover after space returned: %+v %v", backup, err)
	}
	t.Log("actual bounded ENOSPC: failed metadata, unchanged source, subsequent successful backup")
}
