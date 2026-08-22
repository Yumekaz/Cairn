package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/runtime"
)

// stubRuntime embeds the interface so only InspectContainer needs behavior;
// any other method call panics, which is fine because waitForTaskExit never
// touches them.
type stubRuntime struct {
	runtime.RuntimeBackend
	infos []*runtime.ContainerInfo
	calls int
}

func (s *stubRuntime) InspectContainer(ctx context.Context, id string) (*runtime.ContainerInfo, error) {
	idx := s.calls
	if idx >= len(s.infos) {
		idx = len(s.infos) - 1
	}
	s.calls++
	info := s.infos[idx]
	return &runtime.ContainerInfo{State: info.State, ExitCode: info.ExitCode}, nil
}

func exitPtr(code int) *int { return &code }

func TestWaitForTaskExitReturnsExitCode(t *testing.T) {
	stub := &stubRuntime{infos: []*runtime.ContainerInfo{
		{State: runtime.StateRunning},
		{State: runtime.StateStopped, ExitCode: exitPtr(0)},
	}}
	s := &Server{runtime: stub}
	code, err := s.waitForTaskExit(context.Background(), "task-1", 5*time.Second)
	if err != nil || code != 0 {
		t.Fatalf("expected exit 0 nil error, got code=%d err=%v", code, err)
	}
}

func TestWaitForTaskExitTimesOut(t *testing.T) {
	stub := &stubRuntime{infos: []*runtime.ContainerInfo{
		{State: runtime.StateRunning},
	}}
	s := &Server{runtime: stub}
	start := time.Now()
	code, err := s.waitForTaskExit(context.Background(), "task-1", 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got code=%d err=%v", code, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}

func TestWaitForTaskExitRespectsContextCancel(t *testing.T) {
	stub := &stubRuntime{infos: []*runtime.ContainerInfo{
		{State: runtime.StateRunning},
	}}
	s := &Server{runtime: stub}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	code, err := s.waitForTaskExit(ctx, "task-1", 30*time.Second)
	if err == nil || (err != context.Canceled && !strings.Contains(err.Error(), "context canceled")) {
		t.Fatalf("expected context cancel error, got code=%d err=%v", code, err)
	}
}
