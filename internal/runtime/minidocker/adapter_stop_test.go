package minidocker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type stopContractTransport func(*http.Request) (*http.Response, error)

func (f stopContractTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStopContainerNeverConvertsRefusalOrCleanupFailureToSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, inspectState, reason string
		status                    int
	}{
		{"uncertain_identity", "unknown", "Unverified process identity; no signal sent", 409},
		{"stopped_cleanup_failure", "stopped", "Container stopped, but runtime cleanup failed", 500},
		{"stopped_writer_fence_failure", "stopped", "Verified supervisor has not finished stopping", 500},
		{"lost_ack_is_not_cleanup_proof", "stopped", "acknowledgement outcome unknown", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspects, stops := 0, 0
			a := &MiniDockerAdapter{client: &Client{httpClient: &http.Client{Transport: stopContractTransport(func(r *http.Request) (*http.Response, error) {
				code, body := http.StatusOK, `{"id":"owned","status":"`+tc.inspectState+`","network":{"ip":"10.0.0.9"}}`
				if r.Method == http.MethodPost && r.URL.Path == "/containers/owned/stop" {
					stops++
					code, body = tc.status, `{"error":"`+tc.reason+`"}`
				} else if r.Method == http.MethodGet && r.URL.Path == "/containers/owned/json" {
					inspects++
				} else {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}}}
			err := a.StopContainer(context.Background(), "owned")
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("runtime refusal must survive, got %v", err)
			}
			if stops != 1 || inspects != 0 {
				t.Fatalf("generic inspection cannot certify Stop; stops=%d inspects=%d", stops, inspects)
			}
		})
	}
}

func TestStopContainerAcceptsAnAuthoritativeSuccessfulAcknowledgement(t *testing.T) {
	calls := 0
	a := &MiniDockerAdapter{client: &Client{httpClient: &http.Client{Transport: stopContractTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/containers/owned/stop" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}}}
	if err := a.StopContainer(context.Background(), "owned"); err != nil || calls != 1 {
		t.Fatalf("successful acknowledgement rejected: calls=%d err=%v", calls, err)
	}
}
