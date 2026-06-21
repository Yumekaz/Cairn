package minidocker

// CreateContainerRequest represents the payload for creating a container in Mini-Docker.
type CreateContainerRequest struct {
	Image      string            `json:"Image"`
	Cmd        []string          `json:"Cmd,omitempty"`
	Name       string            `json:"name"`
	Env        []string          `json:"Env,omitempty"`
	HostConfig CreateHostConfig  `json:"HostConfig,omitempty"`
}

// CreateHostConfig represents host-level configuration (e.g. port bindings).
type CreateHostConfig struct {
	PortBindings map[string][]PortBinding `json:"PortBindings,omitempty"`
	Binds        []string                 `json:"Binds,omitempty"`
}

// PortBinding maps a container port to a host port.
type PortBinding struct {
	HostPort string `json:"HostPort"`
}

// CreateContainerResponse represents the ID returned on successful creation.
type CreateContainerResponse struct {
	ID    string `json:"Id,omitempty"`
	Error string `json:"error,omitempty"`
}

// MiniDockerContainer represents the container data returned by the inspect endpoint.
type MiniDockerContainer struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	RootFS    string             `json:"rootfs"`
	Command   []string           `json:"command"`
	Status    string             `json:"status"` // e.g. "created", "running", "stopped"
	ExitCode  *int               `json:"exit_code,omitempty"`
	Network   MiniDockerNetwork  `json:"network"`
	Volumes   []MiniDockerVolume `json:"volumes,omitempty"`
}

// MiniDockerNetwork represents the network config inside MiniDockerContainer.
type MiniDockerNetwork struct {
	IP    string   `json:"ip"`
	Ports []string `json:"ports"` // e.g. ["8080:80"]
}

// MiniDockerVolume represents bind mounts inside MiniDockerContainer.
type MiniDockerVolume struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
}
