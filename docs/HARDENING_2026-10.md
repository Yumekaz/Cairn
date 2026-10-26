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
Seven regression cases, 171 ordinary-user cases, all 16 privileged cases and an
actual detached rootless Stop passed locally; the original six regressions
failed before the repair. A separate coding-agent review found no blocking
FD/security issue, but is not professional certification. Runtime head
`141b4cc` changes only public documentation after that repair. Two independent
exact-commit GitHub privileged workflows passed, each including all 16 cases
and ten additional untraced lifecycle repetitions. The final runtime's native
installation, recovery and dashboard proofs also passed. Guest recovery and
two final timed sessions remain open. Storage cleanup can still delay total
Stop latency.

This milestone does not relabel earlier acceptance as a
security certification. Historical passes, failures and interruptions retain
their original revisions and results. **The hardening closeout is not complete.**

The first final-runtime guest recheck hit the installer's 300-second offline venv
setup cap under TCG before activating its new package; no final application
gate passed in that attempt. Its failed status/log remain preserved. The VM
fixture selects an explicit 900-second venv-only budget, bounded and tested;
all application/readiness/runtime test deadlines are unchanged. That setup
then succeeded, but a host poweroff interrupted the guest during the integration
proof, before any final application gate completed. The failed and interrupted
attempts remain retained. The installer/verifier changes do not change the
running controller, runtime or notes implementation. A new explicitly named
attempt must finish the guest gates; stale RUNNING status is not a pass.

The VM runner now records SIGTERM/SIGINT as **interrupted**, preserves any
completed partial gates and source records, and cleans up only its actual
owned child process. Failed evidence writes cannot bypass cleanup or turn
an ambiguous terminal write into a successful owner exit. All 126 verification
runner regressions passed locally, including controlled child-process signal
delivery; the final guest still requires its own real result.

## Implementation and retained evidence

Evidence paths below are relative to this checkout unless a repository is named.
They identify retained local results, not files promised to exist on GitHub.

| Gate | Current evidence and remaining work |
| --- | --- |
| Authenticated administration | Private credential creation, constant-time credential verification, Host/Origin checks, explicit hostname allowlists, TLS >=1.2 and fail-closed public binding are implemented. Native trusted/untrusted TLS handshake tests passed. Installed missing/wrong credentials return 401, correct credentials 200 and hostile Host/Origin 403 (`validation-runs/writeback-installed-admin-20261026.jsonl`, repeated after the user's host boot in `validation-runs/closeout-20261026/postboot-admin-evening.jsonl`). No public deployment or multi-user authorization/MFA is claimed. |
| Runtime identity and isolation | Boot/start identity and pidfds protect lifecycle signals; exec uses pinned namespaces/root, cgroups, reduced capabilities, filtering and an isolated environment. Staging uses no-follow handles, recursive nodev/nosuid, mandatory pivot/old-root detach, private allowlisted devices and bounded shared memory. A subsequent scoped review found writable global proc controls in `ad03a78`; **that revision is superseded**, not a security pass. Final runtime `141b4cc` includes whole-proc read-only enforcement, private inert masks at start and exec, refusal of legacy unprotected exec and the narrowly scoped interrupted-wait restart syscall repair. Linux x86_64 kernel 5.12+ is required; missing enforcement fails closed. |
| Runtime verification | Final runtime `141b4cc` passed **171 ordinary-user** cases and, separately, **all 16 privileged** cases without skips (`validation-runs/stop-mount-pin-root-20261026.{log,xml}`), including pidfd pause/resume, start/exec proc enforcement and device confinement. An actual detached rootless Stop also passed. GitHub runs [40320157797](https://github.com/Yumekaz/Mini-Docker/actions/runs/40320157797) and [40320588965](https://github.com/Yumekaz/Mini-Docker/actions/runs/40320588965) each passed all 16 cases plus ten additional untraced repetitions. Formatting/fatal lint passed; Bandit found no medium/high findings, not zero low findings. Prior red runs and the nodev-/tmp fixture failure remain retained. The untraced supervisor timeout has the writeback cause and targeted repair described above; it is not dismissed as flaky. This is scoped evidence, not proof on every rootless host. |
| Permanent installation | Boot-enabled `cairn.service` and `cairn-runtime.service` use root-owned packages and a restricted root-equivalent runtime socket. Manifest `296bd0f83681f7e9ddc04d13a42bb571beaef3075ffe6c40a44abd67932bf2dc` installed clean Cairn `84ac33c` and runtime `141b4cc`, including the writeback repair. Admin checks, the real notes workflow and both actual daemon-restart/PID/data proofs passed (`validation-runs/writeback-installed-*`). The installed application implementation is unchanged by the later installer/verifier-only changes; its actual binary provenance remains `84ac33c`, not a relabelled newer revision. Both units also auto-started on the user's later host boot; the interrupted fixture's entire journal was verified byte-for-byte. Previous packages, configuration backups and data remain. The agent did not reboot the real host. |
| Restore/reconciliation reliability | A live restore reproduced competing restore/reconciler Starts; its original journal and deterministic failing-before regressions remain retained. Durable restore reservation plus serialized restore steps passed ten fixed repetitions, the full internal suite and three installed notes/data flows. The final writeback-corrected package then passed its own write/restart/redeploy/backup/restore flow and both actual daemon-restart/PID/data checks (`validation-runs/writeback-installed-notes-20261026.json`, `writeback-installed-recovery-20261026.jsonl`). Earlier failed attempts remain failed. Rollback is not database undo. |
| Cron lifecycle and task identity | Hermetic failing-before/passing-after tests repair explicit UID:GID forwarding into migrations/cron and Start/Stop/Restart of cron schedules. Stop pauses future admission without killing existing jobs; Start/Restart require an existing valid schedule and retain `active`. Twenty focused repetitions, full internal tests, vet and button regressions passed. The final installed live proof passed actual migration/cron UID:GID 1000:1000, file ownership, 70 seconds without new admitted jobs after Stop, and new successful jobs after Start/Restart (`validation-runs/writeback-task-identity-20261026/status.json`); exact owned timer cleanup passed. |
| Dashboard | All **15** authenticated, sandbox-enabled checks passed on the final installed runtime (`validation-runs/writeback-dashboard-final-20261026/result.json`): real lifecycle/journal data, logs, modal focus, selected restore, safe rollback/new audit deployment, migration-risk FORCE/cancel/Escape/offline/reconnect with persistent bytes retained. All **four** supplemental checks passed with actual process exit 0 in `writeback-dashboard-events-scoped-20261026/result.json`: real auto/wheel/Home scrolling, escaped metadata Inspect/Hide across refresh/filter, and a new backend event ID correlated to the browser's own polling and rendered row without reload. Cleanup/screenshots passed. An earlier wrapper exit 143 and a systemd-service Chromium sandbox startup failure remain failed/incomplete evidence. The accepted scope inherits the application caller's security context; Chromium's sandbox and host security policy were not disabled. See `validation-runs/closeout-20261026/browser-scope-proof.md`. |
| Final-revision guest proof | Required and still open. A bounded private-static-network guest passed installation, all nine integration cases, guest reboot and owned-QEMU abrupt shutdown on the **older** `93f1911`/`b3628db` candidate. That is historical evidence, not final-runtime proof. Final-runtime attempts, source archives, failed setup and the later host-poweroff interruption are retained in `validation-vms/hardening-release-20261026/`. The 900-second setup succeeded and activated the new package, but integration was interrupted before final gates completed. Explicit resumption must finish the full integration, installed-data, guest-reboot and guest-crash gates. Reinstallation in an existing guest is not a newly pristine OS installation. |
| MiniDB fencing | Revision `12fae5025421026cbe06b042a823719bee5c27cc` implements optional etcd-backed resource-side stale-owner rejection. All 68 tests passed, including eight actual-etcd cases; exact-commit GitHub verification passed. The supported mode is explicitly experimental, loopback-only, fixed membership and ALL acknowledgements. It is not global linearizability, dynamic membership, automatic authority renewal or production split-brain certification. See MiniDB's `docs/fenced-writes.md`. |
| Coordination | Revision `6c2026d840985f7084cfc81bebe7c12f924676a0` passed all 368 tests with no failures/errors/skips and exact-commit GitHub verification. The lease-clock tests cover the process-local monotonic-time boundary; selected campaigns do not establish every partition/concurrent history. |
| Fault framework and campaigns | Repeated-kill scheduling, TTL-aware lock checking and minimization regressions are repaired; both complete recorded histories are mandatory corpus tests. The first matrix remains **20 passes/four NO_COVERAGE failures**. Revision `c7dd846c304f6994737a18750abc829aad6b6ffb` fixes unlock routing to actually observed owned keys, without changing RNG draws, weights, faults, negative probes, minima or checkers. Its complete fresh matrix **passed24/24**, seeds42–47 across both targets/profiles (`FAILFORGE/validation-runs/release-matrix-owned-unlock-20261026/summary.json`); full suite/vet and exact CI passed. No relevant target source changed afterward, so repeating this completed matrix is unnecessary. |
| Bounded load sessions | Historical sessions and short smokes do not count for the final hardening revision. Earlier first attempts were interrupted after 1027 seconds (proc repair), 572.716 seconds (writeback failure) and 2876.970 seconds (host poweroff); their queued second sessions never started. All interruptions/cancellations remain in `validation-runs/soak-sessions.json`; none counts. New `powerresume-soak-session-1-20261026` and separately gated `powerresume-soak-session-2-20261026` are the current attempts, each requiring its own 7200-second post-target terminal sample, zero errors, final restoration, CPU/memory/disk/controller+workload restart evidence and clean stop. These gates remain open until those results actually exist. |
| Exact-head GitHub checks | The snapshot `validation-runs/closeout-20261026/github-resume-evening/report.md` confirms all six pushed heads green: Cairn `b334cb2`, Mini-Docker `141b4cc`, DuraFlow `4a83027`, FailForge `c7dd846`, MiniDB `12fae502`, Coordination `6c2026d`. Cairn's three required jobs include the race detector, real stateful spine, actual PostgreSQL/Redis/MongoDB recovery and package installation. This is not evidence for later uncommitted verifier/documentation changes; every final pushed head must be inspected again. |

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
