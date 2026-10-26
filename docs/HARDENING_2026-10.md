# Hardening follow-up — October 2026

Updated 26 October. The required repeated runtime workflow reproduced the
supervisor Stop failure on an **untraced** run. Its identity-checked kernel
stack established OverlayFS writeback during last-mount namespace cleanup
(`wb_wait_for_completion` → `ovl_sync_fs` → `cleanup_mnt` → `do_exit`), not a
metadata lock or a merely traced-process artifact. The failed job remains
retained as `github-runtime-b3628db-attempt2-job-121554365250.log` under the
closeout evidence directory.

Runtime repair `9d6b339` pins the verified mount namespace **before** signalling,
keeps the existing supervisor-exit fence, and releases the descriptor after
finalization. It neither disables writeback nor lengthens acceptance deadlines.
Seven regression cases,171 ordinary-user cases, all16 privileged cases and an
actual detached rootless Stop passed locally; the original six regressions
failed before the repair. A separate coding-agent review found no blocking
FD/security issue, but is not professional certification. Runtime head
`141b4cc` changes only public documentation after that repair. Both exact CI
proofs and a new installed/recovery/guest/soak proof remain required before
closing this gate. Storage cleanup can still delay total Stop latency.

This milestone does not relabel earlier acceptance as a
security certification. Historical passes, failures and interruptions retain
their original revisions and results. **The hardening closeout is not complete.**

## Implementation and retained evidence

Evidence paths below are relative to this checkout unless a repository is named.
They identify retained local results, not files promised to exist on GitHub.

| Gate | Current evidence and remaining work |
| --- | --- |
| Authenticated administration | Private credential creation, constant-time credential verification, Host/Origin checks, explicit hostname allowlists, TLS >=1.2 and fail-closed public binding are implemented. Native trusted/untrusted TLS handshake tests passed. Installed missing/wrong credentials return 401, correct credentials 200 and hostile Host/Origin 403 (`validation-runs/hardening-release-admin-20261026.jsonl`). No public deployment or multi-user authorization/MFA is claimed. |
| Runtime identity and isolation | Boot/start identity and pidfds protect lifecycle signals; exec uses pinned namespaces/root, cgroups, reduced capabilities, filtering and an isolated environment. Staging uses no-follow handles, recursive nodev/nosuid, mandatory pivot/old-root detach, private allowlisted devices and bounded shared memory. A subsequent scoped review found writable global proc controls in `ad03a78`; **that revision is superseded**, not a security pass. Runtime `3d0e44f0f222db67ecbef734a0259067cf1aac15` requires whole-proc read-only enforcement and private inert masks at start and exec, refuses legacy unprotected exec, and permits only the kernel's interrupted-wait restart syscall in the relevant seccomp repair. Linux x86_64 kernel 5.12+ is required; missing enforcement fails closed. |
| Runtime verification | Latest local **16 privileged** cases passed without skips (`validation-runs/restart-syscall-complete-root-20261026.{log,xml}`), including real pidfd pause/resume, start/exec proc enforcement and device confinement. All **164 ordinary-user** cases passed, with those16 root-only cases separately skipped (`restart-syscall-final-user-host-20261026.log`). Formatting/fatal lint passed; Bandit found no medium/high findings, not zero low findings. The final targeted strace passed. Both prior red GitHub runs and a nodev-/tmp device-fixture failure are retained. Although exact `3d0e44f` CI is green, the earlier repeated supervisor-stop timeout remains under investigation; a passing rerun is not its explanation. Scoped rootless smoke passed for the proc repair, not every rootless host. |
| Permanent installation | Boot-enabled `cairn.service` and `cairn-runtime.service` use root-owned packages and a restricted root-equivalent runtime socket. Bundle `624c33061892db3039ec724f7d44f105949addb2abacdc74fbf50e266b54a799` installed the proc and cron/User repairs from clean source. Its admin, real notes workflow and both daemon-restart/PID/data proofs passed; the two historical owned fixtures were explicitly restarted with exact data hashes/configuration retained. This bundle **predates the supervisor/writeback repair** and needs a clean replacement/reverification. Previous packages and configuration backups remain. No real-host reboot was performed. |
| Restore/reconciliation reliability | A live restore reproduced competing restore/reconciler Starts; the original journal and deterministic failing-before regressions remain retained. Durable restore reservation plus serialized restore steps passed ten fixed repetitions, the full internal suite, three installed notes/data flows and both actual daemon-restart/PID/data checks (`validation-runs/hardening-release-candidate-recovery-20261026-attempt2.jsonl`). The earlier failed authentication-timeout attempt remains failed. New source/package changes require final re-verification, not reuse of the older bundle result. |
| Cron lifecycle and task identity | Hermetic failing-before/passing-after tests repair explicit UID:GID forwarding into migrations/cron and Start/Stop/Restart of cron schedules. Stop pauses future admission without killing existing jobs; Start/Restart require an existing valid schedule and retain `active`. Twenty focused repetitions, full internal tests, vet and button regressions passed. The installed live proof also passed actual migration/cron UID:GID1000:1000, file ownership,70seconds without new admitted jobs after Stop, and new successful jobs after Start/Restart (`validation-runs/protected-task-identity-20261026/status.json`); exact owned timer cleanup passed. |
| Dashboard | All15 authenticated sandbox-enabled checks passed on the proc/cron-corrected installed candidate (`validation-runs/protected-dashboard-final-20261026/result.json`): real lifecycle/journal data, logs, modal focus, selected restore, safe rollback/new audit deployment, migration-risk FORCE/cancel/Escape/offline/reconnect with persistent bytes retained. All4 supplemental checks also passed (`protected-dashboard-events-20261026/result.json`): actual auto/wheel/Home scrolling, escaped metadata Inspect/Hide across refresh/filter, and a newly created event ID correlated to the browser's own poll and rendered row without reload. Cleanup and retained screenshots passed. Earlier failed harness attempts remain preserved; the replacement runtime still needs proportionate final backend/data verification. |
| Final-revision guest proof | Required and still open. The old guest's interrupted provisioning/dpkg failure and subsequent SSH boot timeout are retained in `validation-vms/hardening-final-20261023/`. Neither reached the new application gates. A fresh bounded guest with deterministic private fixture networking must prove the actual packaged units, boot enablement, notes data, all integration cases, guest reboot and owned-QEMU abrupt shutdown recovery. The old successful guest revision is historical only. |
| MiniDB fencing | Revision `12fae5025421026cbe06b042a823719bee5c27cc` implements optional etcd-backed resource-side stale-owner rejection. All 68 tests passed, including eight actual-etcd cases; exact-commit GitHub verification passed. The supported mode is explicitly experimental, loopback-only, fixed membership and ALL acknowledgements. It is not global linearizability, dynamic membership, automatic authority renewal or production split-brain certification. See MiniDB's `docs/fenced-writes.md`. |
| Coordination | Revision `6c2026d840985f7084cfc81bebe7c12f924676a0` passed all 368 tests with no failures/errors/skips and exact-commit GitHub verification. The lease-clock tests cover the process-local monotonic-time boundary; selected campaigns do not establish every partition/concurrent history. |
| Fault framework and campaigns | Repeated-kill scheduling, TTL-aware lock checking and minimization regressions are repaired; both complete recorded histories are mandatory corpus tests. The first matrix remains **20 passes/four NO_COVERAGE failures**. Revision `c7dd846c304f6994737a18750abc829aad6b6ffb` fixes unlock routing to actually observed owned keys, without changing RNG draws, weights, faults, negative probes, minima or checkers. Its complete fresh matrix **passed24/24**, seeds42–47 across both targets/profiles (`FAILFORGE/validation-runs/release-matrix-owned-unlock-20261026/summary.json`); full suite/vet and exact CI passed. No relevant target source changed afterward, so repeating this completed matrix is unnecessary. |
| Bounded load sessions | Historical sessions and short smokes do not count for the final hardening revision. The proc-superseded first attempt was interrupted after1027seconds. The next protected candidate's first session was explicitly interrupted after572.716seconds when the untraced writeback failure reproduced; its queued second session never started. Both interruptions and cancellations remain in `validation-runs/soak-sessions.json`; neither duration counts. Two NEW separate7200second sessions remain required after final repair/installation/recovery, each with terminal sampled duration, zero errors, restoration, CPU/memory/disk/controller+workload restart evidence and clean stop. |
| Exact-head GitHub checks | The latest snapshot `validation-runs/closeout-20261026/github-runtime-3d0e44f/report.md` confirms all six then-pushed heads green, including runtime3d0e44f's required Root Runtime Validation. This is not evidence for uncommitted Cairn changes, a newer package, or an unexplained intermittent failure. Every final pushed head must be inspected again after the remaining changes. |

## Release-evidence rules

- Save commands, revisions, exit codes, seeds, logs and useful screenshots outside `/tmp`.
- Preserve failed/interrupted attempts. A fresh run is a new named result, not a rewritten old pass.
- A pending/failed/skipped/missing required GitHub result remains non-green. Local tests alone are not GitHub proof.
- Local race instrumentation is unavailable without a C compiler. Required GitHub race jobs, not ordinary local tests, must supply that evidence.
- Release records must distinguish source compilation, checksum-verified binary installation and the actual installed source revision.
- Installation is not a database migration or transactional activation rollback. Old release/configuration backups are retained for explicit recovery and inspection.

## Explicit boundaries

- Administrative Unix/runtime socket access is privileged; the runtime socket group is root-equivalent.
- Authenticated TLS administration and automated checks are not an independent professional isolation assessment.
- No public service should be exposed without an explicitly configured trusted certificate and hostname.
- VM process death is not every physical disk/controller/power-loss failure. Never reboot the real host as a verification shortcut.
- Separate completed 2–4-hour sessions are not uninterrupted 24-hour operation; interrupted durations are never summed into a completed session.
- Deployment rollback does not undo persistent database writes. MongoDB restoration is not globally atomic.
- Optional etcd fencing does not convert the standalone MiniDB election protocol into consensus-grade replication.

Do not declare the entire six-project hardening milestone closed while required
final-revision VM, installation/recovery, dashboard, soak, campaign or exact-head
GitHub evidence remains failed, pending or unverified.
