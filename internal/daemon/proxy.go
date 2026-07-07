package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/yumekaz/cairn/internal/api"
)

// ServeHTTP acts as the router/proxy multiplexer.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	// Strip port if present
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// Check if the request is destined for a local service route (e.g. <name>.localhost)
	if strings.HasSuffix(host, ".localhost") {
		serviceName := strings.TrimSuffix(host, ".localhost")

		// 1. Retrieve the service from database
		svc, err := s.store.GetServiceByName(serviceName)
		if err == nil && svc != nil {
			// If it exists but is not running or lacks runtime ID, serve 503
			if svc.ActualState != "running" || svc.RuntimeID == "" {
				s.serveUnavailablePage(w, serviceName)
				return
			}

			// 2. Load the service config to find the container port
			cfg, err := s.loadCurrentServiceConfig(svc)
			containerPort := 80 // Default fallback
			if err == nil && cfg != nil && len(cfg.Ports) > 0 {
				containerPort = cfg.Ports[0].Container
			}

			// 3. Inspect the container to get its IP
			info, err := s.runtime.InspectContainer(r.Context(), svc.RuntimeID)
			if err != nil || info.IPAddress == "" {
				log.Printf("cairnd proxy: failed to inspect container IP for service %s: %v", serviceName, err)
				s.serveUnavailablePage(w, serviceName)
				return
			}

			// 4. Reverse proxy the request to the container IP and port
			targetURL, err := url.Parse(fmt.Sprintf("http://%s:%d", info.IPAddress, containerPort))
			if err != nil {
				log.Printf("cairnd proxy: failed to parse destination URL for service %s: %v", serviceName, err)
				s.serveUnavailablePage(w, serviceName)
				return
			}

			proxy := httputil.NewSingleHostReverseProxy(targetURL)
			originalDirector := proxy.Director
			proxy.Director = func(req *http.Request) {
				originalDirector(req)
				req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
				req.Header.Set("X-Forwarded-Proto", "http")
			}

			proxy.ServeHTTP(w, r)
			return
		}
	}

	// Fallback to normal control plane dashboard/API router
	s.router.ServeHTTP(w, r)
}

func (s *Server) serveUnavailablePage(w http.ResponseWriter, serviceName string) {
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<title>Service Unavailable - Cairn</title>
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f8f9fa; color: #343a40; text-align: center; padding: 100px 20px; }
		.container { max-width: 500px; margin: 0 auto; background: white; padding: 40px; border-radius: 8px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); }
		h1 { color: #dc3545; font-size: 24px; margin-bottom: 20px; }
		p { font-size: 16px; line-height: 1.5; color: #6c757d; }
		.badge { background: #e9ecef; padding: 4px 8px; border-radius: 4px; font-family: monospace; font-size: 14px; }
	</style>
</head>
<body>
	<div class="container">
		<h1>Service Unavailable</h1>
		<p>The service <span class="badge">%s</span> is registered on Cairn but is currently stopped or unhealthy.</p>
	</div>
</body>
</html>`, serviceName)))
}

func (s *Server) loadCurrentServiceConfig(svc *api.Service) (*api.ServiceConfig, error) {
	if svc.CurrentDeployID == "" {
		return nil, fmt.Errorf("no deployment found for service %s", svc.Name)
	}
	cfgPath := filepath.Join(s.config.DataDir, "services", svc.Name, fmt.Sprintf("deploy_%s.json", svc.CurrentDeployID))
	file, err := os.Open(cfgPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cfg api.ServiceConfig
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
