package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/runtime"
)

type jobCleanupRuntime struct {
	runtime.RuntimeBackend
	containers []*runtime.ContainerInfo
	removed    []string
}

func (r *jobCleanupRuntime) ListContainers(context.Context) ([]*runtime.ContainerInfo, error) {
	return r.containers, nil
}
func (r *jobCleanupRuntime) StopContainer(context.Context, string) error { return nil }
func (r *jobCleanupRuntime) RemoveContainer(_ context.Context, id string) error {
	r.removed = append(r.removed, id)
	return nil
}

func TestReconciliationPreservesTrackedCronAndOneOffTasks(t *testing.T) {
	for _, kind := range []string{"cron", "one-off"} {
		t.Run(kind, func(t *testing.T) {
			s, st, cleanup := setupDaemonStore(t)
			defer cleanup()
			now := time.Now()
			svc := &api.Service{ID: "svc", Name: "job-proof", Kind: "cron", DesiredState: "active", ActualState: "active", CreatedAt: now, UpdatedAt: now}
			if err := st.UpsertService(svc); err != nil {
				t.Fatal(err)
			}
			jr := &api.JobRun{ID: "01234567-89ab-cdef-0123-456789abcdef", ServiceID: svc.ID, Type: kind, Status: "running", StartedAt: now}
			if err := st.CreateJobRun(jr); err != nil {
				t.Fatal(err)
			}
			suffix := "cron"
			if kind == "one-off" {
				suffix = "oneoff"
			}
			owned := &runtime.ContainerInfo{ID: "owned-task", Name: "cairn-job-proof-" + suffix + "-01234567", State: runtime.StateRunning}
			rogue := &runtime.ContainerInfo{ID: "rogue", Name: "cairn-job-proof-" + suffix + "-untracked", State: runtime.StateRunning}
			rt := &jobCleanupRuntime{containers: []*runtime.ContainerInfo{owned, rogue}}
			s.runtime = rt
			s.ReconcileServices(context.Background())
			if len(rt.removed) != 1 || rt.removed[0] != "rogue" {
				t.Fatalf("cleanup removed an active tracked job or missed the rogue: %v", rt.removed)
			}
			jr.Status = "success"
			if err := st.UpdateJobRun(jr); err != nil {
				t.Fatal(err)
			}
			rt.containers = []*runtime.ContainerInfo{owned}
			rt.removed = nil
			s.ReconcileServices(context.Background())
			if len(rt.removed) != 1 || rt.removed[0] != "owned-task" {
				t.Fatalf("terminal leftover was not reclaimed: %v", rt.removed)
			}
		})
	}
}
