package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/runtime"
)

type restoringRuntime struct {
	runtime.RuntimeBackend
	running atomic.Bool
	starts  atomic.Int32
}

func (r *restoringRuntime) StopContainer(context.Context, string) error {
	r.running.Store(false)
	return nil
}
func (r *restoringRuntime) StartContainer(context.Context, string) error {
	r.starts.Add(1)
	r.running.Store(true)
	return nil
}
func (r *restoringRuntime) InspectContainer(context.Context, string) (*runtime.ContainerInfo, error) {
	state := runtime.StateStopped
	if r.running.Load() {
		state = runtime.StateRunning
	}
	return &runtime.ContainerInfo{State: state}, nil
}
func (r *restoringRuntime) ListContainers(context.Context) ([]*runtime.ContainerInfo, error) {
	return nil, nil
}

func TestReconciliationDoesNotRestartBetweenDurableRestoreSteps(t *testing.T) {
	for _, status := range []string{"pending", "running"} {
		t.Run(status, func(t *testing.T) {
			s, st, cleanup := setupDaemonStore(t)
			defer cleanup()
			r := &restoringRuntime{}
			r.running.Store(true)
			s.runtime = r
			svc := &api.Service{ID: "restore-service", Name: "restore-race", Kind: "web", RuntimeID: "same-container", DesiredState: "running", ActualState: "running", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := st.UpsertService(svc); err != nil {
				t.Fatal(err)
			}
			vol := &api.Volume{ID: "restore-volume", Name: "restore-data", HostPath: filepath.Join(s.config.VolumeDir, "restore-data"), AttachedServiceID: svc.ID, MountPath: "/data", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := st.UpsertVolume(vol); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(vol.HostPath, 0700); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(vol.HostPath, "journal")
			if err := os.WriteFile(journal, []byte("selected snapshot\n"), 0600); err != nil {
				t.Fatal(err)
			}
			backup, err := s.performVolumeBackup(vol)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(journal, []byte("later write\n"), 0600); err != nil {
				t.Fatal(err)
			}
			input, _ := json.Marshal(RestoreInput{VolumeName: vol.Name, BackupID: backup.ID})
			workflow := &api.Workflow{ID: "durable-restore", Type: "restore", Status: status, InputJSON: string(input)}
			if err := st.CreateWorkflow(workflow); err != nil {
				t.Fatal(err)
			}
			ctx := &duraflow.StepContext{Context: context.Background(), WorkflowID: workflow.ID, InputJSON: string(input), State: map[string]string{}}
			if err := s.execRestoreStopContainer(ctx); err != nil {
				t.Fatal(err)
			}
			s.ReconcileServices(context.Background())
			if r.starts.Load() != 0 || r.running.Load() {
				t.Fatal("reconciliation restarted the intentionally stopped restore container")
			}
			if err := s.execRestoreVerifyAndExtract(ctx); err != nil {
				t.Fatal(err)
			}
			s.ReconcileServices(context.Background())
			if r.starts.Load() != 0 || r.running.Load() {
				t.Fatal("reconciliation entered the extraction/restart gap")
			}
			data, err := os.ReadFile(journal)
			if err != nil || string(data) != "selected snapshot\n" {
				t.Fatalf("selected snapshot not restored: %q %v", data, err)
			}
			if err := s.execRestoreStartContainer(ctx); err != nil {
				t.Fatal(err)
			}
			workflow.Status = "success"
			if err := st.UpdateWorkflow(workflow); err != nil {
				t.Fatal(err)
			}
			s.ReconcileServices(context.Background())
			if r.starts.Load() != 1 || !r.running.Load() {
				t.Fatalf("restore must restart exactly once; starts=%d", r.starts.Load())
			}
		})
	}
}

func TestRestoreStepsSerializeWithAnAlreadyRunningReconciliation(t *testing.T) {
	for _, name := range []string{"stop", "extract", "start"} {
		t.Run(name, func(t *testing.T) {
			s := &Server{}
			executors := map[string]duraflow.StepExecutor{"stop": s.execRestoreStopContainer, "extract": s.execRestoreVerifyAndExtract, "start": s.execRestoreStartContainer}
			s.lifecycleMu.Lock()
			done := make(chan error, 1)
			entered := make(chan struct{})
			go func() { close(entered); done <- executors[name](&duraflow.StepContext{InputJSON: "invalid"}) }()
			<-entered
			select {
			case <-done:
				s.lifecycleMu.Unlock()
				t.Fatal("restore step entered an existing lifecycle/reconciliation window")
			case <-time.After(50 * time.Millisecond):
			}
			s.lifecycleMu.Unlock()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("invalid restore payload accepted")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("restore step did not resume after lifecycle window ended")
			}
		})
	}
}
