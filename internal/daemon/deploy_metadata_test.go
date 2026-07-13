package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/deploymeta"
	"github.com/yumekaz/cairn/internal/store"
)

func setupDaemonStore(t *testing.T) (*Server, *store.Store, func()) {
	t.Helper()
	tmp, err := os.MkdirTemp("", "cairn-deploy-meta-*")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.NewStore(filepath.Join(tmp, "cairn.db"))
	if err != nil {
		os.RemoveAll(tmp)
		t.Fatal(err)
	}
	cfg := &config.DaemonConfig{DataDir: tmp, VolumeDir: filepath.Join(tmp, "volumes"), BackupDir: filepath.Join(tmp, "backups")}
	s := &Server{store: st, config: cfg}
	cleanup := func() {
		st.Close()
		os.RemoveAll(tmp)
	}
	return s, st, cleanup
}

// TestFailDeployRestoresCurrentDeployID drives the real failDeploy method and
// store APIs: after a successful deploy ID is current, a failed candidate must
// not remain current_deploy_id, and previous_deploy_id must be recorded.
func TestFailDeployRestoresCurrentDeployID(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()

	svcID := uuid.New().String()
	successID := uuid.New().String()
	failID := uuid.New().String()

	svc := &api.Service{
		ID:              svcID,
		Name:            "counter-api",
		Kind:            "web",
		RuntimeBackend:  "mini-docker",
		RuntimeID:       "container-healthy",
		CurrentDeployID: successID,
		DesiredState:    "running",
		ActualState:     "running",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeploy(&api.Deploy{
		ID: successID, ServiceID: svcID, Version: "v1", SourcePath: "inline",
		Status: "success", Stage: "completed", HealthStatus: "healthy",
	}); err != nil {
		t.Fatal(err)
	}

	// Same preparation sequence as handleCreateService
	prevID, keepCurrent := deploymeta.PrepareCandidate(svc.CurrentDeployID, failID)
	if prevID != successID || keepCurrent != successID {
		t.Fatalf("prepare: prev=%q keep=%q", prevID, keepCurrent)
	}
	failed := &api.Deploy{
		ID: failID, ServiceID: svcID, Version: "v1", SourcePath: "inline",
		Status: "pending", Stage: "starting", HealthStatus: "unhealthy",
		PreviousDeployID: prevID,
	}
	if err := st.CreateDeploy(failed); err != nil {
		t.Fatal(err)
	}
	svc.CurrentDeployID = keepCurrent
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}

	// Real failDeploy path (health-check failure equivalent)
	s.failDeploy(failed, svc, "health check failed: unhealthy HTTP status: 404")

	gotSvc, err := st.GetService(svcID)
	if err != nil || gotSvc == nil {
		t.Fatalf("get service: %v", err)
	}
	if gotSvc.CurrentDeployID != successID {
		t.Fatalf("current_deploy_id: want last success %s, got %s", successID, gotSvc.CurrentDeployID)
	}
	if gotSvc.RuntimeID != "container-healthy" {
		t.Fatalf("runtime must remain healthy container, got %q", gotSvc.RuntimeID)
	}

	gotDeploy, err := st.GetDeploy(failID)
	if err != nil || gotDeploy == nil {
		t.Fatalf("get failed deploy: %v", err)
	}
	if gotDeploy.Status != "failed" {
		t.Fatalf("deploy status: want failed, got %s", gotDeploy.Status)
	}
	if gotDeploy.PreviousDeployID == "" {
		t.Fatal("previous_deploy_id must be non-empty when a prior healthy deploy exists")
	}
	if gotDeploy.PreviousDeployID != successID {
		t.Fatalf("previous_deploy_id: want %s, got %s", successID, gotDeploy.PreviousDeployID)
	}
	if !deploymeta.IsHealthyCurrent(gotSvc.CurrentDeployID, successID, failID) {
		t.Fatal("post-failure metadata is not healthy")
	}
}

// TestTriggerServiceRedeployFailRestoresCurrent mirrors the fixed
// triggerServiceRedeploy prepare path (env/secret redeploy): candidate gets
// PreviousDeployID, current stays on last success during the attempt, and
// failDeploy restores current after a health-check-class failure.
func TestTriggerServiceRedeployFailRestoresCurrent(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()

	svcID := uuid.New().String()
	successID := uuid.New().String()
	failID := uuid.New().String()

	svc := &api.Service{
		ID:              svcID,
		Name:            "env-service",
		Kind:            "web",
		RuntimeBackend:  "mini-docker",
		RuntimeID:       "container-env-healthy",
		CurrentDeployID: successID,
		DesiredState:    "running",
		ActualState:     "running",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeploy(&api.Deploy{
		ID: successID, ServiceID: svcID, Version: "v1", SourcePath: "inline",
		Status: "success", Stage: "completed", HealthStatus: "healthy",
	}); err != nil {
		t.Fatal(err)
	}

	// Exact prepare sequence used by triggerServiceRedeploy after the fix
	lastSuccessfulDeployID := svc.CurrentDeployID
	prevID, keepCurrent := deploymeta.PrepareCandidate(lastSuccessfulDeployID, failID)
	if prevID != successID || keepCurrent != successID {
		t.Fatalf("triggerServiceRedeploy prepare: prev=%q keep=%q", prevID, keepCurrent)
	}
	failed := &api.Deploy{
		ID: failID, ServiceID: svcID, Version: "v_env_test", SourcePath: "env_update",
		Status: "pending", Stage: "starting", HealthStatus: "unhealthy",
		PreviousDeployID: prevID,
	}
	if err := st.CreateDeploy(failed); err != nil {
		t.Fatal(err)
	}
	svc.CurrentDeployID = keepCurrent
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	// Must not have flipped current to the env-update candidate
	mid, _ := st.GetService(svcID)
	if mid.CurrentDeployID != successID {
		t.Fatalf("during env redeploy current must stay %s, got %s", successID, mid.CurrentDeployID)
	}

	s.failDeploy(failed, svc, "health check failed: unhealthy HTTP status: 404")

	gotSvc, err := st.GetService(svcID)
	if err != nil || gotSvc == nil {
		t.Fatalf("get service: %v", err)
	}
	if gotSvc.CurrentDeployID != successID {
		t.Fatalf("after env redeploy fail, current_deploy_id want %s got %s", successID, gotSvc.CurrentDeployID)
	}
	if gotSvc.CurrentDeployID == failID {
		t.Fatal("current must not equal failed env-update candidate")
	}
	if gotSvc.RuntimeID != "container-env-healthy" {
		t.Fatalf("runtime must remain healthy, got %q", gotSvc.RuntimeID)
	}
	gotDeploy, _ := st.GetDeploy(failID)
	if gotDeploy.Status != "failed" {
		t.Fatalf("status want failed, got %s", gotDeploy.Status)
	}
	if gotDeploy.PreviousDeployID != successID {
		t.Fatalf("previous_deploy_id want %s got %s", successID, gotDeploy.PreviousDeployID)
	}
	if gotDeploy.PreviousDeployID == "" {
		t.Fatal("previous_deploy_id must be non-empty when prior healthy deploy exists")
	}
}

func TestFailDeployFirstDeployLeavesCurrentEmpty(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()

	svcID := uuid.New().String()
	failID := uuid.New().String()
	svc := &api.Service{
		ID: svcID, Name: "fresh", Kind: "web", RuntimeBackend: "mini-docker",
		DesiredState: "stopped", ActualState: "stopped",
	}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	prev, keep := deploymeta.PrepareCandidate("", failID)
	d := &api.Deploy{
		ID: failID, ServiceID: svcID, Version: "v1", Status: "pending",
		Stage: "starting", PreviousDeployID: prev,
	}
	if err := st.CreateDeploy(d); err != nil {
		t.Fatal(err)
	}
	svc.CurrentDeployID = keep
	_ = st.UpsertService(svc)

	s.failDeploy(d, svc, "create failed")
	got, _ := st.GetService(svcID)
	if got.CurrentDeployID != "" {
		t.Fatalf("first failed deploy must leave current empty, got %q", got.CurrentDeployID)
	}
}
