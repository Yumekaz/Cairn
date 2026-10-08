# Acceptance evidence — 2026-10-08

This record describes demonstrated behavior, not a zero-bug or production-ready guarantee.

## Retained acceptance matrix

Run `validation-runs/20261008T124404Z/` completed with retained commands, full starting commit IDs, working-tree patches and per-check logs. It tested local changes on those revisions; uncommitted source is not the same as the base commit. Future runner versions additionally archive untracked source files. Logs are deliberately outside `/tmp`.

| Gate | Result and evidence |
| --- | --- |
| Cairn unit packages | Passed: `logs/cairn-go.log` |
| DuraFlow unprivileged packages | Passed: `logs/duraflow-go.log`; gated runtime tests are separate evidence |
| Mini-Docker non-root suite | Passed, root cases skipped: `logs/minidocker-pytest.log` |
| FailForge unit suite/build | Passed: `logs/failforge-go.log`, `logs/failforge-build.log` |
| MiniDB fault seeds 42 / 43 | Passed with successful-operation and confirmed-fault requirements: `logs/failforge-minidb-{42,43}.log` |
| Coordination baseline / seeds 42 / 43 | Passed with write/lock coverage: `logs/failforge-coordination-*.log` |
| MiniDB complete suite | 33 passed: `logs/minidb-pytest.log` |
| Coordination complete suite | 320 passed: `logs/coordination-pytest.log` |
| Real PostgreSQL / Redis / MongoDB drivers | Passed: `databases/cairn-drivers.log` |
| DuraFlow real PostgreSQL leases | Passed: `databases/duraflow-postgres.log`, including concurrent first acquisition |

The wrapper correctly remains non-green for separately evaluated gates. Its old standalone DSN row is marked blocked even though the private database gate executed the PostgreSQL tests; the log above is the actual proof. The runner has been corrected to project that executed result into the corresponding row on future runs.

Additional retained proofs:

- `validation-runs/duraflow-root-executor-isolated-20261008.log`: three actual Mini-Docker executor tests passed with independent BusyBox rootfs fixtures.
- `validation-runs/minidocker-final-fd-security-root.{log,xml}`: 97 passed after removal of the empty placeholder and addition of meaningful FD ownership, metadata generation and credential-log regressions. Five cases execute real privileged namespace/container operations.
- `validation-runs/cairn-enospc-real-proof-20261008.log`: actual ENOSPC in an isolated 8MiB tmpfs. Backup metadata becomes failed, source bytes remain unchanged, and a new backup succeeds after space returns. This is not power-loss proof.
- `validation-runs/coordination-handoff-authority-fixed-20261008.log`: the handoff/removal test and phantom-vote regression passed in five consecutive repetitions.

## Defects reproduced and repaired

- PostgreSQL restore reported success while later rows remained. Dumps now include clean-object statements; restores use stop-on-error and a single transaction. The real proof restores the selected row set and rejects invalid SQL without retaining its preceding update.
- Redis backup set an empty `REDISCLI_AUTH`, causing authentication against an unauthenticated server. The variable is now omitted when no password is configured.
- MiniDB prepared-write success did not require remote commit acknowledgements, and committed AOF entries could remain buffered through a SIGKILL. Commit confirmations are counted and committed entries are synchronously flushed before return. Configured read thresholds no longer shrink with live ring size.
- FailForge reused values, imposed response-arrival order on overlapping writes, and treated every partially applied failed write as corruption. Values now identify write attempts; the checker uses defensible real-time ordering and treats failed writes as indeterminate. It remains a limited checker, not a complete linearizability analysis.
- Coordination could compare reserved/uncommitted sequence numbers with follower apply cursors. Its committed cursor is separate. Stale peer polls cannot overwrite a newer acknowledgement. Delayed old-leader heartbeats retain their original term, and catch-up/snapshot data from a demoted node is not treated as leader authority.
- Dashboard status polling re-enabled invalid lifecycle/confirmation actions. Semantic and busy guards now survive polling/reconnect. Responses for an earlier volume selection cannot replace the current backup view, and modal focus can return to a replaced Inspect button.
- Mini-Docker supervisors inherited the daemon listener. After daemon shutdown, orphaned supervisors kept an unserved socket alive. The child now preserves only stdio and its startup pipes before opening its own logs/metadata. Legacy test workloads were stopped through an explicitly scoped temporary API, and the normal runtime recovered without killing arbitrary processes or deleting their data. Startup metadata is published before child release, and restart metadata clears the previous exit/finish fields.
- Service/volume names could contain filesystem path components. The API now rejects separators and invalid single-component names before creating records or directories.

## Dashboard live browser acceptance

Fixture: `notes-proof-5606b027`, its data volume and backup `284ee384-352b-4d67-b11b-85ab383e2615`.

| Flow | Observed outcome |
| --- | --- |
| Running-service Start guard | Remained disabled after a status poll |
| Restore acknowledgement | Proceed remained disabled across polling until acknowledgement |
| Actual Restore Proceed | Removed `ui-after-snapshot`; HTTP journal returned exactly `first\n`; `RestoreCompleted` audit event recorded |
| Refresh and modal focus | After the service cards refreshed, closing inspection focused the correct service's replacement Inspect button |
| Automatic reconnect | Disconnected view later returned to Connected with fresh live data without a manual page reload |
| Failed migration / unsafe rollback | Warning identified failed deploy `b8ad32d2` as possibly modifying state; acknowledgement plus FORCE confirmation enabled the deliberate forced rollback |
| Forced rollback backend/data | Active deploy changed to `8087bf89`; migration marker remained in the data, proving rollback is not database undo |
| Fixture cleanup/recovery | Restored known backup and verified exact `first\n` again; evidence in `dashboard-forced-rollback-data-20261008.log` |
| Event metadata Inspect / Hide / refresh | Labelled test event expanded and collapsed; HTML-looking metadata stayed literal text without executing. The test found polling collapsed inspected metadata; expansion now persists by event ID and was verified across a poll. |

Saved screenshots: `validation-runs/screenshots/dashboard-restore-20261008.jpg` and the offline-state screenshot. The September record retains the earlier navigation, search, lifecycle, log controls, safe rollback, event-filter/scrolling and keyboard checks. FORCE matching currently accepts case-insensitive input; exact uppercase is not a documented security boundary. Its input now has an associated accessible label.

Full audit history remains available. Date formatters are cached and DOM updates batched; unchanged polls preserve the existing nodes. Not established: every asynchronous race under arbitrary network conditions or independent assistive-technology coverage. Browser clicks alone do not establish schema compatibility.

## Continuing observation and VM gates

The user replaced the continuous 24-hour gate with separately recorded 2–4-hour sessions. The release checklist uses two independent two-hour sessions: baseline (15-second samples, three verified reads per write) and higher request rate (5-second samples, nine reads per write). `validation-runs/soak-sessions.json` is the session ledger. Interrupted/failed runs do not count, and durations are never combined into an uninterrupted 24-hour claim.

The first session is running under `cairn-soak-session-1.service`, with status/samples in `validation-runs/soak-session-1/`; the second remains not started. Each records latency, memory, CPU, data/disk size and process-identity restart counts, and requires a final verified snapshot restore and successful cleanup. The previous roughly 50-minute run is retained as interrupted; two failed bootstrap smokes remain failed. The corrected resource smoke passed after the listener defect was repaired. The existing monitor was updated in place, not duplicated.

The fresh-OS/reboot gate uses `scripts/vm_acceptance.py`, a checksum-verified official Ubuntu 24.04 cloud image, an 8GiB disposable overlay, 2GiB RAM and two TCG-emulated CPUs. Two attempts hit bounded boot/provisioning timeouts and remain failed, not product passes. The second disposable disk is being resumed only because no recovery gate had passed and no Cairn instance had been installed. Old status/source snapshots remain saved; new installer output streams to a durable log. Current evidence is in `validation-vms/proof2-20261008/`. This gate is not passed until installation, guest reboot and abrupt guest shutdown each preserve the known journal. No host reboot is part of this proof.

## Exact-commit GitHub checks

| Repository | Pushed revision | Observed check |
| --- | --- | --- |
| MiniDB | `8ecbc94e05f0355c2b34f6237ac9cb63ed4dff02` | [Verification passed](https://github.com/Yumekaz/Mini-Redis-Cassandra/actions/runs/37805063079) |
| Coordination | `19537975549f2cf1bdb2860ceb6c737d7504e91c` | [Verification passed](https://github.com/Yumekaz/Coordination-service/actions/runs/37805074698) |
| Mini-Docker | `5c83126f83fcd21355e5931d0fbabde838dd7e9b` | [Tests](https://github.com/Yumekaz/Mini-Docker/actions/runs/37805283091), [strict lint/security](https://github.com/Yumekaz/Mini-Docker/actions/runs/37805283136), [privileged runtime](https://github.com/Yumekaz/Mini-Docker/actions/runs/37805283074) passed |
| DuraFlow | `04eeb9fccc3100359825f31d6af630707bf11985` | [Verification passed](https://github.com/Yumekaz/DURAFLOW/actions/runs/37808843799); prior failure was corrected with owned private runtime state, not weaker isolation or permissions |
| FailForge | `f49a1182096ff3d056200f1e12f083868710bf0a` | [Verification and covered campaigns passed](https://github.com/Yumekaz/FAILFORGE/actions/runs/37807438461) |
| Cairn | Working-tree closeout changes | Final push/check remains pending |

## Security scope and remaining limits

The TCP dashboard now rejects cross-origin/opaque-origin browser calls, cross-site fetches and unexpected Host headers, with regressions proving the mutation handler is not reached. The Unix-socket CLI path is unaffected. This is local-admin hardening, not authentication, TLS, authorization or an independent runtime isolation audit.

Scoped checks also cover path-component rejection, archive traversal rejection, read-only and failed mounts, cgroup memory enforcement, listener FD ownership, socket permissions and known credential redaction in runtime startup logs. Mini-Docker's medium/high Bandit scan is now a required check instead of an ignored command.

PostgreSQL clean-object dumps replace objects present in the snapshot, not unrelated later-created objects. MongoDB restore is not atomic across collections. Failed MiniDB writes can partially apply; lack of consensus-grade fencing remains a stated limitation. Broader hostile-workload isolation, exhaustive fault schedules, physical power-loss and prolonged multi-node production load are still outside this evidence.
