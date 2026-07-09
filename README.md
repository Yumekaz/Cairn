# 🏔️ Cairn PaaS

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue.svg)](https://golang.org)
[![Python Version](https://img.shields.io/badge/Python-3.10%2B-blue.svg)](https://python.org)

**Cairn** is a CLI-first, single-node Platform-as-a-Service (PaaS) designed to run stateful backends, databases, and cron jobs with absolute operational honesty. Built directly on top of the **Mini-Docker** container runtime using a decoupled adapter interface, Cairn offers automated rollouts, hardware-encrypted secrets, logical database backups, and a resilient workflow orchestration engine.

---

## ✨ Features

- 🌐 **Virtual-Host HTTP Reverse Proxy Router (Phase 4)**: Maps custom domains and virtual hosts (e.g., `*.localhost`) directly to isolated container IPs. Features automatic 503 "Service Unavailable" fallback pages when containers are unhealthy.
- 🔒 **Secure Environment & Secrets Manager (Phase 7)**: Encrypts application secrets on the host using hardware-tied **AES-GCM** encryption. Automatically injects decrypted secrets during rollout workflows.
- 🗄️ **First-Class Database Service Drivers (Phase 12)**: Out-of-the-box support for PostgreSQL (`pg_dump`), Redis (`rdb`), and MongoDB (`mongodump`/`mongorestore`). Executes atomic logical backup/restores rather than generic volume tarballs.
- 🔄 **Durable Rollout Workflows (Phase 13-14)**: Built on the **DuraFlow** workflow engine. Executes multi-step ACID deployments (prepare, migrate, build, health check, route traffic) with automatic zero-downtime rollback if health checks fail.
- ⏱️ **Cron Jobs & Background Workers (Phase 9)**: Native daemon-scheduled cron jobs and headless worker services with automatic history tracking in SQLite.
- 📊 **Visual Web Dashboard (Phase 15)**: A built-in web dashboard (listening by default on port `2476`) offering real-time monitoring of deployments, container status, persistent volumes, logs, and system events.
- 🧪 **FailForge Resiliency Testing (Phase 11)**: Integrated verification module simulating system failures, container crashes, and disk corruption to prove system reliability.
- 🚀 **Automated Unix Installer (Phase 16)**: Single-command compilation, path configuration, and user-space deployment.

---

## 🛠️ System Requirements

- **Operating System**: Linux (Ubuntu/Debian recommended)
- **Kernel Modules**: `overlay` (OverlayFS active)
- **Tooling**: Go `v1.22+` (to compile binaries), Python `v3.10+` (to run the Mini-Docker runtime)

---

## 🚀 Quickstart Installation

You can compile and deploy the Cairn PaaS control plane using the automated installer script:

```bash
# 1. Clone the repository
git clone https://github.com/Yumekaz/Cairn.git && cd Cairn

# 2. Run the installer script
./scripts/install.sh
```

The installer will compile the `cairn` CLI client and `cairnd` control plane daemon, initialize configuration paths under `~/.cairn/`, and place the binaries in `$HOME/.local/bin`.

### Running the Services

1. **Start the Mini-Docker Daemon** (required to manage container namespaces):
   ```bash
   sudo python3 -m mini_docker daemon --socket-mode 666
   ```

2. **Initialize the Cairn SQLite Metadata Store**:
   ```bash
   cairn init
   ```

3. **Start the Cairn Control Plane Daemon**:
   ```bash
   cairnd
   ```
   *The daemon starts the background DuraFlow workers, the cron scheduler, and the Web Dashboard/API server on `http://127.0.0.1:2476`.*

---

## 📖 Configuration Reference (`cairn.yaml`)

Cairn services are configured using a declarative `cairn.yaml` specification. Below is a complete PostgreSQL database service example with migrations and health checks:

```yaml
name: core-db
kind: postgres

runtime:
  backend: minidocker
  image_or_rootfs: "postgres:15-alpine"
  command: ["docker-entrypoint.sh", "postgres"]

environment:
  POSTGRES_USER: "cairn_user"
  POSTGRES_DB: "cairn_prod"

volumes:
  - name: db-data
    mount: /var/lib/postgresql/data

migrations:
  command: ["sh", "-c", "psql -h $DB_HOST -U $POSTGRES_USER -d $POSTGRES_DB -f /var/lib/postgresql/data/migrations.sql"]
  timeout: 30s

healthcheck:
  type: http
  path: /health
  interval: 5s
  timeout: 2s
  retries: 3
  startup_grace: 15s

restart:
  policy: always
```

---

## 💻 CLI Commands

### Service Management
```bash
# Deploy a service config
cairn deploy ./examples/counter-api/cairn.yaml

# List running containers
cairn ps

# Stop or restart a service
cairn stop counter-api
cairn restart counter-api

# Stream service logs
cairn logs counter-api --follow
```

### Environment Variables & Secrets
```bash
# Set a standard environment variable
cairn env set counter-api KEY_NAME value

# Set a hardware-encrypted secret variable
cairn secret set counter-api SECRET_KEY confidential_value

# List service environment keys (secrets are automatically masked)
cairn env list counter-api
```

### Backups & Restores
```bash
# Create a volume backup (Logical for DBs, Tarball for files)
cairn backup create counter-data

# List volume backup snapshots
cairn backup list counter-data

# Restore a volume from snapshot
cairn restore counter-data <backup_id>
```

### System Audits
```bash
# Stream the global system event log
cairn events
```

---

## 📐 Architecture Overview

```text
       Developer / CLI
              │
              ▼
    ┌───────────────────┐
    │  Cairn Daemon     │  ◄───►  [SQLite Metadata Store]
    │  (Port 2476)      │
    └─────────┬─────────┘
              │  routes workflows via
              ▼
    ┌───────────────────┐
    │  DuraFlow Engine  │  ◄───►  [duraflow.db]
    └─────────┬─────────┘
              │  schedules execution
              ▼
    ┌───────────────────┐
    │ Runtime Interface │
    └─────────┬─────────┘
              │  maps container namespaces
              ▼
    ┌───────────────────┐
    │ Mini-Docker Sock  │
    └─────────┬─────────┘
              │  spins up sandboxed processes
              ▼
    ┌───────────────────┐
    │ Linux Workloads   │
    └───────────────────┘
```

---

## 📄 License

Distributed under the MIT License. See [LICENSE](LICENSE) for more information.

---

## 🔒 Security

For security vulnerability reporting, please see [SECURITY.md](SECURITY.md).
