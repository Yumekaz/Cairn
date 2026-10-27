package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/events"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
)

// Capture-only runtime. Unimplemented calls cannot reach a real backend.
type DeleteSafetyRuntime struct {
	runtime.RuntimeBackend
	stopErr      error
	removeErr    error
	calls        []string
	containers   []*runtime.ContainerInfo
	listErr      error
	stopErrors   map[string]error
	removeErrors map[string]error
}

func (r *DeleteSafetyRuntime) ListContainers(context.Context) ([]*runtime.ContainerInfo, error) {
	return r.containers, r.listErr
}

func (r *DeleteSafetyRuntime) StopContainer(_ context.Context, id string) error {
	r.calls = append(r.calls, "stop:"+id)
	if err := r.stopErrors[id]; err != nil {
		return err
	}
	return r.stopErr
}

func (r *DeleteSafetyRuntime) RemoveContainer(_ context.Context, id string) error {
	r.calls = append(r.calls, "remove:"+id)
	if err := r.removeErrors[id]; err != nil {
		return err
	}
	return r.removeErr
}

func TestDeleteSafetyRuntimeInventoryControl(t *testing.T) {
	rt := &DeleteSafetyRuntime{}
	if inventory, err := rt.ListContainers(t.Context()); err != nil || inventory != nil {
		t.Fatalf("default fake inventory must be nil without a backend: %+v, %v", inventory, err)
	}
	rt.containers = []*runtime.ContainerInfo{{ID: "controlled-container", Name: "controlled-name"}}
	if inventory, err := rt.ListContainers(t.Context()); err != nil || !reflect.DeepEqual(inventory, rt.containers) {
		t.Fatalf("fake did not return controlled inventory: %+v, %v", inventory, err)
	}
	failure := errors.New("delete-safety inventory unavailable")
	rt.listErr = failure
	if _, err := rt.ListContainers(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("fake did not propagate controlled inventory error: %v", err)
	}
}

func TestDeleteSafetyInventoryErrorRetainsOwnership(t *testing.T) {
	s, svc, vol, rt := deleteSafetyFixture(t, "web")
	failure := errors.New("delete-safety owned inventory outcome uncertain")
	rt.listErr = failure
	ids, err := s.ownedServiceRuntimeIDs(t.Context(), svc)
	if !errors.Is(err, failure) || len(ids) != 0 {
		t.Errorf("uncertain inventory returned owned IDs: %v, %v", ids, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("failed inventory mutated runtime: %v", rt.calls)
	}
	requireDeleteSafetyOwnership(t, s, svc, vol)
}

func deleteSafetyFixture(t *testing.T, kind string) (*Server, *api.Service, *api.Volume, *DeleteSafetyRuntime) {
	t.Helper()
	s, st, cleanup := setupDaemonStore(t)
	t.Cleanup(cleanup)
	svc := &api.Service{
		ID: "delete-safety-service", Name: "delete-safety", Kind: kind,
		RuntimeBackend: "mini-docker", RuntimeID: "delete-safety-container",
		CurrentDeployID: "delete-safety-deploy", DesiredState: "running", ActualState: "running",
		Route: "http://delete-safety.invalid",
	}
	if kind == "cron" {
		svc.RuntimeID = ""
		svc.DesiredState, svc.ActualState = "active", "active"
	}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeploy(&api.Deploy{
		ID: svc.CurrentDeployID, ServiceID: svc.ID, Status: "success", Stage: "completed", HealthStatus: "healthy",
	}); err != nil {
		t.Fatal(err)
	}
	if kind == "cron" {
		if err := st.CreateCronJob(&api.CronJob{ID: "delete-safety-schedule", ServiceID: svc.ID, Name: svc.Name, Schedule: "* * * * *", Command: "fake-job"}); err != nil {
			t.Fatal(err)
		}
	}
	vol := &api.Volume{
		ID: "delete-safety-volume", Name: "delete-safety-data",
		HostPath:          filepath.Join(s.config.VolumeDir, "delete-safety-data"),
		AttachedServiceID: svc.ID, MountPath: "/data", Status: "active",
	}
	if err := st.UpsertVolume(vol); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vol.HostPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vol.HostPath, "sentinel"), []byte("preserve delete-safety data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Compare exact persisted snapshots, without time.Now's monotonic component.
	svc, err := st.GetService(svc.ID)
	if err != nil || svc == nil {
		t.Fatalf("snapshot service: %+v, %v", svc, err)
	}
	vol, err = st.GetVolume(vol.ID)
	if err != nil || vol == nil {
		t.Fatalf("snapshot volume: %+v, %v", vol, err)
	}
	rt := &DeleteSafetyRuntime{containers: []*runtime.ContainerInfo{{ID: svc.RuntimeID, Name: svc.RuntimeID}}, stopErrors: make(map[string]error), removeErrors: make(map[string]error)}
	s.runtime = rt
	return s, svc, vol, rt
}

func deleteSafetyRequest(s *Server, name string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := withChiURLParam(httptest.NewRequest(http.MethodDelete, "/services/"+name, nil), "name", name)
	s.handleRemoveService(response, request)
	return response
}

func requireDeleteSafetyData(t *testing.T, vol *api.Volume) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vol.HostPath, "sentinel"))
	if err != nil || string(data) != "preserve delete-safety data\n" {
		t.Errorf("volume data changed: %q, %v", data, err)
	}
}

func requireDeleteSafetyOwnership(t *testing.T, s *Server, svc *api.Service, vol *api.Volume) {
	t.Helper()
	saved, err := s.store.GetService(svc.ID)
	if err != nil || saved == nil || !reflect.DeepEqual(saved, svc) {
		t.Errorf("service ownership or state changed: %+v, %v; want %+v", saved, err, svc)
	}
	deploy, err := s.store.GetDeploy(svc.CurrentDeployID)
	if err != nil || deploy == nil || deploy.ServiceID != svc.ID || deploy.Status != "success" {
		t.Errorf("deploy ownership lost: %+v, %v", deploy, err)
	}
	if svc.Kind == "cron" {
		job, err := s.store.GetCronJobByName(svc.Name)
		if err != nil || job == nil || job.ServiceID != svc.ID {
			t.Errorf("cron schedule ownership lost: %+v, %v", job, err)
		}
	}
	savedVol, err := s.store.GetVolume(vol.ID)
	if err != nil || savedVol == nil || !reflect.DeepEqual(savedVol, vol) {
		t.Errorf("volume ownership changed: %+v, %v; want %+v", savedVol, err, vol)
	}
	requireDeleteSafetyData(t, vol)
	evts, err := s.store.ListEvents(store.EventFilter{ServiceID: &svc.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, evt := range evts {
		if evt.Type == events.ServiceRemoved.String() {
			t.Errorf("failed deletion emitted ServiceRemoved: %+v", evt)
		}
	}
}

func TestDeleteSafetyRuntimeFailureRetainsOwnership(t *testing.T) {
	for _, phase := range []string{"stop", "remove"} {
		t.Run(phase, func(t *testing.T) {
			s, svc, vol, rt := deleteSafetyFixture(t, "web")
			failure := errors.New("delete-safety " + phase + " outcome uncertain")
			wantCalls := []string{"stop:" + svc.RuntimeID}
			if phase == "stop" {
				rt.stopErr = failure
			} else {
				rt.removeErr = failure
				wantCalls = append(wantCalls, "remove:"+svc.RuntimeID)
			}
			response := deleteSafetyRequest(s, svc.Name)
			if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), failure.Error()) {
				t.Errorf("want explicit runtime failure, got %d %s", response.Code, response.Body.String())
			}
			if !reflect.DeepEqual(rt.calls, wantCalls) {
				t.Errorf("runtime calls = %v, want %v", rt.calls, wantCalls)
			}
			requireDeleteSafetyOwnership(t, s, svc, vol)
		})
	}
}

func TestDeleteSafetySuccessPreservesVolumeData(t *testing.T) {
	for _, kind := range []string{"web", "cron", "undeployed"} {
		t.Run(kind, func(t *testing.T) {
			s, svc, vol, rt := deleteSafetyFixture(t, kind)
			if kind == "undeployed" {
				svc.RuntimeID = ""
				if err := s.store.UpsertService(svc); err != nil {
					t.Fatal(err)
				}
			}
			response := deleteSafetyRequest(s, svc.Name)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "service removed") {
				t.Fatalf("delete failed: %d %s", response.Code, response.Body.String())
			}
			var wantCalls []string
			if svc.RuntimeID != "" {
				wantCalls = []string{"stop:" + svc.RuntimeID, "remove:" + svc.RuntimeID}
			}
			if !reflect.DeepEqual(rt.calls, wantCalls) {
				t.Errorf("runtime calls = %v, want %v", rt.calls, wantCalls)
			}
			if saved, err := s.store.GetService(svc.ID); err != nil || saved != nil {
				t.Errorf("service not deleted: %+v, %v", saved, err)
			}
			if deploy, err := s.store.GetDeploy(svc.CurrentDeployID); err != nil || deploy != nil {
				t.Errorf("deploy not deleted: %+v, %v", deploy, err)
			}
			if job, err := s.store.GetCronJobByName(svc.Name); err != nil || job != nil {
				t.Errorf("cron schedule not deleted: %+v, %v", job, err)
			}
			if savedVol, err := s.store.GetVolume(vol.ID); err != nil || savedVol == nil || savedVol.AttachedServiceID != "" || savedVol.HostPath != vol.HostPath {
				t.Errorf("volume not preserved and detached: %+v, %v", savedVol, err)
			}
			requireDeleteSafetyData(t, vol)
			evts, err := s.store.ListEvents(store.EventFilter{ServiceID: &svc.ID})
			if err != nil {
				t.Fatal(err)
			}
			if len(evts) != 1 || evts[0].Type != events.ServiceRemoved.String() {
				t.Errorf("want one successful removal event, got %+v", evts)
			}
		})
	}
}

func TestDeleteSafetyUnfinishedJobsConflict(t *testing.T) {
	for _, kind := range []string{"web", "cron"} {
		for _, jobType := range []string{"cron", "one-off"} {
			for _, state := range []string{"running", "pending", "unknown", "failed-unverified", "failed-unknown-exit", "success-unfinished"} {
				t.Run(kind+"/"+jobType+"/"+state, func(t *testing.T) {
					s, svc, vol, rt := deleteSafetyFixture(t, kind)
					job := &api.JobRun{ID: "delete-safety-job", ServiceID: svc.ID, Type: jobType, Name: "delete-safety-job", Status: state}
					if state == "failed-unverified" {
						now := time.Now()
						job.Status, job.FinishedAt, job.FailureReason = "failed", &now, "Execution error: inspection unavailable"
					}
					if state == "failed-unknown-exit" {
						now, code := time.Now(), -1
						job.Status, job.FinishedAt, job.ExitCode = "failed", &now, &code
					}
					if state == "success-unfinished" {
						code := 0
						job.Status, job.ExitCode = "success", &code
					}
					if err := s.store.CreateJobRun(job); err != nil {
						t.Fatal(err)
					}
					job, err := s.store.GetJobRun(job.ID)
					if err != nil || job == nil {
						t.Fatalf("snapshot job: %+v, %v", job, err)
					}
					response := deleteSafetyRequest(s, svc.Name)
					if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "job") {
						t.Errorf("want explicit job conflict, got %d %s", response.Code, response.Body.String())
					}
					if len(rt.calls) != 0 {
						t.Errorf("unfinished job caused runtime mutation: %v", rt.calls)
					}
					requireDeleteSafetyOwnership(t, s, svc, vol)
					if saved, err := s.store.GetJobRun(job.ID); err != nil || saved == nil || !reflect.DeepEqual(saved, job) {
						t.Errorf("job ownership or status changed: %+v, %v", saved, err)
					}
				})
			}
		}
	}
}

func TestDeleteSafetyHistoricalWritersAndPartialRetry(t *testing.T) {
	for _, phase := range []string{"stop", "remove", "retry"} {
		t.Run(phase, func(t *testing.T) {
			s, svc, vol, rt := deleteSafetyFixture(t, "web")
			dep := &api.Deploy{ID: "12345678-historical", ServiceID: svc.ID, Status: "failed", Stage: "failed"}
			if err := s.store.CreateDeploy(dep); err != nil {
				t.Fatal(err)
			}
			rt.containers = append(rt.containers, &runtime.ContainerInfo{ID: "old-writer", Name: "cairn-" + svc.Name + "-12345678", State: runtime.StateError}, &runtime.ContainerInfo{ID: "unrelated", Name: "cairn-" + svc.Name + "-87654321", State: runtime.StateRunning})
			if phase == "stop" {
				rt.stopErrors["old-writer"] = errors.New("old writer unverified")
			}
			if phase == "remove" {
				rt.removeErrors["old-writer"] = errors.New("old cleanup failed")
			}
			if phase == "retry" {
				rt.removeErrors[svc.RuntimeID] = errors.New("primary cleanup failed")
			}
			response := deleteSafetyRequest(s, svc.Name)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("uncertain cleanup acknowledged deletion: %d %s", response.Code, response.Body.String())
			}
			requireDeleteSafetyOwnership(t, s, svc, vol)
			for _, call := range rt.calls {
				if strings.Contains(call, "unrelated") {
					t.Errorf("touched unrelated container: %v", rt.calls)
				}
			}
			want := []string{"stop:" + svc.RuntimeID, "stop:old-writer"}
			if phase != "stop" {
				want = append(want, "remove:old-writer")
			}
			if phase == "retry" {
				want = append(want, "remove:"+svc.RuntimeID)
			}
			if !reflect.DeepEqual(want, rt.calls) {
				t.Errorf("cleanup ordering=%v,want%v", rt.calls, want)
			}
			if phase == "retry" {
				// Historical Remove completed before primary Remove failed. Its
				// confirmed absence must not make a subsequent retry impossible.
				rt.containers = rt.containers[:1]
				rt.calls = nil
				delete(rt.removeErrors, svc.RuntimeID)
				response = deleteSafetyRequest(s, svc.Name)
				if response.Code != http.StatusOK {
					t.Fatalf("partial cleanup retry failed: %d %s", response.Code, response.Body.String())
				}
				if !reflect.DeepEqual(rt.calls, []string{"stop:" + svc.RuntimeID, "remove:" + svc.RuntimeID}) {
					t.Errorf("retried absent old runtime: %v", rt.calls)
				}
				requireDeleteSafetyData(t, vol)
			}
		})
	}
}

func TestDeleteSafetyConfirmedRuntimeAbsenceAllowsReplay(t *testing.T) {
	s, svc, vol, rt := deleteSafetyFixture(t, "web")
	rt.containers = nil
	response := deleteSafetyRequest(s, svc.Name)
	if response.Code != http.StatusOK || len(rt.calls) != 0 {
		t.Fatalf("confirmed completed removal could not replay: %d,%v", response.Code, rt.calls)
	}
	requireDeleteSafetyData(t, vol)
}

func TestDeleteSafetyActiveWorkflowKeepsOwnership(t *testing.T) {
	for _, kind := range []string{"deploy", "restore"} {
		t.Run(kind, func(t *testing.T) {
			s, svc, vol, rt := deleteSafetyFixture(t, "web")
			if kind == "deploy" {
				if err := s.store.CreateDeploy(&api.Deploy{ID: "12345678-active", ServiceID: svc.ID, Status: "pending", Stage: "starting"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.store.CreateWorkflow(&api.Workflow{ID: "reserved-restore", Type: "restore", Status: "pending"}); err != nil {
					t.Fatal(err)
				}
			}
			response := deleteSafetyRequest(s, svc.Name)
			if response.Code != http.StatusConflict || len(rt.calls) != 0 {
				t.Errorf("active work lost ownership: %d,%v", response.Code, rt.calls)
			}
			requireDeleteSafetyOwnership(t, s, svc, vol)
		})
	}
}

func TestDeleteSafetyCompletedJobsAllowDeletion(t *testing.T) {
	for _, jobType := range []string{"cron", "one-off"} {
		for _, status := range []string{"success", "failed"} {
			t.Run(jobType+"/"+status, func(t *testing.T) {
				s, svc, vol, rt := deleteSafetyFixture(t, "cron")
				now := time.Now()
				code := 0
				if status == "failed" {
					code = 1
				}
				job := &api.JobRun{ID: "delete-safety-job", ServiceID: svc.ID, Type: jobType, Name: "delete-safety-job", Status: status, ExitCode: &code, FinishedAt: &now}
				if err := s.store.CreateJobRun(job); err != nil {
					t.Fatal(err)
				}
				response := deleteSafetyRequest(s, svc.Name)
				if response.Code != http.StatusOK {
					t.Fatalf("completed job blocked deletion: %d %s", response.Code, response.Body.String())
				}
				if len(rt.calls) != 0 {
					t.Errorf("cron deletion tried to kill task containers: %v", rt.calls)
				}
				if saved, err := s.store.GetJobRun(job.ID); err != nil || saved != nil {
					t.Errorf("completed job did not cascade: %+v, %v", saved, err)
				}
				requireDeleteSafetyData(t, vol)
			})
		}
	}
}
