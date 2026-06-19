# Cairn PaaS

Cairn is a CLI-first, stateful-first, self-hosted backend Platform-as-a-Service (PaaS). It is designed to run on a local system, using **Mini-Docker** as its container runtime backend.

## Architecture

- **CLI (`cairn`)**: Communicates with the local daemon via a Unix domain socket.
- **Daemon (`cairnd`)**: Manages SQLite metadata, registers service lifecycles, and triggers namespace container orchestration through Mini-Docker.
- **Runtime Adapter**: Abstracted interface mapping Cairn service configs to Mini-Docker container actions.

## Development

### Prerequisites

- Go (v1.26+)
- Python 3.14+ (for Mini-Docker)

### Building the Project

To build both the `cairn` CLI and the `cairnd` daemon:

```bash
make build
```

The compiled binaries will be outputted to the `bin/` directory.

### Running the Daemon

```bash
make run-daemon
```

### Running the CLI

```bash
make run-cli
```
