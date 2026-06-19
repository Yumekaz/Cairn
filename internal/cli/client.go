package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

// DaemonClient is the client used by the CLI to query the cairnd Unix socket.
type DaemonClient struct {
	httpClient *http.Client
}

// NewDaemonClient returns a client instance configured for the specified socket.
func NewDaemonClient(socketPath string) *DaemonClient {
	return &DaemonClient{
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// GetDefaultSocketPath returns the standard socket location.
func GetDefaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/home/yumekaz"
	}
	return filepath.Join(home, ".cairn", "cairnd.sock")
}

func (c *DaemonClient) execute(ctx context.Context, method, path string, body interface{}, target interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return fmt.Errorf("failed to encode request body: %w", err)
		}
		bodyReader = buf
	}

	url := "http://localhost" + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cairnd socket connection failed (is the daemon running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("daemon error (status %d): %s", resp.StatusCode, errResp.Error)
		}
		return fmt.Errorf("daemon request failed with status: %d", resp.StatusCode)
	}

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// Get performs a GET request to cairnd.
func (c *DaemonClient) Get(ctx context.Context, path string, target interface{}) error {
	return c.execute(ctx, http.MethodGet, path, nil, target)
}

// Post performs a POST request to cairnd.
func (c *DaemonClient) Post(ctx context.Context, path string, body interface{}, target interface{}) error {
	return c.execute(ctx, http.MethodPost, path, body, target)
}

// Delete performs a DELETE request to cairnd.
func (c *DaemonClient) Delete(ctx context.Context, path string, target interface{}) error {
	return c.execute(ctx, http.MethodDelete, path, nil, target)
}

// Stream performs a GET request to cairnd and returns the response body directly.
func (c *DaemonClient) Stream(ctx context.Context, path string) (io.ReadCloser, error) {
	url := "http://localhost" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cairnd connection failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("daemon error (status %d): %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("daemon request failed with status: %d", resp.StatusCode)
	}

	return resp.Body, nil
}
