package minidocker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

// Client is a Unix socket HTTP client for the Mini-Docker API.
type Client struct {
	httpClient *http.Client
}

// NewClient initializes a new Client pointing to the Unix socket.
func NewClient(socketPath string) *Client {
	return &Client{
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

// execute performs the HTTP request and handles JSON parsing or HTTP status check.
func (c *Client) execute(ctx context.Context, method, path string, body interface{}, target interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return fmt.Errorf("failed to encode request body: %w", err)
		}
		bodyReader = buf
	}

	// We use "http://localhost" as dummy host since it's communicating over a Unix socket
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
		return fmt.Errorf("unix socket connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		// Try to decode error response body
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("API error (status %d): %s", resp.StatusCode, errResp.Error)
		}
		return fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			return fmt.Errorf("failed to decode response body: %w", err)
		}
	}

	return nil
}

// Get performs a GET request and decodes the response into target.
func (c *Client) Get(ctx context.Context, path string, target interface{}) error {
	return c.execute(ctx, http.MethodGet, path, nil, target)
}

// Post performs a POST request.
func (c *Client) Post(ctx context.Context, path string, body interface{}, target interface{}) error {
	return c.execute(ctx, http.MethodPost, path, body, target)
}

// Delete performs a DELETE request.
func (c *Client) Delete(ctx context.Context, path string) error {
	return c.execute(ctx, http.MethodDelete, path, nil, nil)
}
