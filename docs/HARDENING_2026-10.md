# Hardening follow-up — October 2026

Updated 26 October. Runtime test-only follow-up `b3628db` adds ten mandatory
untraced CI repetitions and identity-checked supervisor failure snapshots; its
new exact checks must complete. Twenty separate local rootexec repetitions
passed with unchanged deadlines. The traced diagnostic's delayed exit is not
proof of the original untraced timeout's cause.

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
| Permanent installation | Boot-enabled `cairn.service` and `cairn-runtime.service` use root-owned packages and a restricted root-equivalent runtime socket. The ordinary-user controller retains its private data layout. Current bundle `40d8979e67ba2be7df36538fef8b2f9d1872f1d84c89bee10f6ea15e843a8cf0` **predates the proc and cron/User repairs**. A reviewed clean replacement, controlled owned-workload restart and final recovery proof are still required. Previous packages/configuration backups remain. The default-prefix validation repair passed18 installer regressions. No real-host reboot occurred. |
| Restore/reconciliation reliability | A live restore reproduced competing restore/reconciler Starts; the original journal and deterministic failing-before regressions remain retained. Durable restore reservation plus serialized restore steps passed ten fixed repetitions, the full internal suite, three installed notes/data flows and both actual daemon-restart/PID/data checks (`validation-runs/hardening-release-candidate-recovery-20261026-attempt2.jsonl`). The earlier failed authentication-timeout attempt remains failed. New source/package changes require final re-verification, not reuse of the older bundle result. |
| Cron lifecycle and task identity | Hermetic failing-before/passing-after tests repair explicit UID:GID forwarding into migrations/cron and Start/Stop/Restart of cron schedules. Stop pauses future admission without killing existing jobs; Start/Restart require an existing valid schedule and retain `active`, without a service container. Twenty focused repetitions, full internal tests, vet and dashboard-button regressions passed (`validation-runs/cron-lifecycle-repair-20261026/HANDOFF.md`). Actual installed UID/GID/file-ownership and timer lifecycle proof remains required. |
| Dashboard | All15 authenticated sandbox-enabled browser checks passed on the previous installed candidate (`validation-runs/dashboard-authenticated-risk-proof-20261026/result.json`): real lifecycle/journal data, logs, modal keyboard focus, selected snapshot restore, safe rollback/new audit deployment, and migration-risk FORCE/cancel/Escape/offline/reconnect with persistent migration bytes retained. Supplemental scrolling passed; a metadata locator failure remains preserved. Supplemental runner regressions now require a fresh backend event ID and browser-poll/render correlation, not an older matching message, and independently attempt all cleanup steps. Final-candidate full replay, corrected supplemental replay and screenshots remain required. |
| Final-revision guest proof | Required and still open. The old guest's interrupted provisioning/dpkg failure and subsequent SSH boot timeout are retained in `validation-vms/hardening-final-20261023/`. Neither reached the new application gates. A fresh bounded guest with deterministic private fixture networking must prove the actual packaged units, boot enablement, notes data, all integration cases, guest reboot and owned-QEMU abrupt shutdown recovery. The old successful guest revision is historical only. |
| MiniDB fencing | Revision `12fae5025421026cbe06b042a823719bee5c27cc` implements optional etcd-backed resource-side stale-owner rejection. All 68 tests passed, including eight actual-etcd cases; exact-commit GitHub verification passed. The supported mode is explicitly experimental, loopback-only, fixed membership and ALL acknowledgements. It is not global linearizability, dynamic membership, automatic authority renewal or production split-brain certification. See MiniDB's `docs/fenced-writes.md`. |
| Coordination | Revision `6c2026d840985f7084cfc81bebe7c12f924676a0` passed all 368 tests with no failures/errors/skips and exact-commit GitHub verification. The lease-clock tests cover the process-local monotonic-time boundary; selected campaigns do not establish every partition/concurrent history. |
| Fault framework and campaigns | Repeated-kill scheduling, TTL-aware lock checking and minimization regressions are repaired; both complete recorded histories are mandatory corpus tests. The first matrix remains **20 passes/four NO_COVERAGE failures**. Revision `c7dd846c304f6994737a18750abc829aad6b6ffb` fixes unlock routing to actually observed owned keys, without changing RNG draws, weights, faults, negative probes, minima or checkers. Its complete fresh matrix **passed24/24**, seeds42–47 across both targets/profiles (`FAILFORGE/validation-runs/release-matrix-owned-unlock-20261026/summary.json`); full suite/vet and exact CI passed. No relevant target source changed afterward, so repeating this completed matrix is unnecessary. |
| Bounded load sessions | Historical October sessions and short smokes do not count for the final hardening revision. The October26 first attempt was explicitly **interrupted after1027seconds** when the proc issue was confirmed; the queued second session never started. Both remain accurately recorded in `validation-runs/soak-sessions.json`. Two NEW separately recorded7200second sessions remain required after final installation/recovery. Each needs terminal sampled duration, zero errors, final restoration, CPU/memory/disk/process-restart evidence and successful cleanup. |
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
