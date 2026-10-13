package daemon

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/runtime"
)

type stoppingRuntime struct {
	runtime.RuntimeBackend
	stopped chan struct{}
	release chan struct{}
	starts  atomic.Int32
}

func (r *stoppingRuntime) StopContainer(context.Context, string) error {
	// Real Mini-Docker publishes stopped before its host-side NAT cleanup.
	close(r.stopped)
	<-r.release
	return nil
}
func (r *stoppingRuntime) InspectContainer(context.Context, string) (*runtime.ContainerInfo, error) {
	return &runtime.ContainerInfo{State: runtime.StateStopped}, nil
}
func (r *stoppingRuntime) StartContainer(context.Context, string) error {
	r.starts.Add(1)
	return nil
}
func (r *stoppingRuntime) ListContainers(context.Context) ([]*runtime.ContainerInfo, error) {
	return nil, nil
}
func (r *stoppingRuntime) RemoveContainer(ctx context.Context, id string) error {
	return r.StopContainer(ctx, id)
}

func TestStopCannotBeUndoneByReconciliation(t *testing.T) {
	testLifecycleCannotBeUndoneByReconciliation(t, false)
}

func TestRemovalCannotBeUndoneByReconciliation(t *testing.T) {
	testLifecycleCannotBeUndoneByReconciliation(t, true)
}

func testLifecycleCannotBeUndoneByReconciliation(t *testing.T, remove bool) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()
	now := time.Now()
	svc := &api.Service{ID: "svc", Name: "lifecycle-proof", RuntimeID: "container", DesiredState: "running", ActualState: "running", CreatedAt: now, UpdatedAt: now}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	r := &stoppingRuntime{stopped: make(chan struct{}), release: make(chan struct{})}
	s.runtime = r
	response := httptest.NewRecorder()
	stopDone := make(chan struct{})
	go func() {
		request := withChiURLParam(httptest.NewRequest("POST", "/services/lifecycle-proof/stop", nil), "name", svc.Name)
		if remove {
			s.handleRemoveService(response, request)
		} else {
			s.handleStopService(response, request)
		}
		close(stopDone)
	}()
	<-r.stopped
	reconcileDone := make(chan struct{})
	go func() { s.ReconcileServices(context.Background()); close(reconcileDone) }()
	select {
	case <-reconcileDone:
		t.Error("reconciliation entered the stop/cleanup window")
	case <-time.After(100 * time.Millisecond):
	}
	close(r.release)
	<-stopDone
	<-reconcileDone
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if r.starts.Load() != 0 {
		t.Fatal("reconciliation restarted a container the operator was stopping")
	}
	got, err := st.GetService(svc.ID)
	if remove {
		if err != nil || got != nil {
			t.Fatalf("removed service resurrected: %+v %v", got, err)
		}
		return
	}
	if err != nil || got.DesiredState != "stopped" || got.ActualState != "stopped" {
		t.Fatalf("stop intent overwritten: %+v %v", got, err)
	}
}
