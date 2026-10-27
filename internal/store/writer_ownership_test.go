package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yumekaz/cairn/internal/api"
)

func TestWorkflowRestoreAdmissionIsAtomic(t *testing.T) {
	for _, pair := range [][2]string{{"restore", "restore"}, {"restore", "deploy"}, {"deploy", "restore"}, {"restore", "backup"}, {"backup", "restore"}} {
		t.Run(pair[0]+"/"+pair[1], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "admission.db")
			a, err := NewStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := NewStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			start := make(chan struct{})
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for i, st := range []*Store{a, b} {
				wg.Add(1)
				go func(i int, st *Store) {
					defer wg.Done()
					<-start
					results <- st.CreateWorkflow(&api.Workflow{ID: []string{"first", "second"}[i], Type: pair[i], Status: "pending", InputJSON: "{}"})
				}(i, st)
			}
			close(start)
			wg.Wait()
			close(results)
			success, refused := 0, 0
			for err := range results {
				if err == nil {
					success++
				} else if errors.Is(err, ErrWorkflowConflict) {
					refused++
				} else {
					t.Errorf("admission failed for unrelated reason: %v", err)
				}
			}
			if success != 1 || refused != 1 {
				t.Errorf("mutually exclusive writer admissions succeeded=%d refused=%d", success, refused)
			}
			active, err := a.ListRunningWorkflows()
			if err != nil || len(active) != 1 {
				t.Errorf("durable reservations=%d,%v", len(active), err)
			}
		})
	}
}

func TestWorkflowRestoreAdmissionReleasesOnlyAfterTerminalState(t *testing.T) {
	st, cleanup := setupTestStore(t)
	defer cleanup()
	first := &api.Workflow{ID: "restore", Type: "restore", Status: "pending", InputJSON: "{}"}
	if err := st.CreateWorkflow(first); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"pending", "running"} {
		first.Status = status
		if err := st.UpdateWorkflow(first); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateWorkflow(&api.Workflow{ID: "blocked-" + status, Type: "deploy", Status: "pending"}); err == nil {
			t.Errorf("%s restore admitted another writer", status)
		}
	}
	first.Status = "failed"
	if err := st.UpdateWorkflow(first); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkflow(&api.Workflow{ID: "after-failure", Type: "deploy", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
}

func TestVolumeOwnerCannotBeReassignedOrDetachedByStaleUpsert(t *testing.T) {
	for _, replacement := range []string{"another-service", ""} {
		t.Run(replacement, func(t *testing.T) {
			st, cleanup := setupTestStore(t)
			defer cleanup()
			for _, id := range []string{"owner-service", "another-service"} {
				if err := st.UpsertService(&api.Service{ID: id, Name: id}); err != nil {
					t.Fatal(err)
				}
			}
			vol := &api.Volume{ID: "owned-volume", Name: "owned-volume", HostPath: "/private-test/not-created", AttachedServiceID: "owner-service", MountPath: "/data", Status: "active"}
			if err := st.UpsertVolume(vol); err != nil {
				t.Fatal(err)
			}
			vol.AttachedServiceID = replacement
			if err := st.UpsertVolume(vol); err == nil {
				t.Error("stale upsert discarded live volume ownership")
			}
			saved, err := st.GetVolume(vol.ID)
			if err != nil || saved.AttachedServiceID != "owner-service" {
				t.Errorf("ownership changed: %+v,%v", saved, err)
			}
			// Deliberate service deletion is the supported detach boundary.
			if err := st.DeleteService("owner-service"); err != nil {
				t.Fatal(err)
			}
			vol.AttachedServiceID = "another-service"
			if err := st.UpsertVolume(vol); err != nil {
				t.Fatalf("detached volume could not be claimed: %v", err)
			}
		})
	}
}
