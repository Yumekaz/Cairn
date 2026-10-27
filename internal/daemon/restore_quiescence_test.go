package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/runtime"
)

type restoreQuiescenceRuntime struct {
	runtime.RuntimeBackend
	stopErrors map[string]error
	listError  error
	containers []*runtime.ContainerInfo
	stops      []string
	starts     []string
}

func (r *restoreQuiescenceRuntime) StopContainer(_ context.Context, id string) error {
	r.stops = append(r.stops, id)
	return r.stopErrors[id]
}
func (r *restoreQuiescenceRuntime) StartContainer(_ context.Context, id string) error {
	r.starts = append(r.starts, id)
	return nil
}
func (r *restoreQuiescenceRuntime) ListContainers(context.Context) ([]*runtime.ContainerInfo, error) {
	return r.containers, r.listError
}

func restoreQuiescenceFixture(t *testing.T) (*Server, *api.Service, *api.Volume, *duraflow.StepContext, *restoreQuiescenceRuntime) {
	t.Helper()
	s, st, cleanup := setupDaemonStore(t)
	t.Cleanup(cleanup)
	svc := &api.Service{ID: "quiescence-service", Name: "quiescence", Kind: "web", RuntimeID: "primary", DesiredState: "running", ActualState: "running"}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	vol := &api.Volume{ID: "quiescence-volume", Name: "quiescence-data", HostPath: filepath.Join(s.config.VolumeDir, "quiescence-data"), AttachedServiceID: svc.ID, MountPath: "/data", Status: "active"}
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
	if err := os.WriteFile(journal, []byte("live later write\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(RestoreInput{VolumeName: vol.Name, BackupID: backup.ID})
	ctx := &duraflow.StepContext{Context: context.Background(), InputJSON: string(input), State: map[string]string{}}
	rt := &restoreQuiescenceRuntime{stopErrors: make(map[string]error)}
	s.runtime = rt
	return s, svc, vol, ctx, rt
}

func requireRestoreUntouched(t *testing.T, vol *api.Volume) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vol.HostPath, "journal"))
	if err != nil || string(data) != "live later write\n" {
		t.Errorf("restore changed live data without proven writer quiescence: %q, %v", data, err)
	}
	if _, err := os.Stat(vol.HostPath + ".tmp_restore_bak"); !os.IsNotExist(err) {
		t.Errorf("destructive rename occurred before stop acknowledgement: %v", err)
	}
}

func TestRestoreStopDoesNotTrustCachedActualState(t *testing.T) {
	for _, state := range []string{"stopped", "unknown", "error", "running"} {
		t.Run(state, func(t *testing.T) {
			s, svc, vol, ctx, rt := restoreQuiescenceFixture(t)
			svc.ActualState = state
			if err := s.store.UpsertService(svc); err != nil {
				t.Fatal(err)
			}
			rt.stopErrors[svc.RuntimeID] = errors.New("writer identity unverified")
			if err := s.execRestoreStopContainer(ctx); err == nil || !strings.Contains(err.Error(), "writer identity unverified") {
				t.Errorf("cached %s bypassed authoritative stop refusal: %v", state, err)
			}
			if !reflect.DeepEqual(rt.stops, []string{svc.RuntimeID}) {
				t.Errorf("stop requests = %v", rt.stops)
			}
			requireRestoreUntouched(t, vol)
		})
	}
}

func TestRestoreExtractRechecksStopAcknowledgement(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "after_stop_step", true: "legacy_replay"}[replay], func(t *testing.T) {
			s, svc, vol, ctx, rt := restoreQuiescenceFixture(t)
			if !replay {
				if err := s.execRestoreStopContainer(ctx); err != nil {
					t.Fatal(err)
				}
			}
			rt.stopErrors[svc.RuntimeID] = errors.New("stop cleanup refused")
			if err := s.execRestoreVerifyAndExtract(ctx); err == nil || !strings.Contains(err.Error(), "stop cleanup refused") {
				t.Errorf("extraction bypassed current stop refusal: %v", err)
			}
			requireRestoreUntouched(t, vol)
			if len(rt.starts) != 0 {
				t.Errorf("uncertain writer was restarted: %v", rt.starts)
			}
		})
	}
}

func TestRestoreMissingOwnershipOrInventoryBlocksMutation(t *testing.T) {
	for _, problem := range []string{"missing_primary", "nil_inventory_entry", "inventory_error"} {
		t.Run(problem, func(t *testing.T) {
			s, svc, vol, ctx, rt := restoreQuiescenceFixture(t)
			switch problem {
			case "missing_primary":
				svc.RuntimeID = ""
				if err := s.store.UpsertService(svc); err != nil {
					t.Fatal(err)
				}
			case "nil_inventory_entry":
				rt.containers = []*runtime.ContainerInfo{nil}
			case "inventory_error":
				rt.listError = errors.New("runtime inventory unavailable")
			}
			if err := s.execRestoreVerifyAndExtract(ctx); err == nil {
				t.Error("uncertain ownership allowed extraction")
			}
			requireRestoreUntouched(t, vol)
		})
	}
}

func TestRestoreIncludesOnlyRecordedHistoricalWriters(t *testing.T) {
	s, svc, vol, ctx, rt := restoreQuiescenceFixture(t)
	dep := &api.Deploy{ID: "12345678-old-deployment", ServiceID: svc.ID, Status: "failed", Stage: "failed", HealthStatus: "unhealthy"}
	if err := s.store.CreateDeploy(dep); err != nil {
		t.Fatal(err)
	}
	rt.containers = []*runtime.ContainerInfo{
		{ID: "old-writer", Name: "cairn-" + svc.Name + "-12345678", State: runtime.StateError},
		{ID: "unrelated", Name: "cairn-" + svc.Name + "-87654321", State: runtime.StateRunning},
	}
	rt.stopErrors["old-writer"] = errors.New("historical writer still uncertain")
	if err := s.execRestoreVerifyAndExtract(ctx); err == nil || !strings.Contains(err.Error(), "historical writer still uncertain") {
		t.Errorf("old deployment writer was ignored: %v", err)
	}
	if !reflect.DeepEqual(rt.stops, []string{"primary", "old-writer"}) {
		t.Errorf("owned runtime selection = %v; must not stop unrelated prefix match", rt.stops)
	}
	requireRestoreUntouched(t, vol)
}

func TestRestoreActiveOrUnverifiedJobBlocksMutation(t *testing.T) {
	for _, status := range []string{"running", "failed"} {
		t.Run(status, func(t *testing.T) {
			s, svc, vol, ctx, _ := restoreQuiescenceFixture(t)
			now := time.Now()
			job := &api.JobRun{ID: "abcdefgh-job", ServiceID: svc.ID, Type: "one-off", Status: status, StartedAt: now}
			if status == "failed" {
				job.FinishedAt = &now
			}
			if err := s.store.CreateJobRun(job); err != nil {
				t.Fatal(err)
			}
			if err := s.execRestoreVerifyAndExtract(ctx); err == nil {
				t.Error("unverified job allowed destructive restore")
			}
			requireRestoreUntouched(t, vol)
		})
	}
}

func TestRestoreActiveDeploymentBlocksMutation(t *testing.T) {
	s, svc, vol, ctx, rt := restoreQuiescenceFixture(t)
	if err := s.store.CreateDeploy(&api.Deploy{ID: "12345678-active", ServiceID: svc.ID, Status: "pending", Stage: "migration"}); err != nil {
		t.Fatal(err)
	}
	if err := s.execRestoreVerifyAndExtract(ctx); err == nil || !strings.Contains(err.Error(), "deployment work is active") {
		t.Errorf("active deployment writer allowed physical restore: %v", err)
	}
	if len(rt.stops) != 0 {
		t.Errorf("active deployment disturbed before refusing restore: %v", rt.stops)
	}
	requireRestoreUntouched(t, vol)
}

func TestRestoreRespectsStoppedIntentAtFinalRestart(t *testing.T) {
	s, svc, _, ctx, rt := restoreQuiescenceFixture(t)
	if err := s.execRestoreStopContainer(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.execRestoreVerifyAndExtract(ctx); err != nil {
		t.Fatal(err)
	}
	svc.DesiredState = "stopped"
	if err := s.store.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	if err := s.execRestoreStartContainer(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rt.starts) != 0 {
		t.Errorf("restore undid operator Stop: %v", rt.starts)
	}
}

func TestRestoreQuiescentOwnerRestoresSelectedSnapshot(t *testing.T) {
	s, _, vol, ctx, rt := restoreQuiescenceFixture(t)
	for _, step := range []duraflow.StepExecutor{s.execRestoreStopContainer, s.execRestoreVerifyAndExtract, s.execRestoreStartContainer} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(vol.HostPath, "journal"))
	if err != nil || string(data) != "selected snapshot\n" {
		t.Fatalf("selected data = %q, %v", data, err)
	}
	if !reflect.DeepEqual(rt.starts, []string{"primary"}) {
		t.Fatalf("restore restart = %v", rt.starts)
	}
}

func TestRestoreRegisteredIdleCronVolume(t *testing.T) {
	for _, desired := range []string{"stopped", "active"} {
		t.Run(desired, func(t *testing.T) {
			s, svc, vol, ctx, rt := restoreQuiescenceFixture(t)
			svc.Kind = "cron"
			svc.RuntimeID = ""
			svc.DesiredState = desired
			svc.ActualState = desired
			if err := s.store.UpsertService(svc); err != nil {
				t.Fatal(err)
			}
			if err := s.store.UpsertCronJob(&api.CronJob{ID: "cron-schedule", Name: svc.Name, ServiceID: svc.ID, Schedule: "* * * * *", Command: "true"}); err != nil {
				t.Fatal(err)
			}
			for _, step := range []duraflow.StepExecutor{s.execRestoreStopContainer, s.execRestoreVerifyAndExtract, s.execRestoreStartContainer} {
				if err := step(ctx); err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(filepath.Join(vol.HostPath, "journal"))
			if err != nil || string(data) != "selected snapshot\n" {
				t.Errorf("cron snapshot not restored: %q,%v", data, err)
			}
			if len(rt.starts) != 0 || len(rt.stops) != 0 {
				t.Errorf("idle cron acquired phantom primary: starts%v stops%v", rt.starts, rt.stops)
			}
		})
	}
}
