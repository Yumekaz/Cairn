package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
)

// Channel-controlled runtime only; every lifecycle operation is simulated.
type DeleteSafetyAdmissionRuntime struct {
	runtime.RuntimeBackend
	store                                              *store.Store
	serviceID                                          string
	mu                                                 sync.Mutex
	calls                                              []string
	created                                            chan struct{}
	starting                                           chan struct{}
	startGate                                          chan struct{}
	waiting                                            chan struct{}
	waitGate                                           chan struct{}
	stopping                                           chan struct{}
	stopGate                                           chan struct{}
	createOnce, startOnce, waitOnce, stopOnce          sync.Once
	startReleaseOnce, waitReleaseOnce, stopReleaseOnce sync.Once
}

func (r *DeleteSafetyAdmissionRuntime) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *DeleteSafetyAdmissionRuntime) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *DeleteSafetyAdmissionRuntime) releaseAll() {
	r.releaseStart()
	r.releaseWait()
	r.stopReleaseOnce.Do(func() { close(r.stopGate) })
}

func (r *DeleteSafetyAdmissionRuntime) releaseStart() {
	r.startReleaseOnce.Do(func() { close(r.startGate) })
}

func (r *DeleteSafetyAdmissionRuntime) releaseWait() {
	r.waitReleaseOnce.Do(func() { close(r.waitGate) })
}

func deleteSafetyGate(ctx context.Context, gate <-chan struct{}) error {
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *DeleteSafetyAdmissionRuntime) CreateContainer(_ context.Context, _ *api.ServiceConfig, _ string) (string, error) {
	r.record("create")
	r.createOnce.Do(func() { close(r.created) })
	jobs, err := r.store.ListJobRunsByService(r.serviceID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "running" {
		return "", fmt.Errorf("writer exposed without persisted running job: %+v, %v", jobs, err)
	}
	return "delete-safety-writer", nil
}

func (r *DeleteSafetyAdmissionRuntime) StartContainer(ctx context.Context, id string) error {
	r.record("start:" + id)
	r.startOnce.Do(func() { close(r.starting) })
	return deleteSafetyGate(ctx, r.startGate)
}

func (r *DeleteSafetyAdmissionRuntime) InspectContainer(ctx context.Context, id string) (*runtime.ContainerInfo, error) {
	r.record("inspect:" + id)
	r.waitOnce.Do(func() { close(r.waiting) })
	if err := deleteSafetyGate(ctx, r.waitGate); err != nil {
		return nil, err
	}
	code := 0
	return &runtime.ContainerInfo{ID: id, State: runtime.StateStopped, ExitCode: &code}, nil
}

func (r *DeleteSafetyAdmissionRuntime) StreamLogs(context.Context, string, bool, int) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("simulated writer output")), nil
}

func (r *DeleteSafetyAdmissionRuntime) StopContainer(ctx context.Context, id string) error {
	r.record("stop:" + id)
	r.stopOnce.Do(func() { close(r.stopping) })
	return deleteSafetyGate(ctx, r.stopGate)
}

func (r *DeleteSafetyAdmissionRuntime) RemoveContainer(_ context.Context, id string) error {
	r.record("remove:" + id)
	return nil
}

func (r *DeleteSafetyAdmissionRuntime) ListContainers(context.Context) ([]*runtime.ContainerInfo, error) {
	svc, err := r.store.GetService(r.serviceID)
	if err != nil {
		return nil, err
	}
	if svc == nil || svc.RuntimeID == "" {
		return nil, nil
	}
	return []*runtime.ContainerInfo{{ID: svc.RuntimeID, Name: svc.RuntimeID, State: runtime.StateRunning}}, nil
}

func deleteSafetyAdmissionFixture(t *testing.T, kind string) (*Server, *api.Service, *api.Volume, *api.CronJob, *DeleteSafetyAdmissionRuntime) {
	t.Helper()
	s, svc, vol, _ := deleteSafetyFixture(t, kind)
	cfg := api.ServiceConfig{Name: svc.Name, Kind: kind, Image: "fake-image", Volumes: []api.VolumeConfig{{Name: vol.Name, MountPath: vol.MountPath}}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(s.config.DataDir, "services", svc.Name, "deploy_"+svc.CurrentDeployID+".json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	job, err := s.store.GetCronJobByName(svc.Name)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil {
		job = &api.CronJob{ID: "delete-safety-schedule", ServiceID: svc.ID, Name: svc.Name, Schedule: "* * * * *", Command: "fake-writer"}
		if err := s.store.CreateCronJob(job); err != nil {
			t.Fatal(err)
		}
	}
	rt := &DeleteSafetyAdmissionRuntime{
		store: s.store, serviceID: svc.ID,
		created: make(chan struct{}), starting: make(chan struct{}), startGate: make(chan struct{}),
		waiting: make(chan struct{}), waitGate: make(chan struct{}), stopping: make(chan struct{}), stopGate: make(chan struct{}),
	}
	s.runtime = rt
	t.Cleanup(rt.releaseAll)
	return s, svc, vol, job, rt
}

type DeleteSafetyAdmissionRun struct {
	attempted chan struct{}
	done      chan struct{}
	status    int
	body      string
	err       error
}

func deleteSafetyLaunch(t *testing.T, rt *DeleteSafetyAdmissionRuntime, f func(context.Context, *DeleteSafetyAdmissionRun)) *DeleteSafetyAdmissionRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	run := &DeleteSafetyAdmissionRun{attempted: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(run.done)
		close(run.attempted)
		f(ctx, run)
	}()
	t.Cleanup(func() {
		rt.releaseAll()
		cancel()
		select {
		case <-run.done:
		case <-time.After(time.Second):
			t.Error("simulated operation leaked past cleanup")
		}
	})
	return run
}

func deleteSafetyLaunchWriter(t *testing.T, path string, s *Server, svc *api.Service, job *api.CronJob, rt *DeleteSafetyAdmissionRuntime) *DeleteSafetyAdmissionRun {
	t.Helper()
	return deleteSafetyLaunch(t, rt, func(ctx context.Context, run *DeleteSafetyAdmissionRun) {
		if path == "scheduled" {
			run.err = NewScheduler(s.store, rt, s.config.DataDir).ExecuteCronJob(ctx, job)
			return
		}
		request := withChiURLParam(httptest.NewRequest(http.MethodPost, "/services/"+svc.Name+"/run", strings.NewReader(`{"command":"fake-writer"}`)).WithContext(ctx), "name", svc.Name)
		response := httptest.NewRecorder()
		s.handleRunOneOff(response, request)
		run.status, run.body = response.Code, response.Body.String()
	})
}

func deleteSafetyLaunchDelete(t *testing.T, s *Server, svc *api.Service, rt *DeleteSafetyAdmissionRuntime) *DeleteSafetyAdmissionRun {
	t.Helper()
	return deleteSafetyLaunch(t, rt, func(ctx context.Context, run *DeleteSafetyAdmissionRun) {
		request := withChiURLParam(httptest.NewRequest(http.MethodDelete, "/services/"+svc.Name, nil).WithContext(ctx), "name", svc.Name)
		response := httptest.NewRecorder()
		s.handleRemoveService(response, request)
		run.status, run.body = response.Code, response.Body.String()
	})
}

func deleteSafetyAwait(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func requireDeleteSafetyNoJobAdmission(t *testing.T, s *Server, svc *api.Service, rt *DeleteSafetyAdmissionRuntime) {
	t.Helper()
	if calls := rt.snapshot(); len(calls) != 0 {
		t.Errorf("blocked writer reached runtime: %v", calls)
	}
	if jobs, err := s.store.ListJobRunsByService(svc.ID); err != nil || len(jobs) != 0 {
		t.Errorf("blocked writer recorded job: %+v, %v", jobs, err)
	}
}

func TestDeleteSafetyAdmissionDeletionFirst(t *testing.T) {
	for _, path := range []string{"oneoff", "scheduled"} {
		for _, kind := range []string{"web", "worker"} {
			t.Run(path+"/"+kind, func(t *testing.T) {
				s, svc, vol, job, rt := deleteSafetyAdmissionFixture(t, kind)
				deletion := deleteSafetyLaunchDelete(t, s, svc, rt)
				deleteSafetyAwait(t, rt.stopping, "delete entering Stop")
				writer := deleteSafetyLaunchWriter(t, path, s, svc, job, rt)
				deleteSafetyAwait(t, writer.attempted, "racing writer attempt")
				select {
				case <-rt.created:
					t.Error("writer entered Create while deletion was paused in Stop")
					t.Logf("failing-before race proof: deletion holds Stop; writer calls=%v", rt.snapshot())
				case <-time.After(50 * time.Millisecond):
				}
				if jobs, err := s.store.ListJobRunsByService(svc.ID); err != nil || len(jobs) != 0 {
					t.Errorf("writer admitted before deletion released lifecycle: %+v, %v", jobs, err)
				}
				rt.releaseAll()
				deleteSafetyAwait(t, deletion.done, "deletion completion")
				deleteSafetyAwait(t, writer.done, "queued writer rejection")
				if deletion.status != http.StatusOK {
					t.Errorf("delete failed: %d %s", deletion.status, deletion.body)
				}
				if path == "oneoff" && (writer.status != http.StatusNotFound || !strings.Contains(writer.body, "service not found")) {
					t.Errorf("queued oneoff not rejected as deleted: %d %s", writer.status, writer.body)
				}
				if path == "scheduled" && (writer.err == nil || !strings.Contains(writer.err.Error(), "service not found")) {
					t.Errorf("queued schedule not rejected as deleted: %v", writer.err)
				}
				requireDeleteSafetyData(t, vol)
			})
		}
	}
}

func TestDeleteSafetyAdmissionWriterFirst(t *testing.T) {
	for _, path := range []string{"oneoff", "scheduled"} {
		for _, kind := range []string{"web", "worker", "cron"} {
			t.Run(path+"/"+kind, func(t *testing.T) {
				s, svc, vol, job, rt := deleteSafetyAdmissionFixture(t, kind)
				writer := deleteSafetyLaunchWriter(t, path, s, svc, job, rt)
				deleteSafetyAwait(t, rt.starting, "writer Start acknowledgement barrier")
				jobs, err := s.store.ListJobRunsByService(svc.ID)
				if err != nil || len(jobs) != 1 || jobs[0].Status != "running" {
					t.Fatalf("no durable job before Start: %+v, %v", jobs, err)
				}
				deletion := deleteSafetyLaunchDelete(t, s, svc, rt)
				deleteSafetyAwait(t, deletion.attempted, "delete attempt")
				select {
				case <-deletion.done:
					t.Error("deletion returned before admitted writer Start acknowledged")
				case <-time.After(50 * time.Millisecond):
				}
				// Release just admission. Keep simulated execution waiting for exit.
				rt.releaseStart()
				deleteSafetyAwait(t, rt.waiting, "writer execution wait")
				deleteSafetyAwait(t, deletion.done, "delete conflict during active job")
				if deletion.status != http.StatusConflict {
					t.Errorf("delete did not retain admitted writer ownership: %d %s", deletion.status, deletion.body)
				}
				select {
				case <-writer.done:
					t.Error("writer finished before simulated exit was released")
				default:
				}
				for _, call := range rt.snapshot() {
					if strings.HasPrefix(call, "stop:") || strings.HasPrefix(call, "remove:") {
						t.Errorf("conflict signalled active writer: %s", call)
					}
				}
				requireDeleteSafetyOwnership(t, s, svc, vol)
				// lifecycle itself must also be free, not only the admission mutex.
				if !s.lifecycleMu.TryLock() {
					t.Error("writer held lifecycle during execution wait")
				} else {
					s.lifecycleMu.Unlock()
				}
				rt.releaseWait()
				deleteSafetyAwait(t, writer.done, "natural writer completion")
				if path == "oneoff" && writer.status != http.StatusOK {
					t.Errorf("oneoff failed: %d %s", writer.status, writer.body)
				}
				if writer.err != nil {
					t.Errorf("scheduled writer failed: %v", writer.err)
				}
				rt.releaseAll()
				response := deleteSafetyRequest(s, svc.Name)
				if response.Code != http.StatusOK {
					t.Errorf("completed writer blocked deletion: %d %s", response.Code, response.Body.String())
				}
			})
		}
	}
}

func TestDeleteSafetyAdmissionRestoreReservation(t *testing.T) {
	for _, path := range []string{"oneoff", "scheduled"} {
		for _, kind := range []string{"web", "cron"} {
			for _, status := range []string{"pending", "running", "success", "failed"} {
				t.Run(path+"/"+kind+"/"+status, func(t *testing.T) {
					s, svc, vol, job, rt := deleteSafetyAdmissionFixture(t, kind)
					workflow := &api.Workflow{ID: "delete-safety-restore", Type: "restore", Status: status, InputJSON: "unverified-input"}
					if err := s.store.CreateWorkflow(workflow); err != nil {
						t.Fatal(err)
					}
					rt.releaseAll()
					writer := deleteSafetyLaunchWriter(t, path, s, svc, job, rt)
					deleteSafetyAwait(t, writer.done, "restore admission decision")
					active := status == "pending" || status == "running"
					if active {
						if path == "oneoff" && (writer.status != http.StatusConflict || !strings.Contains(writer.body, "restore")) {
							t.Errorf("restore did not conflict with oneoff: %d %s", writer.status, writer.body)
						}
						if path == "scheduled" && (writer.err == nil || !strings.Contains(writer.err.Error(), "restore")) {
							t.Errorf("restore did not block schedule: %v", writer.err)
						}
						requireDeleteSafetyNoJobAdmission(t, s, svc, rt)
					} else if (path == "oneoff" && writer.status != http.StatusOK) || writer.err != nil {
						t.Errorf("terminal restore blocked writer: %d %s %v", writer.status, writer.body, writer.err)
					}
					requireDeleteSafetyData(t, vol)
				})
			}
		}
	}
}

func TestDeleteSafetyAdmissionRestoreQueryFailure(t *testing.T) {
	for _, path := range []string{"oneoff", "scheduled"} {
		t.Run(path, func(t *testing.T) {
			s, svc, _, job, rt := deleteSafetyAdmissionFixture(t, "web")
			// Fault only this disposable fixture's restore inventory, not service lookup.
			db, err := sql.Open("sqlite", filepath.Join(s.config.DataDir, "cairn.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec("DROP TABLE duraflow_workflows"); err != nil {
				t.Fatal(err)
			}
			rt.releaseAll()
			writer := deleteSafetyLaunchWriter(t, path, s, svc, job, rt)
			deleteSafetyAwait(t, writer.done, "unavailable restore inventory rejection")
			if path == "oneoff" && (writer.status != http.StatusInternalServerError || !strings.Contains(writer.body, "restore")) {
				t.Errorf("unknown restore ownership admitted oneoff: %d %s", writer.status, writer.body)
			}
			if path == "scheduled" && (writer.err == nil || !strings.Contains(writer.err.Error(), "restore")) {
				t.Errorf("unknown restore ownership admitted schedule: %v", writer.err)
			}
			requireDeleteSafetyNoJobAdmission(t, s, svc, rt)
		})
	}
}

func TestDeleteSafetyAdmissionRechecksQueuedRestore(t *testing.T) {
	for _, path := range []string{"oneoff", "scheduled"} {
		for _, kind := range []string{"web", "cron"} {
			t.Run(path+"/"+kind, func(t *testing.T) {
				s, svc, _, job, rt := deleteSafetyAdmissionFixture(t, kind)
				rt.releaseAll()
				// Same order as physical restore/lifecycle: never admission then lifecycle.
				s.lifecycleMu.Lock()
				cronTaskAdmissionMu.Lock()
				var unlock sync.Once
				release := func() {
					unlock.Do(func() {
						cronTaskAdmissionMu.Unlock()
						s.lifecycleMu.Unlock()
					})
				}
				defer release()
				writer := deleteSafetyLaunchWriter(t, path, s, svc, job, rt)
				deleteSafetyAwait(t, writer.attempted, "writer queued behind restore barrier")
				select {
				case <-rt.created:
					t.Error("writer escaped held physical-restore admission barrier")
				case <-time.After(50 * time.Millisecond):
				}
				requireDeleteSafetyNoJobAdmission(t, s, svc, rt)
				if err := s.store.CreateWorkflow(&api.Workflow{ID: "delete-safety-queued-restore", Type: "restore", Status: "pending"}); err != nil {
					t.Fatal(err)
				}
				release()
				deleteSafetyAwait(t, writer.done, "queued restore reservation recheck")
				if path == "oneoff" && (writer.status != http.StatusConflict || !strings.Contains(writer.body, "restore")) {
					t.Errorf("queued oneoff missed restore reservation: %d %s", writer.status, writer.body)
				}
				if path == "scheduled" && (writer.err == nil || !strings.Contains(writer.err.Error(), "restore")) {
					t.Errorf("queued schedule missed restore reservation: %v", writer.err)
				}
				requireDeleteSafetyNoJobAdmission(t, s, svc, rt)
			})
		}
	}
}
