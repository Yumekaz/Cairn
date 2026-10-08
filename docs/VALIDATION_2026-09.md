# Validation record — September 2026

This is an evidence record, not a claim that the six-project suite is fault-free.
The latest repeatable run is retained under `validation-runs/<UTC stamp>/` by
[`scripts/acceptance_suite.sh`](../scripts/acceptance_suite.sh). It records full
repository revisions, dirty-tree state, per-check exit codes and bounded logs.
Checks that need a VM, root, external database, long observation period, or
independent security review are reported as blocked rather than passed.

## Cairn dashboard — live browser check, 2026-09-25

Test target: the live local daemon, disposable service `notes-proof-62a235f5`,
and attached test volume `notes-proof-62a235f5-data`.

| Flow/control | Observed result |
| --- | --- |
| Overview, Services, Volumes & Backups, Events navigation | Passed; live daemon status and data rendered |
| Service search | Passed; exact test-service match showed `1 of 4`, unknown name showed `0 of 4` and “No matches” |
| Service inspection | Passed; ID, kind, Mini-Docker runtime, route, lifecycle state, deployment history and console output rendered |
| Service Stop / Start / Restart | Passed on the disposable service; Stop appeared in audit history and reduced Active from 2 to 1; Start restored Active to 2; Restart produced a `ServiceRestarted` event and the service remained RUNNING |
| Console Refresh / Clear / Wrap / Follow | Passed for refresh and visible output, local-view-only clear message, wrap class toggle, and follow checkbox toggle |
| Service modal keyboard behavior | Passed; Tab stayed in the dialog, Escape closed it, and focus returned to the originating Inspect button |
| Volume Inspect / Backup Now | Passed; selected the test volume and created successful backup prefix `b8defe70-08f` |
| Backup restore guard | Passed; Proceed began disabled, acknowledgement enabled it, unchecking disabled it, and Cancel/Escape closed the dialog |
| Safe rollback path | Passed on the disposable service; selecting older successful deploy `bf52a3c7` created active deploy `63658fc4`; selecting the original `238d1c4f` created active deploy `39b31f97`. The service remained running. Both source releases were version 1. |
| Event type filter / Refresh | Passed; ServiceStopped and ServiceRestarted filters rendered only their selected event types; Refresh retained the selected filter and audit records |
| Event Auto-scroll | Passed; with auto-scroll off, refreshing from the top kept the view at the top; with it on, refresh moved to the latest event |
| Daemon offline / reconnect | Passed in the earlier local run: offline banner and disabled mutations appeared, then Retry now restored Connected and refreshed state |

The Restore Proceed action was not exercised in that September browser session.
It has since been verified on an explicitly disposable notes fixture, including
the application-data result; see [the October record](VALIDATION_2026-10.md).

Not covered by this browser session: the unsafe-migration rollback confirmation
path, metadata expansion on individual events, every modal close affordance, or
offline behavior under an actual network partition. The safe rollback path does
not establish that every database migration is reversible.

## Automated project suites

| Project | Current evidence |
| --- | --- |
| Cairn | Full `go test ./...` is included in the retained acceptance run; the live migration/recovery and dashboard work is recorded separately. |
| DuraFlow | `go test ./...`; its PostgreSQL integration test requires `DURAFLOW_TEST_POSTGRES_DSN` and is not counted as proven when that variable is absent. |
| Mini-Docker | `python3 -m pytest -q`; non-root runs report six root-only skips. A separate privileged run is required to close that gate. |
| FailForge | `go test ./...`; CPU pause and slow-disk fault recovery also had repeated runs in the earlier local proof. |
| MiniDB | `python3 -m pytest -q tests`, including small-cluster replication, persistence and fault-oriented tests. |
| Coordination-service | `python3 -m pytest -q`, including leader transfer/removal, concurrency and recovery tests. |

See the timestamped acceptance report for the exact current revisions, result
codes, and logs; do not use a historical pass to override a current failure or
blocked result.

## Historical open gates

This list describes the September snapshot. The October record supersedes it;
do not read already-closed database or runtime gaps below as current failures.

1. Seed known records into real PostgreSQL, Redis, and MongoDB services; verify
   Cairn backups/restores against those records and run DuraFlow PostgreSQL
   integration tests with a dedicated disposable DSN.
2. Run Mini-Docker's root-only namespace/runtime integration suite from a
   privileged disposable environment.
3. Verify fresh installation, daemon startup after reboot, and abrupt VM
   shutdown/recovery in a disposable Linux VM. The host itself was not rebooted.
4. Run bounded disk-full tests on a dedicated small filesystem and power-loss
   tests in a VM. Host `/tmp` must not be filled for these checks.
5. Complete a monitored 24-hour soak (extend to 72 hours if clean), retaining
   request/error totals, latency, resource use, restart count and a restore
   verification.
6. Complete a threat-model review and adversarial workload test before using
   untrusted tenants. This record is not an independent security assessment.
