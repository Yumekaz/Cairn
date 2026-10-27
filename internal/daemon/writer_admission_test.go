package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/store"
	dfengine "github.com/yumekaz/duraflow/pkg/engine"
	dfexecutor "github.com/yumekaz/duraflow/pkg/executor"
	dfstore "github.com/yumekaz/duraflow/pkg/store"
)

func TestNewServiceCannotTakeAnotherOwnersVolume(t *testing.T) {
	s, svc, vol, _, _ := restoreQuiescenceFixture(t)
	body, _ := json.Marshal(api.ServiceConfig{Name: "competing-service", Kind: "web", Image: "fake-image", Volumes: []api.VolumeConfig{{Name: vol.Name, MountPath: "/data"}}})
	response := httptest.NewRecorder()
	s.handleCreateService(response, httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body)))
	if response.Code != http.StatusConflict {
		t.Fatalf("owner conflict should precede deploy preparation: %d %s", response.Code, response.Body.String())
	}
	other, err := s.store.GetServiceByName("competing-service")
	if err != nil || other != nil {
		t.Errorf("rejected request created a service: %+v,%v", other, err)
	}
	saved, err := s.store.GetVolume(vol.ID)
	if err != nil || saved.AttachedServiceID != svc.ID {
		t.Errorf("live owner overwritten: %+v,%v", saved, err)
	}
	requireRestoreUntouched(t, vol)
}

func TestDeployAdmissionConflictDoesNotScheduleOrOrphanPreparedDeploy(t *testing.T) {
	s, svc, _, _, _ := restoreQuiescenceFixture(t)
	svc.CurrentDeployID = "aaaaaaaa-previous"
	if err := s.store.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	previous := &api.Deploy{ID: svc.CurrentDeployID, ServiceID: svc.ID, Status: "success", Stage: "completed", HealthStatus: "healthy"}
	prepared := &api.Deploy{ID: "bbbbbbbb-prepared", ServiceID: svc.ID, PreviousDeployID: previous.ID, Status: "pending", Stage: "starting"}
	for _, dep := range []*api.Deploy{previous, prepared} {
		if err := s.store.CreateDeploy(dep); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.CreateWorkflow(&api.Workflow{ID: "reserved-restore", Type: "restore", Status: "pending", InputJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	ds := dfstore.NewSQLiteStore(filepath.Join(s.config.DataDir, "admission-duraflow.db"))
	if err := ds.Init(); err != nil {
		t.Fatal(err)
	}
	defer ds.Close()
	real := dfengine.NewWorkflowEngine(ds, dfexecutor.NewRegistry())
	s.duraflow = duraflow.NewEngine(s.store, s.runtime)
	s.duraflow.SetRealEngine(real)
	s.RegisterDuraFlowTemplates(s.duraflow)
	scheduled := false
	real.OnEvent = func(ev *dfstore.Event) {
		if ev.EventType == dfengine.EventWorkflowRunCreated {
			scheduled = true
		}
	}
	_, err := s.runDeployWorkflowSync(context.Background(), &api.ServiceConfig{Name: svc.Name, Kind: "web"}, prepared, svc, svc.RuntimeID)
	if !errors.Is(err, store.ErrWorkflowConflict) {
		t.Fatalf("expected definite unscheduled conflict: %v", err)
	}
	if scheduled {
		t.Error("conflicting writer was scheduled")
	}
	active, err := s.store.ServiceHasActiveDeploy(svc.ID)
	if err != nil || active {
		t.Errorf("unscheduled pending deploy orphaned: %v,%v", active, err)
	}
	saved, err := s.store.GetService(svc.ID)
	if err != nil || saved.RuntimeID != "primary" || saved.CurrentDeployID != previous.ID {
		t.Errorf("admission failure changed owner: %+v,%v", saved, err)
	}
	workflows, err := s.store.ListWorkflows()
	if err != nil || len(workflows) != 1 || workflows[0].ID != "reserved-restore" {
		t.Errorf("conflict left a new workflow: %+v,%v", workflows, err)
	}
}
