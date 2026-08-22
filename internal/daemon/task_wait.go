package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/yumekaz/cairn/internal/runtime"
)

// backupTaskTimeout bounds every backup/restore task-container wait so a hung
// container cannot spin the poll loop forever.
const backupTaskTimeout = 10 * time.Minute

// waitForTaskExit polls the runtime until the task container stops, errors,
// the deadline expires, or ctx is canceled. It returns the container exit code.
func (s *Server) waitForTaskExit(ctx context.Context, taskID string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		info, err := s.runtime.InspectContainer(ctx, taskID)
		if err != nil {
			return -1, err
		}
		if info.State == runtime.StateStopped || info.State == runtime.StateError {
			if info.ExitCode != nil {
				return *info.ExitCode, nil
			}
			return -1, nil
		}
		if time.Now().After(deadline) {
			return -1, fmt.Errorf("timed out after %s waiting for task container %s (state: %s)", timeout, taskID, info.State)
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
