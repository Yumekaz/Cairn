package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/yumekaz/cairn/internal/api"
)

// RunHealthCheck blocks until the service is healthy or the retries are exhausted.
func RunHealthCheck(ctx context.Context, hc *api.HealthCheckConfig, ipAddress string, containerPort int) error {
	if hc == nil {
		return nil // No health check configured, default to success
	}

	interval := hc.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timeout := hc.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	retries := hc.Retries
	if retries <= 0 {
		retries = 3
	}

	// Wait for startup grace period if configured
	if hc.StartupGrace > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(hc.StartupGrace):
		}
	}

	if hc.HTTPPath == "TCP" {
		var lastErr error
		for i := 0; i < retries; i++ {
			d := net.Dialer{Timeout: timeout}
			conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", ipAddress, containerPort))
			if err == nil {
				conn.Close()
				return nil // Container is healthy
			}
			lastErr = err

			if i < retries-1 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(interval):
				}
			}
		}
		return fmt.Errorf("TCP health check failed for %s:%d: %w", ipAddress, containerPort, lastErr)
	}

	path := hc.HTTPPath
	if path == "" {
		path = "/"
	}
	url := fmt.Sprintf("http://%s:%d%s", ipAddress, containerPort, path)

	client := &http.Client{
		Timeout: timeout,
	}

	var lastErr error
	for i := 0; i < retries; i++ {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					resp.Body.Close()
					return nil // Container is healthy
				}
				resp.Body.Close()
				lastErr = fmt.Errorf("unhealthy HTTP status: %d", resp.StatusCode)
			} else {
				lastErr = err
			}
		} else {
			lastErr = err
		}

		if i < retries-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
		}
	}

	return fmt.Errorf("health check failed for %s: %w", url, lastErr)
}
