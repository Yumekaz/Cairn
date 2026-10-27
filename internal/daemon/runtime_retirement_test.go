package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/events"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
)

type retirementRuntime struct {
	runtime.RuntimeBackend
	failurePhase string
	calls        []string
	removed      bool
}

func (r *retirementRuntime) InspectContainer(_ context.Context, id string) (*runtime.ContainerInfo, error) {
	return &runtime.ContainerInfo{ID: id, State: runtime.StateRunning}, nil
}
func (r *retirementRuntime) StopContainer(_ context.Context, id string) error {
	r.calls = append(r.calls, "stop:"+id)
	if r.failurePhase == "stop" {
		return errors.New("uncertain writer")
	}
	return nil
}
func (r *retirementRuntime) RemoveContainer(_ context.Context, id string) error {
	r.calls = append(r.calls, "remove:"+id)
	if r.failurePhase == "remove" {
		return errors.New("incomplete cleanup")
	}
	r.removed = true
	return nil
}

func (r *retirementRuntime) ListContainers(context.Context) ([]*runtime.ContainerInfo, error) {
	if r.removed {
		return nil, nil
	}
	return []*runtime.ContainerInfo{{ID: "old-runtime", Name: "old-runtime", State: runtime.StateRunning}}, nil
}

func TestDeploymentRetirementFailureIsVisibleAndKeepsOwnership(t *testing.T) {
	for _, kind := range []string{"web", "cron"} {
		for _, phase := range []string{"stop", "remove", "success"} {
			t.Run(kind+"/"+phase, func(t *testing.T) {
				s, st, cleanup := setupDaemonStore(t)
				defer cleanup()
				svc := &api.Service{ID: "retirement-service", Name: "retirement", Kind: "web", RuntimeID: "old-runtime", DesiredState: "running", ActualState: "running", CurrentDeployID: "aaaaaaaa-old"}
				if err := st.UpsertService(svc); err != nil {
					t.Fatal(err)
				}
				old := &api.Deploy{ID: svc.CurrentDeployID, ServiceID: svc.ID, Status: "success", Stage: "completed", HealthStatus: "healthy"}
				dep := &api.Deploy{ID: "bbbbbbbb-candidate", ServiceID: svc.ID, Status: "pending", Stage: "starting", PreviousDeployID: old.ID}
				for _, d := range []*api.Deploy{old, dep} {
					if err := st.CreateDeploy(d); err != nil {
						t.Fatal(err)
					}
				}
				rt := &retirementRuntime{failurePhase: phase}
				s.runtime = rt
				cfg := api.ServiceConfig{Name: svc.Name, Kind: kind, Schedule: "* * * * *", Run: "true"}
				input, _ := json.Marshal(DeployInput{ServiceConfig: cfg, Service: *svc, Deploy: *dep, PreviousRuntimeID: svc.RuntimeID})
				err := s.execDeployRouteTrafficAndCleanup(&duraflow.StepContext{Context: context.Background(), InputJSON: string(input), State: map[string]string{"candidate_id": "new-runtime"}})
				if kind == "cron" && phase != "success" {
					if err == nil {
						t.Error("cron registration concealed failed prior-runtime retirement")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				wantCalls := []string{"stop:old-runtime"}
				if phase != "stop" {
					wantCalls = append(wantCalls, "remove:old-runtime")
				}
				if !reflect.DeepEqual(rt.calls, wantCalls) {
					t.Errorf("retirement calls %v, want %v", rt.calls, wantCalls)
				}
				saved, err := st.GetService(svc.ID)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "web" && saved.RuntimeID != "new-runtime" {
					t.Errorf("healthy candidate cutover lost: %+v", saved)
				}
				if kind == "cron" && phase != "success" && saved.RuntimeID != "old-runtime" {
					t.Errorf("failed retirement discarded owner: %+v", saved)
				}
				if oldSaved, err := st.GetDeploy(old.ID); err != nil || oldSaved == nil {
					t.Fatalf("historical ownership lost: %v", err)
				}
				evts, err := st.ListEvents(store.EventFilter{ServiceID: &svc.ID})
				if err != nil {
					t.Fatal(err)
				}
				warnings := 0
				for _, evt := range evts {
					if evt.Type == events.RuntimeCleanupFailed.String() {
						warnings++
						if !strings.Contains(evt.MetadataJSON, "old-runtime") || !strings.Contains(evt.MetadataJSON, phase) {
							t.Errorf("warning lost runtime/phase: %+v", evt)
						}
					}
				}
				wantWarnings := 1
				if phase == "success" {
					wantWarnings = 0
				}
				if warnings != wantWarnings {
					t.Errorf("durable cleanup warnings = %d, want %d", warnings, wantWarnings)
				}
			})
		}
	}
}

func TestCronCutoverReplayPreservesCommittedOwnerAndStoppedIntent(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()
	svc := &api.Service{ID: "cron-replay", Name: "cron-replay", Kind: "web", RuntimeID: "old-runtime", DesiredState: "running", ActualState: "running", CurrentDeployID: "aaaaaaaa-old"}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	dep := &api.Deploy{ID: "bbbbbbbb-cron", ServiceID: svc.ID, Status: "pending", Stage: "starting", PreviousDeployID: svc.CurrentDeployID}
	if err := st.CreateDeploy(dep); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(DeployInput{ServiceConfig: api.ServiceConfig{Name: svc.Name, Kind: "cron", Schedule: "* * * * *", Run: "true"}, Service: *svc, Deploy: *dep, PreviousRuntimeID: svc.RuntimeID})
	rt := &retirementRuntime{}
	s.runtime = rt
	ctx := &duraflow.StepContext{Context: context.Background(), InputJSON: string(input), State: map[string]string{}}
	if err := s.execDeployRouteTrafficAndCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	saved, err := st.GetService(svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Kind != "cron" || saved.RuntimeID != "" {
		t.Fatalf("cron ownership not committed: %+v", saved)
	}
	saved.DesiredState = "stopped"
	saved.ActualState = "stopped"
	if err := st.UpsertService(saved); err != nil {
		t.Fatal(err)
	}
	calls := append([]string(nil), rt.calls...)
	// Simulate loss of the durable step acknowledgement and all in-memory state.
	ctx.State = map[string]string{}
	if err := s.execDeployRouteTrafficAndCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	saved, err = st.GetService(svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.CurrentDeployID != dep.ID || saved.DesiredState != "stopped" || saved.RuntimeID != "" {
		t.Errorf("replay undid committed operator state: %+v", saved)
	}
	if !reflect.DeepEqual(calls, rt.calls) {
		t.Errorf("replayed completed runtime retirement: %v", rt.calls)
	}
	d, err := st.GetDeploy(dep.ID)
	if err != nil || d.Status != "success" {
		t.Errorf("replay invalidated completed deploy: %+v,%v", d, err)
	}
}

func TestCronCutoverResumesAfterConfirmedPreviousRuntimeRemoval(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()
	svc := &api.Service{ID: "cron-precommit", Name: "cron-precommit", Kind: "web", RuntimeID: "old-runtime", DesiredState: "running", CurrentDeployID: "aaaaaaaa-old"}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	dep := &api.Deploy{ID: "bbbbbbbb-cron", ServiceID: svc.ID, Status: "pending", Stage: "starting", PreviousDeployID: svc.CurrentDeployID}
	if err := st.CreateDeploy(dep); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(DeployInput{ServiceConfig: api.ServiceConfig{Name: svc.Name, Kind: "cron", Schedule: "* * * * *", Run: "true"}, Service: *svc, Deploy: *dep, PreviousRuntimeID: svc.RuntimeID})
	rt := &retirementRuntime{removed: true, failurePhase: "stop"}
	s.runtime = rt
	if err := s.execDeployRouteTrafficAndCleanup(&duraflow.StepContext{Context: context.Background(), InputJSON: string(input), State: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("confirmed absent previous runtime was stopped again: %v", rt.calls)
	}
	saved, err := st.GetService(svc.ID)
	if err != nil || saved.CurrentDeployID != dep.ID || saved.Kind != "cron" {
		t.Errorf("cutover did not recover: %+v,%v", saved, err)
	}
}
