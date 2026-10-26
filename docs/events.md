# Cairn event types

Audit events are stored in SQLite and listed via `cairn events` / `cairn events <service>`.

## Implemented (emitted by control plane)

| Type | When |
| --- | --- |
| `DaemonStarted` / `DaemonStopped` | Daemon lifecycle |
| `ServiceCreated` | First deploy of a new service name |
| `ServiceStarted` / `ServiceStopped` / `ServiceRestarted` / `ServiceRemoved` | Service actions; **reconcile** auto-recover also emits `ServiceRestarted` (`container_stopped` / `container_missing`). Crash-loop (default 5 auto-restarts in 10m, process-local) sets desired=stopped and emits `ServiceStopped` with a crash-loop message. Manual `cairn start` / `restart` resets the counter. |
| `DeployStarted` / `DeploySucceeded` / `DeployCompleted` / `DeployFailed` | Deploy workflow |
| `RollbackBlocked` / `RollbackForced` | Unsafe rollback across intervening `state_touched` (migration) deploys; force bypass |
| `RuntimeCreateStarted` / `RuntimeCreateCompleted` | Candidate container create |
| `HealthCheckPassed` / `HealthCheckFailed` | Deploy health step |
| `RouteUpdated` | Successful promote / traffic shift |
| `RoutePreserved` | Failed deploy; previous `current_deploy_id` kept |
| `VolumeCreated` / `VolumeAttached` / `VolumeRemoved` | Volume lifecycle |
| `BackupStarted` / `BackupSucceeded` / `BackupCompleted` / `BackupFailed` | Backup |
| `RestoreStarted` / `BackupRestored` / `RestoreCompleted` / `RestoreFailed` | Restore |

### Aliases (MLP §17 naming)

| MLP name | Canonical emit |
| --- | --- |
| `DeployCompleted` | Also emitted after `DeploySucceeded` |
| `BackupCompleted` | Also emitted after `BackupSucceeded` |
| `RestoreCompleted` | Emitted with `BackupRestored` on success |

## Rollback safety policy

Unsafe = intervening deploys with `state_touched=true`, including failed or
interrupted deployments. This durable marker is saved **before** migration
execution because an unsuccessful migration may already have changed data.
It is a conservative migration-risk signal, not tracking of arbitrary volume writes.

- Without `--force`: HTTP 409 + CLI warning + `RollbackBlocked` event; `current_deploy_id` unchanged.
- With `--force`: `RollbackForced` then normal rollback deploy workflow.

Scripted proof: `./scripts/rollback_safety_demo.sh` (optional `FORCE=1`).

For cron services, Start/Restart reactivate an existing valid schedule as
`active`; Stop records `stopped` and prevents future task admission. Jobs already
started continue. These actions emit the same service lifecycle event types and
do not create a long-running service container or reconstruct a deleted schedule.

## Deferred / not emitted as separate types yet

- Fine-grained migration sub-steps beyond deploy fail/success
- Continuous post-deploy probe failures as `HealthCheckFailed` (deploy-time health is covered)
- Separate `CrashLoopDetected` type (today: `ServiceStopped` + crash-loop message)
- Multi-node events (Phase 18)

## Demo assertion

After `./scripts/clean_demo.sh`, expected types for a full counter-api cycle include at least:

`DeployStarted`, `RuntimeCreateStarted`, `RuntimeCreateCompleted`, `HealthCheckPassed` (or fail path), `RouteUpdated` or `RoutePreserved`+`DeployFailed`, `DeploySucceeded`/`DeployCompleted`, `BackupStarted`, `BackupSucceeded`, `RestoreStarted`, `RestoreCompleted`.
