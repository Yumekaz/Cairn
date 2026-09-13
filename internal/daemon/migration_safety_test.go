package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/runtime"
)

type migrationFaultRuntime struct {
	runtime.RuntimeBackend
	start    func() error
	starts   int
	exitCode int
}

func (r *migrationFaultRuntime) InspectContainer(context.Context, string) (*runtime.ContainerInfo, error) {
	return &runtime.ContainerInfo{State: runtime.StateStopped, ExitCode: &r.exitCode}, nil
}
func (r *migrationFaultRuntime) StreamLogs(context.Context, string, bool, int) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("migration output")), nil
}

func (r *migrationFaultRuntime) CreateContainer(context.Context, *api.ServiceConfig, string) (string, error) {
	return "migration-task", nil
}
func (r *migrationFaultRuntime) StartContainer(context.Context, string) error {
	r.starts++
	return r.start()
}
func (r *migrationFaultRuntime) RemoveContainer(context.Context, string) error { return nil }

func TestMigrationMutationThenInterruptionOrFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failure  error
		exitCode int
	}{
		{"interrupted", context.Canceled, 0},
		{"lost start response", errors.New("start response lost after mutation"), 0},
		{"partial migration failure", nil, 1},
		{"health failure after migration", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, st, cleanup := setupDaemonStore(t)
			defer cleanup()
			svc := api.Service{ID: "svc", Name: "svc", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := st.UpsertService(&svc); err != nil {
				t.Fatal(err)
			}
			d := api.Deploy{ID: "migration-fault", ServiceID: svc.ID, Status: "running", CreatedAt: time.Now()}
			if err := st.CreateDeploy(&d); err != nil {
				t.Fatal(err)
			}
			data := filepath.Join(t.TempDir(), "persistent-data")
			r := &migrationFaultRuntime{start: func() error {
				persisted, err := st.GetDeploy(d.ID)
				if err != nil || !persisted.StateTouched {
					t.Fatalf("execution before durable marker: %v", err)
				}
				if err := os.WriteFile(data, []byte("schema changed"), 0600); err != nil {
					t.Fatal(err)
				}
				return tc.failure
			}}
			r.exitCode = tc.exitCode
			s.runtime = r
			input, err := json.Marshal(DeployInput{ServiceConfig: api.ServiceConfig{Migration: "change-schema"}, Deploy: d, Service: svc})
			if err != nil {
				t.Fatal(err)
			}
			step := &duraflow.StepContext{Context: context.Background(), InputJSON: string(input), State: map[string]string{}}
			migrationErr := s.execDeployRunMigration(step)
			if tc.failure == nil && tc.exitCode == 0 {
				if migrationErr != nil {
					t.Fatal(migrationErr)
				}
				// Health failure uses the original workflow payload without the marker.
				s.failDeploy(&d, &svc, "candidate health check failed")
			} else if migrationErr == nil {
				t.Fatal("expected migration failure")
			}
			if _, err := os.Stat(data); err != nil {
				t.Fatal(err)
			}
			// Reconstruct the server and step state, as after daemon restart.
			restarted := &Server{store: st, config: s.config, runtime: r}
			step.State = map[string]string{}
			if err := restarted.execDeployRunMigration(step); err == nil {
				t.Fatal("replay allowed")
			}
			if r.starts != 1 {
				t.Fatalf("migration executed %d times", r.starts)
			}
		})
	}
}

// A nil runtime deliberately panics if recovery attempts any external work.
func TestMigrationReplayBlockedAndMarkerSticky(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()
	svc := api.Service{ID: "migration-service", Name: "migration-service", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := st.UpsertService(&svc); err != nil {
		t.Fatal(err)
	}
	d := api.Deploy{ID: "migration-deploy", ServiceID: svc.ID, Status: "running", StateTouched: true, CreatedAt: time.Now()}
	if err := st.CreateDeploy(&d); err != nil {
		t.Fatal(err)
	}
	// Later steps deserialize the original input, which predates the marker.
	d.StateTouched = false
	if err := st.UpdateDeploy(&d); err != nil {
		t.Fatal(err)
	}
	persisted, err := st.GetDeploy(d.ID)
	if err != nil || !persisted.StateTouched {
		t.Fatalf("marker lost: %+v %v", persisted, err)
	}
	input, err := json.Marshal(DeployInput{ServiceConfig: api.ServiceConfig{Migration: "destructive-command"}, Deploy: d, Service: svc})
	if err != nil {
		t.Fatal(err)
	}
	err = s.execDeployRunMigration(&duraflow.StepContext{Context: context.Background(), InputJSON: string(input), State: map[string]string{}})
	if err == nil {
		t.Fatal("uncertain migration must not replay")
	}
	persisted, err = st.GetDeploy(d.ID)
	if err != nil || !persisted.StateTouched || persisted.Status != "failed" {
		t.Fatalf("unsafe recovery state: %+v %v", persisted, err)
	}
}
