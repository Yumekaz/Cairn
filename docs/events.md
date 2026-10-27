# Cairn event types

Audit events are stored in SQLite and listed via `cairn events` / `cairn events <service>`.

This catalog includes the current safety changes under validation.
`RuntimeCleanupFailed` is new source behavior, not a capability proved by the
frozen `9126ce1`/`bf1f63f` installation. See the
[hardening checkpoint](HARDENING_2026-10.md) for provenance and open gates.

## Implemented (emitted by control plane)

| Type | When |
| --- | --- |
| `DaemonStarted` / `DaemonStopped` | Daemon lifecycle |
| `ServiceCreated` | First deploy of a new service name |
| `ServiceStarted` / `ServiceStopped` / `ServiceRestarted` / `ServiceRemoved` | Service actions; **reconcile** auto-recover also emits `ServiceRestarted` (`container_stopped` / `container_missing`). Crash-loop (default 5 auto-restarts in 10m, process-local) sets desired=stopped and emits `ServiceStopped` with a crash-loop message. Manual `cairn start` / `restart` resets the counter. |
| `DeployStarted` / `DeploySucceeded` / `DeployCompleted` / `DeployFailed` | Deploy workflow |
| `RollbackBlocked` / `RollbackForced` | Unsafe rollback across intervening `state_touched` (migration) deploys; force bypass |
| `RuntimeCreateStarted` / `RuntimeCreateCompleted` | Candidate container create |
| `RuntimeCleanupFailed` | Runtime retirement Stop or Remove failed; warning metadata identifies the runtime and failed phase. Ownership is retained for verification/retry. See below. |
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

## Runtime cleanup warning and uncertainty

`RuntimeCleanupFailed` records failed deployment-runtime retirement, including
old-generation cleanup and rejected-candidate cleanup. It is associated with
the service and deployment; `metadata_json` contains `runtime_id`, `phase`
(`stop` or `remove`) and `error`. The warning is persisted in SQLite, not merely
printed to a transient log. A warning-persistence error is also an error, not
proof that retirement completed.

Normal web cutover is availability-first: it can commit a healthy candidate
and emit deployment success even if retiring the previous runtime fails.
`DeploySucceeded`, `DeployCompleted` and `RouteUpdated` therefore do **not**
certify that every old runtime was removed. The warning leaves the old runtime's
ownership available for inspection and retry. Conversion to cron instead
refuses to proceed when previous-runtime retirement fails.

A refused Stop is never converted to success from a generic stopped/error
inspection, and Remove is not attempted after that refusal. Physical restore
re-establishes authoritative Stop acknowledgements for the primary and exact
recorded historical deployment/job runtimes before destructive file changes,
including replay of the extraction step. Missing identity, uncertain job
completion or failed inventory blocks mutation. Service deletion likewise
retains its records when job completion, Stop or Remove is unverified; it must
not emit a successful removal as a substitute for cleanup.

These are scoped single-owner safeguards under verification, not global
transactional restore or a guarantee about arbitrary external writers. Restore
restart also re-reads current desired state and must not resurrect a service
whose desired state is stopped. PostgreSQL/MongoDB logical restore is a separate
path; deployment rollback is not database undo.

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
