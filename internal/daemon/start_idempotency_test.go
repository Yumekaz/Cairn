package daemon

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/runtime"
)

type startAckRuntime struct {
	runtime.RuntimeBackend
	running, startsRunning bool
	starts                 int
}

func (r *startAckRuntime) InspectContainer(context.Context, string) (*runtime.ContainerInfo, error) {
	state := runtime.StateStopped
	if r.running {
		state = runtime.StateRunning
	}
	return &runtime.ContainerInfo{State: state}, nil
}
func (r *startAckRuntime) StartContainer(context.Context, string) error {
	r.starts++
	r.running = r.startsRunning
	return errors.New("start acknowledgement unavailable")
}
func TestServiceStartRequiresPositiveRunningEvidence(t *testing.T) {
	for _, test := range []struct {
		name           string
		already, after bool
		status, starts int
	}{
		{"already running", true, false, 200, 0},
		{"lost acknowledgement", false, true, 200, 1},
		{"real failure", false, false, 500, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, st, cleanup := setupDaemonStore(t)
			defer cleanup()
			now := time.Now()
			svc := &api.Service{ID: "start-proof", Name: "start-proof", RuntimeID: "owned-runtime", DesiredState: "stopped", ActualState: "stopped", CreatedAt: now, UpdatedAt: now}
			if err := st.UpsertService(svc); err != nil {
				t.Fatal(err)
			}
			rt := &startAckRuntime{running: test.already, startsRunning: test.after}
			s.runtime = rt
			response := httptest.NewRecorder()
			req := withChiURLParam(httptest.NewRequest("POST", "/services/start-proof/start", nil), "name", svc.Name)
			s.handleStartService(response, req)
			if response.Code != test.status || rt.starts != test.starts {
				t.Fatalf("status=%d starts=%d body=%s", response.Code, rt.starts, response.Body.String())
			}
			saved, err := st.GetService(svc.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (saved.DesiredState == "running") != (test.status == 200) {
				t.Fatalf("incorrect durable desired state: %s", saved.DesiredState)
			}
		})
	}
}
