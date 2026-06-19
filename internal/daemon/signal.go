package daemon

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// SetupSignalContext returns a context that is cancelled when SIGINT or SIGTERM is received.
func SetupSignalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		cancel()
	}()

	return ctx, cancel
}
