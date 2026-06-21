# Runtime Adapter Interface

Cairn remains runtime-agnostic through the `RuntimeBackend` interface, allowing you to swap out or support alternative containerization backends (like standard Docker, Podman, or host-process supervision) without modifying Cairn core daemon logic.

---

## ⚙️ The `RuntimeBackend` Interface

Defined in `internal/runtime/runtime.go`:

```go
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
```

### Methods Explanation

1. **`CreateContainer`**: Provision container overlay directory structures, allocate network addresses, setup loopback/bridge interface requirements, and register the container metadata configuration.
2. **`StartContainer`**: Spin up the namespace-isolated runtime process, bind network interfaces, and run migrations or health probes.
3. **`StopContainer`**: Terminate the runtime container process cleanly using SIGTERM/SIGKILL signal escalations.
4. **`RestartContainer`**: Force cycle a running container process to pick up updated configurations or reload system assets.
5. **`RemoveContainer`**: Safely stop and purge all transient namespace containers, cleaning up network routes, rootfs mounts, and metadata records.
6. **`InspectContainer`**: Read current active state (Created, Running, Stopped, Error), assigned IP addresses, host port mappings, and exit codes.
7. **`StreamLogs`**: Attach an stream reader to fetch logs (stdout/stderr) from the container's standard output.
8. **`ListContainers`**: Query the container list currently active or registered on the host runtime.

---

## 📦 Container Lifecycle States

The interface models container state using `ContainerState` constants:

- **`StateCreated`**: The container namespace and filesystem layers are created, but no process has started.
- **`StateRunning`**: The container process is executing successfully.
- **`StateStopped`**: The container process has completed or was stopped manually by command.
- **`StateError`**: The container run/initialization failed.

---

## 🛠️ Implementing a Custom Adapter

To write a new adapter (e.g., standard Docker SDK adapter):

1. Implement the `RuntimeBackend` interface methods inside a new package under `internal/runtime/`.
2. Map the adapter's struct configuration flags in `internal/config/config.go`.
3. Swap the initialization code inside `cmd/cairnd/main.go` to construct your adapter when selected by environment flags.
