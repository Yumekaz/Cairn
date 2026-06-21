package runtime

import (
	"context"
	"io"

	"github.com/yumekaz/cairn/internal/api"
)

// ContainerState represents the lifecycle state of a container.
type ContainerState string

const (
	StateCreated ContainerState = "created"
	StateRunning ContainerState = "running"
	StateStopped ContainerState = "stopped"
	StateError   ContainerState = "error"
)

// ContainerInfo represents generic runtime container state.
type ContainerInfo struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	State     ContainerState    `json:"state"`
	IPAddress string            `json:"ip_address"`
	Ports     []api.PortMapping `json:"ports"`
	ExitCode  *int              `json:"exit_code,omitempty"`
}

// RuntimeBackend defines the abstract interface for managing containerized workloads.
type RuntimeBackend interface {
	CreateContainer(ctx context.Context, cfg *api.ServiceConfig, name string) (string, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string) error
	RestartContainer(ctx context.Context, id string) error
	RemoveContainer(ctx context.Context, id string) error
	InspectContainer(ctx context.Context, id string) (*ContainerInfo, error)
	StreamLogs(ctx context.Context, id string, follow bool, tail int) (io.ReadCloser, error)
	ListContainers(ctx context.Context) ([]*ContainerInfo, error)
}
