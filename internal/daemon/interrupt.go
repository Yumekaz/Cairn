package daemon

import (
	"context"
	"errors"
)

// isWorkflowInterrupted reports daemon shutdown / parent cancel during a step.
// These must not mark the Cairn deploy as failed — DuraFlow should resume the
// incomplete run after cairnd restarts.
func isWorkflowInterrupted(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled)
}
