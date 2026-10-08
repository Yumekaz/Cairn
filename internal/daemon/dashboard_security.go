package daemon

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// The TCP dashboard is a local admin API, not an authenticated multi-tenant
// endpoint. Reject browser cross-origin calls and unexpected Host headers;
// otherwise a hostile website can reach it through CSRF or DNS rebinding.
// CLI traffic over the permission-protected Unix socket is unaffected.
func dashboardHTTPGuard(next http.Handler, address string) http.Handler {
	listenHost, listenPort, _ := net.SplitHostPort(address)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(r.Host)
		allowedHost := strings.EqualFold(host, listenHost)
		if ip := net.ParseIP(listenHost); ip != nil && ip.IsLoopback() || strings.EqualFold(listenHost, "localhost") {
			allowedHost = strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
		}
		if ip := net.ParseIP(listenHost); ip != nil && ip.IsUnspecified() {
			if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
				localHost, _, _ := net.SplitHostPort(local.String())
				allowedHost = host == localHost
			}
		}
		if err != nil || port != listenPort || !allowedHost {
			http.Error(w, "unexpected dashboard Host", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Scheme != "http" || !strings.EqualFold(parsed.Host, r.Host) || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
				http.Error(w, "cross-origin dashboard request rejected", http.StatusForbidden)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "cross-site dashboard request rejected", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
