package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	tmpDir, err := os.MkdirTemp("", "cairn-store-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "cairn.db")
	st, err := NewStore(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create store: %v", err)
	}

	cleanup := func() {
		st.Close()
		os.RemoveAll(tmpDir)
	}

	return st, cleanup
}

func TestServicesCRUD(t *testing.T) {
	st, cleanup := setupTestStore(t)
	defer cleanup()

	svcID := uuid.New().String()
	svc := &api.Service{
		ID:             svcID,
		Name:           "test-service",
		Kind:           "web",
		RuntimeBackend: "mini-docker",
		RuntimeID:      "container-123",
		DesiredState:   "running",
		ActualState:    "stopped",
		Route:          "http://localhost:8080",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// 1. Create
	err := st.UpsertService(svc)
	if err != nil {
		t.Fatalf("UpsertService (insert) failed: %v", err)
	}

	// 2. Get by Name
	gotByName, err := st.GetServiceByName(svc.Name)
	if err != nil {
		t.Fatalf("GetServiceByName failed: %v", err)
	}
	if gotByName == nil {
		t.Fatal("expected service to be found")
	}
	if gotByName.ID != svcID {
		t.Errorf("expected ID '%s', got '%s'", svcID, gotByName.ID)
	}

	// 3. Get by ID
	gotByID, err := st.GetService(svcID)
	if err != nil {
		t.Fatalf("GetService failed: %v", err)
	}
	if gotByID == nil {
		t.Fatal("expected service to be found")
	}

	// 4. Update
	svc.ActualState = "running"
	err = st.UpsertService(svc)
	if err != nil {
		t.Fatalf("UpsertService (update) failed: %v", err)
	}

	gotUpdated, _ := st.GetService(svcID)
	if gotUpdated.ActualState != "running" {
		t.Errorf("expected actual state 'running', got '%s'", gotUpdated.ActualState)
	}

	// 5. List
	list, err := st.ListServices()
	if err != nil {
		t.Fatalf("ListServices failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 service, got %d", len(list))
	}

	// 6. Delete
	err = st.DeleteService(svcID)
	if err != nil {
		t.Fatalf("DeleteService failed: %v", err)
	}

	gotDeleted, _ := st.GetService(svcID)
	if gotDeleted != nil {
		t.Error("expected service to be deleted")
	}
}

func TestDeploysCRUD(t *testing.T) {
	st, cleanup := setupTestStore(t)
	defer cleanup()

	svcID := uuid.New().String()
	svc := &api.Service{
		ID:             svcID,
		Name:           "test-svc",
		Kind:           "web",
		RuntimeBackend: "mini-docker",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	st.UpsertService(svc)

	deployID := uuid.New().String()
	deploy := &api.Deploy{
		ID:           deployID,
		ServiceID:    svcID,
		Version:      "v1.0",
		SourcePath:   "/src",
		Status:       "running",
		Stage:        "health-checking",
		HealthStatus: "checking",
		StateTouched: true,
		CreatedAt:    time.Now(),
	}

	err := st.CreateDeploy(deploy)
	if err != nil {
		t.Fatalf("CreateDeploy failed: %v", err)
	}

	got, err := st.GetDeploy(deployID)
	if err != nil {
		t.Fatalf("GetDeploy failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected deploy to be found")
	}
	if got.StateTouched != true {
		t.Errorf("expected StateTouched true, got %t", got.StateTouched)
	}

	deploy.Status = "success"
	deploy.HealthStatus = "healthy"
	deploy.StateTouched = false
	now := time.Now()
	deploy.CompletedAt = &now

	err = st.UpdateDeploy(deploy)
	if err != nil {
		t.Fatalf("UpdateDeploy failed: %v", err)
	}

	gotUpdated, _ := st.GetDeploy(deployID)
	if gotUpdated.Status != "success" {
		t.Errorf("expected status 'success', got '%s'", gotUpdated.Status)
	}
	if gotUpdated.StateTouched != false {
		t.Errorf("expected StateTouched to be updated to false, got %t", gotUpdated.StateTouched)
	}
}

func TestVolumesAndBackups(t *testing.T) {
	st, cleanup := setupTestStore(t)
	defer cleanup()

	svcID := uuid.New().String()
	svc := &api.Service{
		ID:             svcID,
		Name:           "test-svc",
		Kind:           "web",
		RuntimeBackend: "mini-docker",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	st.UpsertService(svc)

	volID := uuid.New().String()
	vol := &api.Volume{
		ID:                volID,
		Name:              "data-vol",
		HostPath:          "/host/path",
		AttachedServiceID: svcID,
		MountPath:         "/data",
		Status:            "active",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	err := st.UpsertVolume(vol)
	if err != nil {
		t.Fatalf("UpsertVolume failed: %v", err)
	}

	gotVol, err := st.GetVolumeByName(vol.Name)
	if err != nil {
		t.Fatalf("GetVolumeByName failed: %v", err)
	}
	if gotVol == nil {
		t.Fatal("expected volume to be found")
	}

	vols, err := st.ListVolumes()
	if err != nil {
		t.Fatalf("ListVolumes failed: %v", err)
	}
	if len(vols) != 1 {
		t.Errorf("expected 1 volume, got %d", len(vols))
	}

	backupID := uuid.New().String()
	backup := &api.Backup{
		ID:         backupID,
		VolumeID:   volID,
		BackupPath: "/backups/data-vol.tar.gz",
		Status:     "success",
		SizeBytes:  1024,
		Checksum:   "abcdef",
		CreatedAt:  time.Now(),
	}

	err = st.CreateBackup(backup)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	backups, err := st.ListBackups(volID)
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 1 {
		t.Errorf("expected 1 backup, got %d", len(backups))
	}
}

func TestFailIncompleteBackups(t *testing.T) {
	st, cleanup := setupTestStore(t)
	defer cleanup()

	svcID := uuid.New().String()
	if err := st.UpsertService(&api.Service{
		ID: svcID, Name: "svc-fail-bak", Kind: "web", RuntimeBackend: "mini-docker",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertService: %v", err)
	}
	volID := uuid.New().String()
	if err := st.UpsertVolume(&api.Volume{
		ID: volID, Name: "vol-fail-bak", HostPath: "/tmp/vol-fail-bak",
		AttachedServiceID: svcID, MountPath: "/data", Status: "active",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertVolume: %v", err)
	}

	pendingID := uuid.New().String()
	if err := st.CreateBackup(&api.Backup{
		ID: pendingID, VolumeID: volID, BackupPath: "/tmp/pending.tar.gz",
		Status: "pending", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateBackup pending: %v", err)
	}
	okID := uuid.New().String()
	if err := st.CreateBackup(&api.Backup{
		ID: okID, VolumeID: volID, BackupPath: "/tmp/ok.tar.gz",
		Status: "success", SizeBytes: 10, Checksum: "abc", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateBackup success: %v", err)
	}

	incomplete, err := st.ListIncompleteBackups()
	if err != nil {
		t.Fatalf("ListIncompleteBackups: %v", err)
	}
	if len(incomplete) != 1 || incomplete[0].ID != pendingID {
		t.Fatalf("expected only pending incomplete, got %#v", incomplete)
	}

	n, err := st.FailIncompleteBackups("interrupted (daemon restart)")
	if err != nil {
		t.Fatalf("FailIncompleteBackups: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row failed, got %d", n)
	}

	got, err := st.GetBackup(pendingID)
	if err != nil || got == nil {
		t.Fatalf("GetBackup pending: %v %#v", err, got)
	}
	if got.Status != "failed" {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.FailureReason != "interrupted (daemon restart)" {
		t.Fatalf("unexpected reason %q", got.FailureReason)
	}
	if got.CompletedAt == nil {
		t.Fatal("expected completed_at set")
	}

	ok, err := st.GetBackup(okID)
	if err != nil || ok == nil || ok.Status != "success" {
		t.Fatalf("success backup must stay success: %#v err=%v", ok, err)
	}

	n2, err := st.FailIncompleteBackups("again")
	if err != nil {
		t.Fatalf("second FailIncompleteBackups: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 rows on second pass, got %d", n2)
	}
}

func TestEvents(t *testing.T) {
	st, cleanup := setupTestStore(t)
	defer cleanup()

	svcID := uuid.New().String()
	event := &api.Event{
		ID:        uuid.New().String(),
		ServiceID: &svcID,
		Type:      "test-event-type",
		Message:   "This is a test event log",
		CreatedAt: time.Now(),
	}

	err := st.CreateEvent(event)
	if err != nil {
		t.Fatalf("CreateEvent failed: %v", err)
	}

	eventsList, err := st.ListEvents(EventFilter{})
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(eventsList) != 1 {
		t.Errorf("expected 1 event, got %d", len(eventsList))
	}
}
