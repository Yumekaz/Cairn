package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsWorkflowInterrupted(t *testing.T) {
	if isWorkflowInterrupted(nil) {
		t.Fatal("nil must not be interrupt")
	}
	if !isWorkflowInterrupted(context.Canceled) {
		t.Fatal("context.Canceled must be interrupt")
	}
	if !isWorkflowInterrupted(fmt.Errorf("wrap: %w", context.Canceled)) {
		t.Fatal("wrapped Canceled must be interrupt")
	}
	if isWorkflowInterrupted(context.DeadlineExceeded) {
		t.Fatal("DeadlineExceeded is a step timeout class, not daemon interrupt for failDeploy skip")
	}
	if isWorkflowInterrupted(errors.New("health check failed")) {
		t.Fatal("normal errors must not be interrupt")
	}
}
