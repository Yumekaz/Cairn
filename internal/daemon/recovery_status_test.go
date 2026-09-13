package daemon

import (
	"encoding/json"
	"github.com/yumekaz/cairn/internal/api"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoveryStatusPreservesUncertaintyAndOmitsSecrets(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()
	svc := &api.Service{ID: "svc", Name: "svc", CurrentDeployID: "healthy", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	for _, d := range []*api.Deploy{
		{ID: "healthy", ServiceID: svc.ID, Status: "success", StateTouched: true, CreatedAt: time.Now()},
		{ID: "uncertain", ServiceID: svc.ID, Status: "failed", StateTouched: true, FailureReason: "automatic replay blocked", CreatedAt: time.Now()},
	} {
		if err := st.CreateDeploy(d); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(s.config.DataDir, "services", "svc")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy_uncertain.json"), []byte(`{"environment":{"SECRET":"must-not-leak"},"volumes":[{"name":"data"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	req := withChiURLParam(httptest.NewRequest("GET", "/services/svc/recovery", nil), "name", "svc")
	rr := httptest.NewRecorder()
	s.handleRecoveryStatus(rr, req)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "must-not-leak") {
		t.Fatal("secret exposed")
	}
	var result api.RecoveryStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Deployments) != 1 || result.Deployments[0].TaskState != "unknown" || len(result.Deployments[0].Volumes) != 1 {
		t.Fatalf("unexpected report: %+v", result)
	}
	d, err := st.GetDeploy("uncertain")
	if err != nil || !d.StateTouched || d.Status != "failed" {
		t.Fatalf("read mutated state: %+v %v", d, err)
	}
}
