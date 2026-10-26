package daemon

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/runtime"
)

// This runtime only captures configuration and simulates a successful task.
// Unimplemented methods panic through the nil embedded interface; there is no
// real runtime, process execution, or container backend to fall back to.
type taskIdentityRuntime struct {
	runtime.RuntimeBackend
	configs []api.ServiceConfig
	names   []string
	calls   []string
}

func (r *taskIdentityRuntime) CreateContainer(_ context.Context, cfg *api.ServiceConfig, name string) (string, error) {
	r.configs = append(r.configs, *cfg)
	r.names = append(r.names, name)
	r.calls = append(r.calls, "create")
	return "identity-task", nil
}

func (r *taskIdentityRuntime) StartContainer(_ context.Context, id string) error {
	r.calls = append(r.calls, "start:"+id)
	return nil
}

func (r *taskIdentityRuntime) InspectContainer(_ context.Context, id string) (*runtime.ContainerInfo, error) {
	r.calls = append(r.calls, "inspect:"+id)
	exitCode := 0
	return &runtime.ContainerInfo{ID: id, State: runtime.StateStopped, ExitCode: &exitCode}, nil
}

func (r *taskIdentityRuntime) StreamLogs(_ context.Context, id string, _ bool, _ int) (io.ReadCloser, error) {
	r.calls = append(r.calls, "logs:"+id)
	return io.NopCloser(strings.NewReader("simulated task output")), nil
}

func (r *taskIdentityRuntime) RemoveContainer(_ context.Context, id string) error {
	r.calls = append(r.calls, "remove:"+id)
	return nil
}

func TestTaskIdentity(t *testing.T) {
	for _, path := range []string{"candidate", "migration_input", "migration_state", "cron"} {
		for _, tc := range []struct {
			name string
			user string
		}{
			{name: "explicit_uid_gid", user: "1001:1002"},
			{name: "empty_default", user: ""},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				s, st, cleanup := setupDaemonStore(t)
				defer cleanup()
				rt := &taskIdentityRuntime{}
				s.runtime = rt
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()

				svc := api.Service{ID: "identity-service", Name: "identity-service", Kind: "worker", CurrentDeployID: "identity-deploy"}
				if err := st.UpsertService(&svc); err != nil {
					t.Fatal(err)
				}
				deploy := api.Deploy{ID: svc.CurrentDeployID, ServiceID: svc.ID, Status: "running"}
				if err := st.CreateDeploy(&deploy); err != nil {
					t.Fatal(err)
				}
				cfg := api.ServiceConfig{
					Name: svc.Name, Kind: svc.Kind, Image: "identity-test-image", User: tc.user,
					Command:     []string{"main-command"},
					Migration:   "migration-command",
					Environment: map[string]string{"IDENTITY_FIXTURE": "preserved"},
					Volumes:     []api.VolumeConfig{{Name: "identity-volume", MountPath: "/data"}},
				}
				if path == "cron" {
					cfg.Kind = "cron"
					cfg.Schedule = "* * * * *"
					cfg.Run = "scheduled-command"
				}
				inputCfg := cfg
				state := map[string]string{}
				if path == "migration_state" {
					// The saved workflow config must take precedence, including
					// when its User is empty and the original payload is not.
					inputCfg.User = "2001:2002"
					encoded, err := json.Marshal(cfg)
					if err != nil {
						t.Fatal(err)
					}
					state["config_json"] = string(encoded)
				}
				encoded, err := json.Marshal(DeployInput{ServiceConfig: inputCfg, Deploy: deploy, Service: svc})
				if err != nil {
					t.Fatal(err)
				}
				step := &duraflow.StepContext{Context: ctx, InputJSON: string(encoded), State: state}
				wantCommand := cfg.Command
				wantCalls := []string{"create"}
				wantNamePrefix := "cairn-" + svc.Name + "-"

				switch path {
				case "candidate":
					if err := s.execDeployCreateContainer(step); err != nil {
						t.Fatal(err)
					}
					if step.State["candidate_id"] != "identity-task" {
						t.Fatalf("candidate not recorded: %v", step.State)
					}
				case "migration_input", "migration_state":
					if err := s.execDeployRunMigration(step); err != nil {
						t.Fatal(err)
					}
					persisted, err := st.GetDeploy(deploy.ID)
					if err != nil || persisted == nil || !persisted.StateTouched {
						t.Fatalf("migration marker not persisted: %+v, %v", persisted, err)
					}
					wantCommand = []string{"/bin/sh", "-c", cfg.Migration}
					wantCalls = append(wantCalls, "start:identity-task", "inspect:identity-task", "remove:identity-task")
					wantNamePrefix += "task-"
				case "cron":
					// Use the production config persistence and execution paths,
					// not a duplicate of the scheduler's task constructor.
					if err := s.execDeploySaveConfigOnDisk(step); err != nil {
						t.Fatal(err)
					}
					job := &api.CronJob{ID: "identity-cron", ServiceID: svc.ID, Name: svc.Name, Schedule: cfg.Schedule, Command: cfg.Run}
					if err := st.CreateCronJob(job); err != nil {
						t.Fatal(err)
					}
					if err := NewScheduler(st, rt, s.config.DataDir).ExecuteCronJob(ctx, job); err != nil {
						t.Fatal(err)
					}
					runs, err := st.ListJobRunsByCronJob(job.ID)
					if err != nil {
						t.Fatal(err)
					}
					if len(runs) != 1 || runs[0].Status != "success" || runs[0].ExitCode == nil || *runs[0].ExitCode != 0 || runs[0].FinishedAt == nil || runs[0].Logs != "simulated task output" {
						t.Fatalf("cron execution did not complete successfully: %+v", runs)
					}
					wantCommand = []string{"/bin/sh", "-c", job.Command}
					wantCalls = append(wantCalls, "start:identity-task", "inspect:identity-task", "logs:identity-task", "remove:identity-task")
					wantNamePrefix += "cron-"
				}

				if len(rt.configs) != 1 {
					t.Fatalf("runtime creates = %d, want 1", len(rt.configs))
				}
				task := rt.configs[0]
				if task.User != tc.user {
					t.Errorf("runtime task.User = %q, want configured User %q", task.User, tc.user)
				}
				if task.Name != cfg.Name || task.Kind != cfg.Kind || task.Image != cfg.Image || !reflect.DeepEqual(task.Command, wantCommand) || !reflect.DeepEqual(task.Environment, cfg.Environment) || !reflect.DeepEqual(task.Volumes, cfg.Volumes) {
					t.Errorf("task lost other service configuration: %+v", task)
				}
				if !strings.HasPrefix(rt.names[0], wantNamePrefix) {
					t.Errorf("task name = %q, want prefix %q", rt.names[0], wantNamePrefix)
				}
				if !reflect.DeepEqual(rt.calls, wantCalls) {
					t.Errorf("runtime lifecycle = %v, want %v", rt.calls, wantCalls)
				}
				t.Logf("captured task.User=%q through %s (mock runtime only)", task.User, path)
			})
		}
	}
}
