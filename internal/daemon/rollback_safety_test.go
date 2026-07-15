package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/events"
	"github.com/yumekaz/cairn/internal/store"
)

func withChiURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestRollbackBlockedEmitsEvent(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()

	svcID := uuid.New().String()
	oldID := uuid.New().String()
	touchedID := uuid.New().String()
	now := time.Now()

	svc := &api.Service{
		ID:              svcID,
		Name:            "rb-svc",
		Kind:            "web",
		RuntimeBackend:  "mini-docker",
		CurrentDeployID: touchedID,
		DesiredState:    "running",
		ActualState:     "running",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeploy(&api.Deploy{
		ID: oldID, ServiceID: svcID, Version: "v1", SourcePath: "inline",
		Status: "success", Stage: "completed", HealthStatus: "healthy",
		StateTouched: false, CreatedAt: now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeploy(&api.Deploy{
		ID: touchedID, ServiceID: svcID, Version: "v2", SourcePath: "inline",
		Status: "success", Stage: "completed", HealthStatus: "healthy",
		StateTouched: true, PreviousDeployID: oldID, CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"deploy_id": oldID,
		"force":     false,
	})
	req := httptest.NewRequest(http.MethodPost, "/services/rb-svc/rollback", bytes.NewReader(body))
	req = withChiURLParam(req, "name", "rb-svc")

	rr := httptest.NewRecorder()
	s.handleRollbackService(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d body=%s", rr.Code, rr.Body.String())
	}

	evts, err := st.ListEvents(store.EventFilter{ServiceID: &svcID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evts {
		if e.Type == events.RollbackBlocked.String() {
			found = true
			if e.Message == "" {
				t.Fatal("RollbackBlocked message should explain why")
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected RollbackBlocked event, got %d events", len(evts))
	}
}

func TestRollbackForcedEmitsEvent(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()
	// No runtime: workflow may fail after events are written — we only assert events.

	svcID := uuid.New().String()
	oldID := uuid.New().String()
	touchedID := uuid.New().String()
	now := time.Now()

	svc := &api.Service{
		ID:              svcID,
		Name:            "rb-force",
		Kind:            "web",
		RuntimeBackend:  "mini-docker",
		CurrentDeployID: touchedID,
		DesiredState:    "running",
		ActualState:     "running",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeploy(&api.Deploy{
		ID: oldID, ServiceID: svcID, Version: "v1", SourcePath: "inline",
		Status: "success", Stage: "completed", HealthStatus: "healthy",
		StateTouched: false, CreatedAt: now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDeploy(&api.Deploy{
		ID: touchedID, ServiceID: svcID, Version: "v2", SourcePath: "inline",
		Status: "success", Stage: "completed", HealthStatus: "healthy",
		StateTouched: true, PreviousDeployID: oldID, CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	cfgDir := filepath.Join(s.config.DataDir, "services", svc.Name)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgJSON := []byte(`{"name":"rb-force","kind":"web","image":"/tmp/rootfs","command":["/bin/true"]}`)
	if err := os.WriteFile(filepath.Join(cfgDir, "deploy_"+oldID+".json"), cfgJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"deploy_id": oldID,
		"force":     true,
	})
	req := httptest.NewRequest(http.MethodPost, "/services/rb-force/rollback", bytes.NewReader(body))
	req = withChiURLParam(req, "name", "rb-force")

	rr := httptest.NewRecorder()
	s.handleRollbackService(rr, req)
	_ = rr.Code

	evts, err := st.ListEvents(store.EventFilter{ServiceID: &svcID})
	if err != nil {
		t.Fatal(err)
	}
	foundForced, foundStarted := false, false
	for _, e := range evts {
		switch e.Type {
		case events.RollbackForced.String():
			foundForced = true
		case events.DeployStarted.String():
			foundStarted = true
		case events.RollbackBlocked.String():
			t.Fatal("must not emit RollbackBlocked when force=true")
		}
	}
	if !foundForced {
		t.Fatal("expected RollbackForced event when force bypasses StateTouched")
	}
	if !foundStarted {
		t.Fatal("expected DeployStarted after force path proceeds")
	}
}
