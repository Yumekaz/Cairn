package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yumekaz/cairn/internal/api"
)

func TestServiceVirtualHostsNeverReachAdminHandlers(t *testing.T) {
	s, st, cleanup := setupDaemonStore(t)
	defer cleanup()
	s.config.DashboardAddr = "127.0.0.1:2476"
	s.router = chi.NewRouter()
	mutations := 0
	s.router.Post("/services/victim/stop", func(w http.ResponseWriter, r *http.Request) {
		mutations++
		w.WriteHeader(204)
	})
	now := time.Now()
	if err := st.UpsertService(&api.Service{ID: "app", Name: "app", ActualState: "stopped", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		host string
		want int
	}{
		{"app.localhost", 503},
		{"app.localhost:2476", 503},
		{"missing.localhost", 404},
		{"missing.localhost:2476", 404},
		{"app.localhost:80", 403},
		{"hostile.invalid:2476", 403},
	} {
		t.Run(test.host, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://127.0.0.1:2476/services/victim/stop", nil)
			req.Host = test.host
			req.Header.Set("Origin", "http://"+test.host)
			response := httptest.NewRecorder()
			s.tcpHTTPHandler().ServeHTTP(response, req)
			if response.Code != test.want || mutations != 0 {
				t.Fatalf("status=%d admin mutations=%d", response.Code, mutations)
			}
		})
	}
	// Requests from an application origin to the real admin host stay blocked.
	req := httptest.NewRequest("POST", "http://127.0.0.1:2476/services/victim/stop", nil)
	req.Header.Set("Origin", "http://app.localhost:2476")
	response := httptest.NewRecorder()
	s.tcpHTTPHandler().ServeHTTP(response, req)
	if response.Code != 403 || mutations != 0 {
		t.Fatalf("application origin reached admin: status=%d mutations=%d", response.Code, mutations)
	}
}
