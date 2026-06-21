# Cairn Architecture

This document describes the internals, data flow, and components of Cairn PaaS.

---

## 🏛️ High-Level Overview

Cairn is a single-node Platform-as-a-Service structured as a client-server architecture:

```
┌──────────────────┐
│    cairn CLI     │◀───( REST over Unix Socket )
└──────────────────┘
         │
         ▼
┌────────────────────────────────────────────────────────────────────────┐
│ cairnd Daemon                                                          │
│                                                                        │
│   ┌──────────────────┐      ┌──────────────────┐      ┌────────────┐   │
│   │   REST API       │      │   DuraFlow       │      │   Cron     │   │
│   │   Handlers       │◀────▶│   Engine         │◀────▶│   Scheduler│   │
│   └──────────────────┘      └──────────────────┘      └────────────┘   │
│            │                          │                      │         │
│            ▼                          ▼                      ▼         │
│   ┌──────────────────┐      ┌──────────────────────────────────────┐   │
│   │   SQLite Store   │      │   RuntimeBackend Interface           │   │
│   │   (cairn.db)     │      │   (minidocker/adapter.go)            │   │
│   └──────────────────┘      └──────────────────────────────────────┘   │
└──────────────────────────────────────────┬─────────────────────────────┘
                                           │ (REST over Unix Socket)
                                           ▼
                              ┌────────────────────────┐
                              │  Mini-Docker Daemon    │
                              │  (Isolation, OverlayFS)│
                              └────────────────────────┘
```

---

## 🧩 Core Components

### 1. CLI Client (`cmd/cairn`)
The CLI is a Cobra-based Go application. Commands like `cairn deploy`, `cairn ps`, `cairn backup`, and `cairn status` map input arguments to JSON REST payloads and send them to the `cairnd` daemon over the UNIX domain socket (`~/.cairn/cairnd.sock`).

### 2. Control Daemon (`cmd/cairnd`)
The daemon runs the REST API server using Chi, listens on the UNIX socket, and serves the dashboard over a secondary TCP port (defaulting to `http://127.0.0.1:2476`). It coordinates:
- **DuraFlow Workflow Engine**: Coordinates stateful deploy, backup, and restore pipelines.
- **Service Reconciliation Loop**: A background routine running every 30 seconds to restart crashed containers, recover missing containers after reboots, and clean up dangling containers matching the `cairn-` prefix.
- **Cron Scheduler**: Spawns worker containers at interval cron schedules.

### 3. Metadata Store (`cairn.db`)
Cairn maintains its source of truth in a local SQLite database (`~/.cairn/cairn.db`). It contains tables for:
- `services`: Service IDs, names, current active deployment, desired states, actual states, routes.
- `deploys`: Logs of deployment attempts, failure reasons, configuration files, migration logs.
- `volumes`: Persistent volume records and host path mappings.
- `backups`: Backup archive metadata, timestamps, sizing, and checksums.
- `workflows` / `workflow_steps`: Execution states of durable DuraFlow pipelines.

### 4. DuraFlow Workflow Engine (`internal/duraflow/`)
Deploying or restoring a stateful service is error-prone. DuraFlow breaks these flows down into small, checkpointed transactional steps saved in SQLite.
- If the daemon crashes or restarts mid-deployment, the DuraFlow engine detects the in-flight workflow on reboot and resumes it from the exact step where it stopped.
- Example Deploy steps: `parse_config`, `resolve_environment`, `create_candidate_container`, `run_migrations`, `start_candidate`, `perform_health_checks`, `switch_route`, `stop_old_container`.

### 5. Runtime Backend Abstraction (`internal/runtime/`)
To remain runtime-agnostic, Cairn talks to container systems via the `RuntimeBackend` interface.
- It is decoupled from Docker SDKs or specific container runtimes.
- It exposes general primitives: `CreateContainer`, `StartContainer`, `StopContainer`, `RemoveContainer`, `InspectContainer`, `ListContainers`, and `StreamLogs`.
- The default implementation is the **Mini-Docker adapter**, which talks to the `mini_docker` daemon using a custom HTTP JSON API over `/var/run/mini-docker.sock`.
