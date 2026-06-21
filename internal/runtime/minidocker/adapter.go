package minidocker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/runtime"
)

// MiniDockerAdapter implements runtime.RuntimeBackend using the Mini-Docker socket client.
type MiniDockerAdapter struct {
	client    *Client
	volumeDir string
}

// NewAdapter initializes a new MiniDockerAdapter.
func NewAdapter(socketPath string, volumeDir string) *MiniDockerAdapter {
	return &MiniDockerAdapter{
		client:    NewClient(socketPath),
		volumeDir: volumeDir,
	}
}

// CreateContainer creates a container in Mini-Docker.
func (a *MiniDockerAdapter) CreateContainer(ctx context.Context, cfg *api.ServiceConfig, name string) (string, error) {
	// Construct the port bindings
	portBindings := make(map[string][]PortBinding)
	for _, portMap := range cfg.Ports {
		containerKey := fmt.Sprintf("%d/tcp", portMap.Container)
		portBindings[containerKey] = []PortBinding{
			{HostPort: strconv.Itoa(portMap.Host)},
		}
	}

	// Construct the volume binds
	var binds []string
	for _, vol := range cfg.Volumes {
		hostPath := filepath.Join(a.volumeDir, vol.Name)
		if err := os.MkdirAll(hostPath, 0755); err != nil {
			return "", fmt.Errorf("failed to create host volume directory %s: %w", hostPath, err)
		}
		binds = append(binds, fmt.Sprintf("%s:%s:rw", hostPath, vol.MountPath))
	}

	var envs []string
	for k, v := range cfg.Environment {
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}

	req := CreateContainerRequest{
		Image: cfg.Image,
		Cmd:   cfg.Command,
		Name:  name,
		Env:   envs,
		HostConfig: CreateHostConfig{
			PortBindings: portBindings,
			Binds:        binds,
		},
	}

	var resp CreateContainerResponse
	err := a.client.Post(ctx, "/containers/create", req, &resp)
	if err != nil {
		return "", fmt.Errorf("failed to create container via Mini-Docker: %w", err)
	}

	if resp.Error != "" {
		return "", fmt.Errorf("Mini-Docker container creation error: %s", resp.Error)
	}

	if resp.ID == "" {
		return "", fmt.Errorf("Mini-Docker returned an empty container ID")
	}

	return resp.ID, nil
}

// StartContainer starts the container.
func (a *MiniDockerAdapter) StartContainer(ctx context.Context, id string) error {
	path := fmt.Sprintf("/containers/%s/start", id)
	err := a.client.Post(ctx, path, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to start container %s: %w", id, err)
	}
	return nil
}

// StopContainer stops the container.
func (a *MiniDockerAdapter) StopContainer(ctx context.Context, id string) error {
	path := fmt.Sprintf("/containers/%s/stop", id)
	err := a.client.Post(ctx, path, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", id, err)
	}
	return nil
}

// RestartContainer restarts the container.
func (a *MiniDockerAdapter) RestartContainer(ctx context.Context, id string) error {
	path := fmt.Sprintf("/containers/%s/restart", id)
	err := a.client.Post(ctx, path, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to restart container %s: %w", id, err)
	}
	return nil
}

// RemoveContainer removes the container and cleans up associated volumes.
func (a *MiniDockerAdapter) RemoveContainer(ctx context.Context, id string) error {
	// force=true to stop running container first; v=true to delete volumes
	path := fmt.Sprintf("/containers/%s?force=true&v=true", id)
	err := a.client.Delete(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to remove container %s: %w", id, err)
	}
	return nil
}

// InspectContainer inspects the container state and returns a generic ContainerInfo.
func (a *MiniDockerAdapter) InspectContainer(ctx context.Context, id string) (*runtime.ContainerInfo, error) {
	path := fmt.Sprintf("/containers/%s/json", id)
	var c MiniDockerContainer
	err := a.client.Get(ctx, path, &c)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %s: %w", id, err)
	}

	// Map status to ContainerState
	var state runtime.ContainerState
	switch strings.ToLower(c.Status) {
	case "created":
		state = runtime.StateCreated
	case "running":
		state = runtime.StateRunning
	case "stopped", "exited":
		state = runtime.StateStopped
	default:
		state = runtime.StateError
	}

	// Map port mappings back to API structure
	var ports []api.PortMapping
	for _, portStr := range c.Network.Ports {
		parts := strings.Split(portStr, ":")
		if len(parts) == 2 {
			hostPort, err1 := strconv.Atoi(parts[0])
			containerPort, err2 := strconv.Atoi(parts[1])
			if err1 == nil && err2 == nil {
				ports = append(ports, api.PortMapping{
					Host:      hostPort,
					Container: containerPort,
				})
			}
		}
	}

	return &runtime.ContainerInfo{
		ID:        c.ID,
		Name:      c.Name,
		Image:     c.RootFS,
		State:     state,
		IPAddress: c.Network.IP,
		Ports:     ports,
		ExitCode:  c.ExitCode,
	}, nil
}

// StreamLogs returns a stream of container logs from Mini-Docker.
func (a *MiniDockerAdapter) StreamLogs(ctx context.Context, id string, follow bool, tail int) (io.ReadCloser, error) {
	path := fmt.Sprintf("/containers/%s/logs?follow=%t&timestamps=true", id, follow)
	if tail > 0 {
		path = fmt.Sprintf("%s&tail=%d", path, tail)
	}

	stream, err := a.client.Stream(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to stream logs from Mini-Docker for container %s: %w", id, err)
	}
	return stream, nil
}

// ListContainers lists all containers in Mini-Docker.
func (a *MiniDockerAdapter) ListContainers(ctx context.Context) ([]*runtime.ContainerInfo, error) {
	var list []MiniDockerContainer
	err := a.client.Get(ctx, "/containers/json", &list)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers via Mini-Docker: %w", err)
	}

	var infos []*runtime.ContainerInfo
	for _, c := range list {
		var state runtime.ContainerState
		switch strings.ToLower(c.Status) {
		case "created":
			state = runtime.StateCreated
		case "running":
			state = runtime.StateRunning
		case "stopped", "exited":
			state = runtime.StateStopped
		default:
			state = runtime.StateError
		}

		var ports []api.PortMapping
		for _, portStr := range c.Network.Ports {
			parts := strings.Split(portStr, ":")
			if len(parts) == 2 {
				hostPort, err1 := strconv.Atoi(parts[0])
				containerPort, err2 := strconv.Atoi(parts[1])
				if err1 == nil && err2 == nil {
					ports = append(ports, api.PortMapping{
						Host:      hostPort,
						Container: containerPort,
					})
				}
			}
		}

		infos = append(infos, &runtime.ContainerInfo{
			ID:        c.ID,
			Name:      c.Name,
			Image:     c.RootFS,
			State:     state,
			IPAddress: c.Network.IP,
			Ports:     ports,
			ExitCode:  c.ExitCode,
		})
	}
	return infos, nil
}
