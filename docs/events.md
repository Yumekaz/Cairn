# Cairn event types

Audit events are stored in SQLite and listed via `cairn events` / `cairn events <service>`.

## Implemented (emitted by control plane)

| Type | When |
| --- | --- |
| `DaemonStarted` / `DaemonStopped` | Daemon lifecycle |
| `ServiceCreated` | First deploy of a new service name |
| `ServiceStarted` / `ServiceStopped` / `ServiceRestarted` / `ServiceRemoved` | Service actions |
| `DeployStarted` / `DeploySucceeded` / `DeployCompleted` / `DeployFailed` | Deploy workflow |
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

## Deferred / not emitted as separate types yet

- Fine-grained migration sub-steps beyond deploy fail/success
- Continuous post-deploy probe failures as `HealthCheckFailed` (deploy-time health is covered)
- Multi-node events (Phase 18)

## Demo assertion

After `./scripts/clean_demo.sh`, expected types for a full counter-api cycle include at least:

`DeployStarted`, `RuntimeCreateStarted`, `RuntimeCreateCompleted`, `HealthCheckPassed` (or fail path), `RouteUpdated` or `RoutePreserved`+`DeployFailed`, `DeploySucceeded`/`DeployCompleted`, `BackupStarted`, `BackupSucceeded`, `RestoreStarted`, `RestoreCompleted`.
