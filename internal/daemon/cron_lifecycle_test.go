package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
)

// Capture-only runtime: no process, container, socket, mount, or backend fallback.
type cronLifecycleRuntime struct {
	runtime.RuntimeBackend
	mu             sync.Mutex
	calls          []string
	users          []string
	createEntered  chan struct{}
	createRelease  chan struct{}
	createOnce     sync.Once
	inspectEntered chan struct{}
	inspectRelease chan struct{}
	inspectOnce    sync.Once
}

func (r *cronLifecycleRuntime) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *cronLifecycleRuntime) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...), append([]string(nil), r.users...)
}

func (r *cronLifecycleRuntime) CreateContainer(ctx context.Context, cfg *api.ServiceConfig, _ string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, "create")
	r.users = append(r.users, cfg.User)
	id := fmt.Sprintf("mock-cron-task-%d", len(r.users))
	r.mu.Unlock()
	if r.createEntered != nil {
		r.createOnce.Do(func() { close(r.createEntered) })
		select {
		case <-r.createRelease:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return id, nil
}

func (r *cronLifecycleRuntime) StartContainer(context.Context, string) error {
	r.record("start")
	return nil
}
func (r *cronLifecycleRuntime) StopContainer(context.Context, string) error {
	r.record("stop")
	return nil
}
func (r *cronLifecycleRuntime) RestartContainer(context.Context, string) error {
	r.record("restart")
	return nil
}
func (r *cronLifecycleRuntime) RemoveContainer(context.Context, string) error {
	r.record("remove")
	return nil
}
func (r *cronLifecycleRuntime) InspectContainer(ctx context.Context, id string) (*runtime.ContainerInfo, error) {
	r.record("inspect")
	if r.inspectEntered != nil {
		r.inspectOnce.Do(func() { close(r.inspectEntered) })
		select {
		case <-r.inspectRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	exitCode := 0
	return &runtime.ContainerInfo{ID: id, State: runtime.StateStopped, ExitCode: &exitCode}, nil
}
func (r *cronLifecycleRuntime) StreamLogs(context.Context, string, bool, int) (io.ReadCloser, error) {
	r.record("logs")
	return io.NopCloser(strings.NewReader("mock cron completed")), nil
}

func cronLifecycleFixture(t *testing.T, kind string) (*Server, *api.Service, *api.CronJob, *cronLifecycleRuntime) {
	t.Helper()
	s, st, cleanup := setupDaemonStore(t)
	t.Cleanup(cleanup)
	svc := &api.Service{ID: "cron-lifecycle", Name: "cron-lifecycle", Kind: kind, CurrentDeployID: "cron-deploy-1234", DesiredState: "active", ActualState: "active"}
	if kind != "cron" {
		svc.DesiredState, svc.ActualState = "running", "running"
		svc.RuntimeID = "ordinary-container"
	}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	deploy := api.Deploy{ID: svc.CurrentDeployID, ServiceID: svc.ID, Status: "success"}
	if err := st.CreateDeploy(&deploy); err != nil {
		t.Fatal(err)
	}
	cfg := api.ServiceConfig{Name: svc.Name, Kind: kind, Image: "mock-image", User: "1001:1002", Schedule: "* * * * *", Run: "mock-scheduled-command"}
	encoded, err := json.Marshal(DeployInput{ServiceConfig: cfg, Deploy: deploy, Service: *svc})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.execDeploySaveConfigOnDisk(&duraflow.StepContext{Context: t.Context(), InputJSON: string(encoded), State: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	job := &api.CronJob{ID: "registered-cron-job", ServiceID: svc.ID, Name: svc.Name, Schedule: cfg.Schedule, Command: cfg.Run}
	if err := st.CreateCronJob(job); err != nil {
		t.Fatal(err)
	}
	rt := &cronLifecycleRuntime{}
	s.runtime = rt
	return s, svc, job, rt
}

func cronLifecycleRequest(s *Server, name, action string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := withChiURLParam(httptest.NewRequest(http.MethodPost, "/services/"+name+"/"+action, nil), "name", name)
	switch action {
	case "start":
		s.handleStartService(response, request)
	case "stop":
		s.handleStopService(response, request)
	case "restart":
		s.handleRestartService(response, request)
	default:
		panic("unexpected lifecycle test action")
	}
	return response
}

func requireCronState(t *testing.T, s *Server, svc *api.Service, state string) {
	t.Helper()
	saved, err := s.store.GetService(svc.ID)
	if err != nil || saved == nil || saved.DesiredState != state || saved.ActualState != state || saved.RuntimeID != "" {
		t.Fatalf("want cron state %q without RuntimeID; got %+v, %v", state, saved, err)
	}
}

func TestCronLifecycleStopGatesExecutionAndResume(t *testing.T) {
	for _, resume := range []string{"start", "restart"} {
		t.Run(resume, func(t *testing.T) {
			s, svc, job, rt := cronLifecycleFixture(t, "cron")
			scheduler := NewScheduler(s.store, rt, s.config.DataDir)
			if err := scheduler.ExecuteCronJob(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			before, users := rt.snapshot()
			if !reflect.DeepEqual(users, []string{"1001:1002"}) {
				t.Fatalf("initial task identity = %v", users)
			}
			response := cronLifecycleRequest(s, svc.Name, "stop")
			if response.Code != http.StatusOK {
				t.Fatal(response.Body.String())
			}
			requireCronState(t, s, svc, "stopped")
			// job was fetched before Stop, like a scheduler tick already queued.
			if err := scheduler.ExecuteCronJob(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			after, _ := rt.snapshot()
			if !reflect.DeepEqual(after, before) {
				t.Errorf("Stop failed to gate actual scheduler execution: before=%v after=%v", before, after)
			}
			runs, err := s.store.ListJobRunsByCronJob(job.ID)
			if err != nil || len(runs) != 1 {
				t.Errorf("stopped cron created run history: %d runs, %v", len(runs), err)
			}
			registered, err := s.store.GetCronJob(job.ID)
			if err != nil || registered == nil || registered.Schedule != job.Schedule || registered.Command != job.Command {
				t.Fatalf("Stop deleted/changed schedule: %+v, %v", registered, err)
			}
			before, _ = rt.snapshot()
			response = cronLifecycleRequest(s, svc.Name, resume)
			if response.Code != http.StatusOK {
				t.Fatalf("%s without RuntimeID: status=%d body=%s", resume, response.Code, response.Body.String())
			}
			requireCronState(t, s, svc, "active")
			after, _ = rt.snapshot()
			if !reflect.DeepEqual(after, before) {
				t.Errorf("cron %s made container lifecycle calls: %v", resume, after[len(before):])
			}
			if err := scheduler.ExecuteCronJob(t.Context(), registered); err != nil {
				t.Fatal(err)
			}
			_, users = rt.snapshot()
			if !reflect.DeepEqual(users, []string{"1001:1002", "1001:1002"}) {
				t.Errorf("expected exactly initial and resumed tasks, got users=%v", users)
			}
			runs, err = s.store.ListJobRunsByCronJob(job.ID)
			if err != nil || len(runs) != 2 {
				t.Fatalf("resumed run count=%d, %v", len(runs), err)
			}
			for _, run := range runs {
				if run.Status != "success" {
					t.Errorf("job did not finish: %+v", run)
				}
			}
			events, err := s.store.ListEvents(store.EventFilter{ServiceID: &svc.ID})
			if err != nil || len(events) != 2 {
				t.Fatalf("expected existing Stop/resume lifecycle events: %+v, %v", events, err)
			}
		})
	}
}

func TestCronLifecycleResumeRejectsUnusableSchedule(t *testing.T) {
	for _, action := range []string{"start", "restart"} {
		for _, fault := range []string{"deleted_schedule", "invalid_schedule", "empty_command", "missing_deploy", "missing_config", "invalid_config", "wrong_owner"} {
			t.Run(action+"/"+fault, func(t *testing.T) {
				s, svc, job, rt := cronLifecycleFixture(t, "cron")
				svc.DesiredState, svc.ActualState = "stopped", "stopped"
				cfgPath := filepath.Join(s.config.DataDir, "services", svc.Name, "deploy_"+svc.CurrentDeployID+".json")
				switch fault {
				case "deleted_schedule":
					if err := s.store.DeleteCronJob(job.ID); err != nil {
						t.Fatal(err)
					}
				case "invalid_schedule":
					job.Schedule = "not a cron expression"
				case "empty_command":
					job.Command = "  "
				case "missing_deploy":
					svc.CurrentDeployID = ""
				case "missing_config":
					if err := os.Remove(cfgPath); err != nil {
						t.Fatal(err)
					}
				case "invalid_config":
					if err := os.WriteFile(cfgPath, []byte("{invalid"), 0600); err != nil {
						t.Fatal(err)
					}
				case "wrong_owner":
					other := &api.Service{ID: "other-service", Name: "other-service"}
					if err := s.store.UpsertService(other); err != nil {
						t.Fatal(err)
					}
					job.ServiceID = other.ID
				}
				if fault == "invalid_schedule" || fault == "empty_command" || fault == "wrong_owner" {
					if err := s.store.UpsertCronJob(job); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.store.UpsertService(svc); err != nil {
					t.Fatal(err)
				}
				response := cronLifecycleRequest(s, svc.Name, action)
				if response.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(response.Body.String()), "cron") {
					t.Errorf("want explicit cron configuration error, got %d %s", response.Code, response.Body.String())
				}
				requireCronState(t, s, svc, "stopped")
				calls, _ := rt.snapshot()
				if len(calls) != 0 {
					t.Errorf("unusable schedule touched runtime: %v", calls)
				}
			})
		}
	}
}

func TestCronLifecycleStopLeavesRunningJobAlone(t *testing.T) {
	s, svc, job, rt := cronLifecycleFixture(t, "cron")
	rt.inspectEntered, rt.inspectRelease = make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	scheduler := NewScheduler(s.store, rt, s.config.DataDir)
	go func() { done <- scheduler.ExecuteCronJob(ctx, job) }()
	select {
	case <-rt.inspectEntered:
	case <-ctx.Done():
		t.Fatal("mock job never reached running wait")
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(rt.inspectRelease) }) }
	defer release()
	stopped := make(chan *httptest.ResponseRecorder, 1)
	go func() { stopped <- cronLifecycleRequest(s, svc.Name, "stop") }()
	select {
	case response := <-stopped:
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("Stop waited for running cron job to exit")
	}
	before, _ := rt.snapshot()
	if !reflect.DeepEqual(before, []string{"create", "start", "inspect"}) {
		t.Fatalf("Stop signalled running task: %v", before)
	}
	if err := scheduler.ExecuteCronJob(ctx, job); err != nil {
		t.Error(err)
	}
	after, _ := rt.snapshot()
	if !reflect.DeepEqual(after, before) {
		t.Errorf("future execution was not skipped: %v", after)
	}
	// Release only this test's simulated task; Stop did not terminate it.
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("running task did not complete naturally: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("mock scheduler did not exit")
	}
}

func TestCronLifecycleStopSerializesTaskAdmission(t *testing.T) {
	s, svc, job, rt := cronLifecycleFixture(t, "cron")
	rt.createEntered, rt.createRelease = make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	scheduler := NewScheduler(s.store, rt, s.config.DataDir)
	done := make(chan error, 1)
	go func() { done <- scheduler.ExecuteCronJob(ctx, job) }()
	select {
	case <-rt.createEntered:
	case <-ctx.Done():
		t.Fatal("task not admitted")
	}
	stopped := make(chan *httptest.ResponseRecorder, 1)
	go func() { stopped <- cronLifecycleRequest(s, svc.Name, "stop") }()
	var early *httptest.ResponseRecorder
	select {
	case early = <-stopped:
		t.Error("Stop returned before admitted task could start")
	case <-time.After(50 * time.Millisecond):
	}
	close(rt.createRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if early == nil {
		early = <-stopped
	}
	if early.Code != http.StatusOK {
		t.Fatal(early.Body.String())
	}
	before, _ := rt.snapshot()
	if err := scheduler.ExecuteCronJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	after, _ := rt.snapshot()
	if !reflect.DeepEqual(after, before) {
		t.Errorf("task admitted after completed Stop: %v", after)
	}
}

func TestCronLifecycleNonCronServicesUnchanged(t *testing.T) {
	for _, kind := range []string{"web", "worker"} {
		for _, action := range []string{"start", "stop", "restart", "start_without_runtime", "restart_without_runtime", "independent_cron"} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				s, svc, job, rt := cronLifecycleFixture(t, kind)
				if strings.HasSuffix(action, "_without_runtime") {
					svc.RuntimeID = ""
				}
				if action == "independent_cron" {
					svc.DesiredState, svc.ActualState = "stopped", "stopped"
				}
				if err := s.store.UpsertService(svc); err != nil {
					t.Fatal(err)
				}
				if action == "independent_cron" {
					if err := NewScheduler(s.store, rt, s.config.DataDir).ExecuteCronJob(t.Context(), job); err != nil {
						t.Fatal(err)
					}
					_, users := rt.snapshot()
					if !reflect.DeepEqual(users, []string{"1001:1002"}) {
						t.Errorf("independent cron was gated: %v", users)
					}
					return
				}
				response := cronLifecycleRequest(s, svc.Name, strings.Split(action, "_")[0])
				wantStatus := http.StatusOK
				wantCalls := map[string][]string{"start": {"inspect", "start"}, "stop": {"stop"}, "restart": {"restart"}, "start_without_runtime": nil, "restart_without_runtime": {"create", "start"}}[action]
				if action == "start_without_runtime" {
					wantStatus = http.StatusBadRequest
				}
				calls, _ := rt.snapshot()
				if response.Code != wantStatus || !reflect.DeepEqual(calls, wantCalls) {
					t.Fatalf("ordinary %s changed: status=%d calls=%v body=%s", action, response.Code, calls, response.Body.String())
				}
			})
		}
	}
}
