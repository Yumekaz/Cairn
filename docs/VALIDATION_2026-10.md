# Acceptance evidence — 2026-10-08 through 2026-10-14

This record describes demonstrated behavior, not a zero-bug or production-ready guarantee.

## Release closeout

The agreed, bounded acceptance gates below are closed. Historical failed, interrupted, skipped and blocked attempts remain retained; they are not relabelled as passes. The final pushed-head GitHub report is generated after the documentation commit, so this document does not try to embed its own future commit ID. Retained report location: `validation-runs/github-release-final-20261014/report.{md,json}`. The preceding code-head snapshot is `validation-runs/github-pre-closeout-final-oct14/`.

| Agreed gate | Completed proof |
| --- | --- |
| Repeatable verification and evidence retention | `scripts/acceptance_suite.sh` saves commands, revisions, patches, exit codes and bounded logs; `scripts/github_ci_snapshot.py` separately checks all six exact heads. Runner regressions reject missing/skipped integration tests and retain transport failures as non-green evidence. |
| Dashboard controls and backend/data outcomes | September control matrix plus the October restore, migration/rollback, focus, metadata and complete acknowledged-dialog reconnect results below. Selected snapshot restoration is checked against actual application bytes. |
| Privileged runtime and recovery | 97 local Mini-Docker tests, five actual namespace/container cases, three DuraFlow root executor cases and required privileged GitHub jobs. |
| Real database recovery | `validation-runs/databases-replay-final-oct14/`: Cairn PostgreSQL/Redis/MongoDB production-driver proofs and all five DuraFlow PostgreSQL cases passed. Invalid/corrupted restoration is checked without accepting a success toast as proof. |
| Reproduced failures and bounded fault campaigns | Deterministic lease/replay, lost-Start-ack, active-job cleanup and handoff/removal regressions; covered FailForge seeds 42/43 against MiniDB and Coordination, with successful operations and confirmed fault injection required. |
| Resource exhaustion | Actual bounded 8MiB ENOSPC: failed backup recorded, original data preserved, subsequent backup succeeds after recovery. |
| Fresh guest installation, reboot and crash | `validation-vms/proof2-20261008/status.json`: all nine integration workflows, installation, graceful guest reboot and abrupt QEMU shutdown passed. Known journal bytes survived both recovery paths. |
| Separately recorded soaks | Two independent 7200-second targets exceeded with zero errors, verified final snapshot restoration and retained CPU/memory/disk/process evidence. No interrupted-run credit or uninterrupted 24-hour claim. |
| Scoped security | Admin Host/origin/fetch guards, path/archive traversal rejection, mount failures/read-only enforcement, cgroup memory, listener FD ownership, socket permissions and credential-log redaction. Required lint/security checks passed. |
| Documentation and final CI | Capability/limitation documentation and MiniDB positioning updated. Exact code-head checks below passed; the post-commit report records the final documentation head and each other repository's unchanged pushed head. |

These proofs have distinct provenance. The final VM installed Cairn `0b8b40076d4e3c4f84151959fdd2217b64c3ba31`, DuraFlow `584cdf1096e6d51f8dd3f6d82c81f223671391f7` and Mini-Docker `5c83126f83fcd21355e5931d0fbabde838dd7e9b`; checksums and source snapshots are retained in its directory. The later Cairn active-job cleanup repair at `dd62c1c7988f95cfdbe452c178020ed83de02b72` passed twenty deterministic local repetitions and the complete required native GitHub integration/database job. The VM is not falsely presented as having run that later SHA. Its database-task adapter tests are not substitutes for the separate real-database driver proofs.

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
- The official PostgreSQL entrypoint temporarily starts a socket-only initialization server. A readiness probe accepted it just before its shutdown, intermittently failing the real-database CI fixture. Both probes now require the final TCP listener; the actual PostgreSQL restore test passed five fresh-container repetitions.
- Reconciliation could restart a container during Stop cleanup, leaving duplicate/stale NAT rules that masked a subsequent deployment. Lifecycle operations, removal, reconciliation and deployment route commit/cleanup now serialize. The Stop regression passed 20 repetitions and the real notes restart/redeploy/restore flow passed after scoped stale-route cleanup.
- DuraFlow replay was reproduced in two deterministic October 14 regressions: lease acquisition reset a succeeded step after a stale scan, and the same worker launched its own in-flight step again when its heartbeat appeared stale. SQLite/PostgreSQL now preserve terminal state except deliberate compensation, and workers reserve execution before claiming leases. Full local DuraFlow checks and five real PostgreSQL cases passed. Exact-commit GitHub verification, including the required race detector and privileged runtime, passed for `584cdf1096e6d51f8dd3f6d82c81f223671391f7`.
- A stronger crash fixture reproduced lost-Start-acknowledgement recovery failure: the runtime already had a running candidate, but replaying Start returned an error. Recovery now inspects and preserves that actual running candidate, resolves its canonical ID, and rechecks state if a concurrent Start completed. The fixture implements real name lookup and already-running rejection and asserts the candidate actually exists in running state. Five consecutive local recovery repetitions passed.
- The final cron GitHub check reproduced a task-cleanup race: reconciliation deleted a just-created live cron container as untracked, twice, before its result could be recorded. Exact active job ownership is now checked in the durable jobs table at the cleanup decision. Cron and one-off regressions also require genuinely untracked and terminal-leftover containers to be reclaimed. Twenty local repetitions passed. Both required GitHub jobs passed for `dd62c1c7988f95cfdbe452c178020ed83de02b72`, including actual successful cron execution, all nine integration workflows and the real database driver step: [run 38502561820](https://github.com/Yumekaz/Cairn/actions/runs/38502561820). The failing predecessor's logs remain in `validation-runs/cairn-spine-0b8b400.zip` and `cairn-final-spine-ci-failure-oct14.log`.

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
| Restore dialog Close / Cancel / reverse Tab / polling focus | October 14: Shift+Tab wrapped to Cancel; acknowledgement enabled Proceed; Close and Cancel did not restore data and returned focus to backup `284ee384` in the correct volume after repeated list replacement. |
| Offline acknowledged confirmation | October 14: Proceed became disabled with acknowledgement still checked when the acceptance daemon stopped. The daemon was restored and Connected/live data returned; an interrupted turn lost the open-dialog state, so that sequence alone is not an uninterrupted reconnect-dialog proof. Earlier no-reload reconnect evidence remains separately recorded. |
| Complete acknowledged-dialog reconnect | Repeated October 14 without interruption: same dialog stayed open and acknowledged while offline Proceed was disabled. Restoring the exact daemon returned Connected and enabled the valid acknowledged action without page reload. Cancel closed it and focused the selected snapshot button. No restore was executed. Screenshot: `dashboard-reconnect-confirmation-complete-oct14.png`. |

Saved screenshots: `validation-runs/screenshots/dashboard-restore-20261008.jpg` and the offline-state screenshot. The September record retains the earlier navigation, search, lifecycle, log controls, safe rollback, event-filter/scrolling and keyboard checks. FORCE matching currently accepts case-insensitive input; exact uppercase is not a documented security boundary. Its input now has an associated accessible label.

Full audit history remains available. Date formatters are cached and DOM updates batched; unchanged polls preserve the existing nodes. Not established: every asynchronous race under arbitrary network conditions or independent assistive-technology coverage. Browser clicks alone do not establish schema compatibility.

The host shut down between work sessions (acceptance runtime journal stopped at 03:11 and host boot began at 07:24 on October 14). Its temporary runtime unit did not survive that shutdown. Cairn's API was reachable while Mini-Docker was absent; Connected is API reachability, not a whole-stack health guarantee. After read-only inspection, the owned runtime was restored as `mini-docker-cairn-oct14-session.service` with the same state path and restricted 660 socket. Doctor passed and the actual notes proxy again returned HTTP 200 with exact `first\n`. No host reboot was initiated by this verification.

## Completed observation and VM gates

The user replaced the continuous 24-hour gate with separately recorded 2–4-hour sessions. The release checklist uses two independent two-hour sessions: baseline (15-second samples, three verified reads per write) and higher request rate (5-second samples, nine reads per write). `validation-runs/soak-sessions.json` is the session ledger. Interrupted/failed runs do not count, and durations are never combined into an uninterrupted 24-hour claim.

Both required sessions passed independently. Baseline: 7211.49 seconds, 1912 requests, zero errors and verified final restore; two daemon restarts and no application-runtime restart. Higher rate: 7211.4 seconds, 14130 requests, zero errors, zero daemon/application restarts and verified final restore. Evidence: `validation-runs/soak-session-1/` and `validation-runs/soak-session-2c-20261013/`. The original higher-rate attempt failed at 4363.19 seconds when its runtime/daemon units were stopped; it does not count. The replacement bootstrap reproduced the Stop/reconcile/NAT defect and also remains failed. Neither duration contributes to either passing session.

Each session retains latency, memory, CPU, data/disk size and process-identity restart counts. The previous roughly 50-minute run is retained as interrupted; failed bootstrap smokes remain failed. The existing monitor was updated in place and was retired on October 14 after both session gates closed. No duplicate monitor or uninterrupted 24-hour claim was created.

The fresh-OS/reboot gate uses `scripts/vm_acceptance.py`, a checksum-verified official Ubuntu 24.04 cloud image, an 8GiB disposable overlay, 2GiB RAM and two TCG-emulated CPUs. The installed guest passed actual notes write/restart/redeploy/backup/restore, guest graceful reboot and abrupt QEMU shutdown, preserving the known journal. Evidence is in `validation-vms/proof2-20261008/`, including timestamped previous statuses, source/revision snapshots, checksum manifests and guest journal. No host reboot occurred.

Installation used checksum-verified locally built static Cairn binaries and a locally built Mini-Docker wheel installed offline. Clean source compilation and the supported source installer are separately proved in GitHub CI; cold source compilation in TCG hit bounded timeouts and is not reported as passed. Earlier provisioning and readiness failures remain retained. The first expanded guest attempt skipped two tests because Doctor allowed an absent daemon before readiness. It exposed a missing health-check page and machine-speed-dependent recovery timing. After those corrections, eight named workflows passed in the guest, including real cron execution, migration/rollback and database task adapters; standalone dashboard testing still failed its two-second client deadline under TCG. That attempt remains failed. The dashboard fixture now waits for the actual API and retains all redirect, asset and deployment-API assertions. Final guest revalidation passed all nine required named workflows without skips, followed by the notes-data installation/reboot/abrupt-shutdown proofs. Its wrapper is `validation-runs/vm-final-readiness-and-ack-oct14.log`; `commands.jsonl` identifies the appended log segments and exit codes. The owned QEMU process stopped after completion; no duplicate guest or real-host reboot was started.

## Exact-commit GitHub checks

| Repository | Pushed revision | Observed check |
| --- | --- | --- |
| MiniDB | `8ecbc94e05f0355c2b34f6237ac9cb63ed4dff02` | [Verification passed](https://github.com/Yumekaz/Mini-Redis-Cassandra/actions/runs/37805063079) |
| Coordination | `19537975549f2cf1bdb2860ceb6c737d7504e91c` | [Verification passed](https://github.com/Yumekaz/Coordination-service/actions/runs/37805074698) |
| Mini-Docker | `5c83126f83fcd21355e5931d0fbabde838dd7e9b` | [Tests](https://github.com/Yumekaz/Mini-Docker/actions/runs/37805283091), [strict lint/security](https://github.com/Yumekaz/Mini-Docker/actions/runs/37805283136), [privileged runtime](https://github.com/Yumekaz/Mini-Docker/actions/runs/37805283074) passed |
| DuraFlow | `584cdf1096e6d51f8dd3f6d82c81f223671391f7` | [Verification passed](https://github.com/Yumekaz/DURAFLOW/actions/runs/38466458454), including required race detection with PostgreSQL and privileged runtime |
| FailForge | `f49a1182096ff3d056200f1e12f083868710bf0a` | [Verification and covered campaigns passed](https://github.com/Yumekaz/FAILFORGE/actions/runs/37807438461) |
| Cairn code revision | `dd62c1c7988f95cfdbe452c178020ed83de02b72` | [Both jobs passed](https://github.com/Yumekaz/Cairn/actions/runs/38502561820), including source installation, runner/focus regressions, full privileged integration and actual PostgreSQL/Redis/MongoDB recovery |

The complete code-head snapshot is retained in `validation-runs/github-pre-closeout-final-oct14/`. The final documentation commit's exact-head results are retained separately in `validation-runs/github-release-final-20261014/`; use that report for the final pushed Cairn revision. An earlier snapshot hit a network read timeout before recording the sixth repository; it is incomplete, not green. The collector now has bounded transient retries and retains non-green error records for every repository instead of abandoning the report. Regression tests exercise both behavior and permanent errors. Source changes are not hidden behind documentation-only CI skips.

## Security scope and remaining limits

The TCP dashboard now rejects cross-origin/opaque-origin browser calls, cross-site fetches and unexpected Host headers, with regressions proving the mutation handler is not reached. The Unix-socket CLI path is unaffected. This is local-admin hardening, not authentication, TLS, authorization or an independent runtime isolation audit.

Scoped checks also cover path-component rejection, archive traversal rejection, read-only and failed mounts, cgroup memory enforcement, listener FD ownership, socket permissions and known credential redaction in runtime startup logs. Mini-Docker's medium/high Bandit scan is now a required check instead of an ignored command.

PostgreSQL clean-object dumps replace objects present in the snapshot, not unrelated later-created objects. MongoDB restore is not atomic across collections. Failed MiniDB writes can partially apply; lack of consensus-grade fencing remains a stated limitation. Broader hostile-workload isolation, exhaustive fault schedules, physical power-loss and prolonged multi-node production load are still outside this evidence.
