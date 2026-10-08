package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashboardRejectsCrossOriginAndRebindingRequests(t *testing.T) {
	for _, test := range []struct {
		name, host, origin, fetchSite string
		want                          int
	}{
		{"same origin", "127.0.0.1:2476", "http://127.0.0.1:2476", "same-origin", 204},
		{"local CLI", "localhost:2476", "", "", 204},
		{"cross-origin simple POST", "127.0.0.1:2476", "https://hostile.invalid", "cross-site", 403},
		{"opaque origin", "127.0.0.1:2476", "null", "", 403},
		{"fetch without Origin", "127.0.0.1:2476", "", "cross-site", 403},
		{"rebound hostname", "hostile.invalid:2476", "http://hostile.invalid:2476", "same-origin", 403},
		{"wrong port", "127.0.0.1:80", "", "", 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := false
			handler := dashboardHTTPGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mutated = true; w.WriteHeader(204) }), "127.0.0.1:2476")
			req := httptest.NewRequest("POST", "http://"+test.host+"/services/test/stop", nil)
			req.Header.Set("Content-Type", "text/plain")
			req.Header.Set("Origin", test.origin)
			req.Header.Set("Sec-Fetch-Site", test.fetchSite)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != test.want || mutated != (test.want == 204) {
				t.Fatalf("status=%d mutated=%v", res.Code, mutated)
			}
		})
	}
}
