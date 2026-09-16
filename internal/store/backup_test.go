package store

import (
	"github.com/yumekaz/cairn/internal/api"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupToIncludesUncheckpointedWAL(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	svc := &api.Service{ID: "wal-service", Name: "wal-service", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "snapshot.db")
	if err := st.BackupTo(path); err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	got, err := snapshot.GetService(svc.ID)
	if err != nil || got == nil || got.Name != svc.Name {
		t.Fatalf("snapshot omitted committed WAL state: %+v %v", got, err)
	}
}
