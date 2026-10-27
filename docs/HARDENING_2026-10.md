# Hardening follow-up — October 2026

Updated 27 October. The frozen baseline below completed native, guest and
dashboard proofs, but a reproduced Stop acknowledgement defect required
controller/runtime safety repairs and new evidence. This document records
the repair and baseline, not a blanket certification. Final acceptance uses exact source,
manifest and check records, not an older baseline pass. Earlier results keep
their original provenance.

Evidence links into `validation-runs/`, `validation-vms/` and sibling checkouts
refer to retained local artifacts, not files promised to be published in GitHub.
The [release protocol](RELEASE_CANDIDATE.md#final-evidence-protocol) defines
acceptance without requiring this document to name its own eventual commit.

## Frozen baseline and exact provenance

| Component | Recorded revision and scope |
| --- | --- |
| Cairn | `9126ce1e1bf3c2f21b508720ed2b400fda4703e2`: frozen VM/hosted source and ordinary CI baseline, **not** the new safety implementation. Its two-file change from `0893b85` only corrects hosted bootstrap evidence placement and adds a regression; raw clean-source checks are unchanged. |
| Mini-Docker | `bf1f63f44f09f90e0209a74e1eed1a5da88bdb1a`: frozen installed/VM/hosted runtime, superseding `141b4cc`. Metadata refresh now compares records under the supervisor's exclusive lock; concurrent final records win, deleted records stay deleted and missing exit codes are not guessed success. |
| DuraFlow | `4a83027c623746f9e541423dcc37781fec1c137b`: unchanged workflow dependency, with retained core/real-PostgreSQL and exact-commit verification. |
| FailForge | `c7dd846c304f6994737a18750abc829aad6b6ffb`: unchanged; complete owned-unlock matrix passed **24/24**, seeds 42–47 across both targets/profiles and two repetitions. |
| MiniDB | `12fae5025421026cbe06b042a823719bee5c27cc`: unchanged; **68 tests**, including eight actual-etcd cases, and exact-commit verification passed. |
| Coordination | `6c2026d840985f7084cfc81bebe7c12f924676a0`: unchanged; **368 tests**, no failures/errors/skips, and exact-commit verification passed. |

The native package is separately identified: manifest SHA-256
`824fc98908bf7ea5b6349277e922c708565dbbba099574d9256283221750d4ca`,
installed release `/opt/cairn/releases/824fc98908bf7ea5b634`, built from Cairn
`0893b85f2fca4735ea9e7585e90a470673604dfa`, Mini-Docker `bf1f63f` and
DuraFlow `4a83027`. Its [manifest](../validation-runs/metadata-final-bundle-20261026/manifest.json)
records clean source and artifact hashes. Do not relabel that binary as
`9126ce1` or as the forthcoming safety repair.

## Stop acknowledgement and writer-ownership repair

The dependency lock now selects Mini-Docker
`958097ab6293d10f96267ce2baece1289e7a103b`; the Cairn commit containing this
record identifies its controller, tests and hosted verification workflow.

The private mock-Unix-API [StopACK probe](../validation-runs/closeout-20261026/stop-ack-failing-before.log)
reproduced false success when runtime Stop refused identity verification or
reported stopped-but-cleanup-failed, followed by generic stopped/error Inspect.
The adapter now propagates the Stop error. Inspect is not an acknowledgement of
writer exit or cleanup.

The current repair establishes these scoped rules; it is not yet a final
installed/VM/soak result:

- Physical restore authoritatively Stops the primary runtime and exact
  persisted historical deployment/job containers, rather than trusting cached
  `ActualState` or name-prefix guesses. It repeats this check immediately
  before destructive rename/extraction, including legacy/replayed steps.
  Missing service/runtime identity (except a verified registered cron service
  that intentionally has no primary), failed inventory, active deployment work
  or unverified job completion blocks mutation. Restart re-reads current desired
  state and respects desired-stopped. PostgreSQL/MongoDB logical restore
  remains a separate path.
- Service deletion must not discard service/volume ownership after refused
  Stop or failed Remove, or while a writer job's completion is uncertain.
  Admission barriers cover scheduled and explicit one-off writers; ownership
  must be recorded before deletion or restore can assess it.
- Deployment retirement does not Remove after a refused Stop. Normal web
  cutover remains availability-first: a healthy candidate may be committed
  while old-runtime retirement fails, with ownership retained and the new
  durable [`RuntimeCleanupFailed` warning](events.md#runtime-cleanup-warning-and-uncertainty)
  identifying runtime, phase and error. Deployment success is not proof of old
  cleanup. Conversion to cron refuses retirement failure. Replayed committed
  cutover preserves later operator Stop intent; replay before commit verifies
  previous runtime absence from healthy inventory rather than swallowing a
  Stop refusal.
- Mini-Docker idempotent Stop must use the same verified writer-exit/cleanup
  path for stopped records; unknown identity or cleanup failure refuses
  success, terminal timestamps/codes are preserved, and Remove retains
  metadata on uncertain cleanup. Detached supervisors now use private stdio
  so a ready container cannot hold the caller's captured stdout pipe open.
- One exclusive recorded service owner per volume is the supported model,
  with no untracked external writers. Conditional SQLite writes enforce owner
  retention and atomic restore/deploy/backup workflow admission. A definitely
  rejected deployment is not scheduled or left as an orphan pending candidate.
  These local admission rules are not globally atomic storage or a guarantee
  across arbitrary writers.

Focused [Stop contract](../validation-runs/closeout-20261026/stop-contract-fixed-20261027.log)
and [physical-restore](../validation-runs/closeout-20261026/restore-quiescence-fixed-20261027.log)
regressions passed after retained failing-before runs. An intermediate runtime
passed **216 ordinary-user** and **16 real privileged** cases without privileged
skips, before the latest stdio/helper changes. The final detached-stdio
regression [failed before](../../Mini-Docker/validation-runs/detached-stdio-finaltest-failing-before-20261027.log)
(the attached control passed), then
[both cases passed](../../Mini-Docker/validation-runs/detached-stdio-fixed-20261027.log).
An actual owned rootless
[Start/readiness/Stop/repeated-Stop/Remove proof](../../Mini-Docker/validation-runs/stop-remove-rootless-fixed-20261027/result.json)
passed with unchanged terminal record on repeated Stop. After the filesystem
cleanup repair, **236 ordinary-user tests** and separately **all 16 real
privileged cases** passed without privileged skips. Final Go command/internal
packages, vet, **135 runner tests**, all three dashboard regression scripts and
ten repetitions of the new safety cases passed. Logs are retained in the
closeout directory and `validation-runs/stop-remove-final-root-20261027.{log,xml}`.
These local results do not replace final exact-commit CI, installed/guest or
hosted-session gates.

## Retained baseline evidence

| Gate | Completed baseline evidence and limits |
| --- | --- |
| Authenticated administration | Private credentials, constant-time verification, Host/Origin checks, explicit hostname allowlists, TLS >=1.2 and fail-closed public binding are implemented. Trusted/untrusted TLS handshakes passed. Installed [admin checks](../validation-runs/metadata-installed-admin-20261026.jsonl) returned 401 for missing/wrong credentials, 200 authenticated and 403 for hostile Host/Origin. No public deployment, multi-user authorization or MFA is claimed. |
| Runtime identity/isolation | Baseline `bf1f63f` includes boot/start identity and pidfd signal fences, pinned exec namespaces/root, cgroups, reduced capabilities, filtering, isolated environment, no-follow staging, recursive nodev/nosuid, mandatory pivot/old-root detach, private allowlisted devices and bounded shared memory. It includes whole-proc read-only enforcement, inert masks at start/exec and refusal of legacy unprotected exec. Linux x86_64 kernel 5.12+ is required; missing required enforcement fails closed. This is scoped tested isolation, not independent certification. |
| Runtime verification | `bf1f63f` passed **183 ordinary-user** cases, separately **all 16 privileged** cases without skips ([XML](../validation-runs/metadata-refresh-root-20261026.xml)), and an actual owned rootless Start/readiness/Stop. [Exact-commit root CI](https://github.com/Yumekaz/Mini-Docker/actions/runs/40369934207) also passed all 16 plus ten untraced lifecycle repetitions. Style/fatal lint passed; Bandit found no medium/high findings, with 24 low findings remaining. These results predate the new Stop/Remove/stdio/helper changes. |
| Permanent installation and recovery | Manifest `824fc989…` was applied to boot-enabled `cairn.service` and `cairn-runtime.service` with root-owned packages and a restricted runtime socket. The [native notes proof](../validation-runs/metadata-installed-notes-20261026.json) passed write/restart/redeploy/backup/restore in 35.11 seconds. [Both actual daemon-restart checks](../validation-runs/metadata-installed-recovery-20261026.jsonl) preserved workload PID and data. Prior packages/configuration backups/data remain. The agent did not reboot the real host. |
| Restore/reconciliation baseline | Durable restore reservation and serialized steps repaired the earlier competing restore/reconciler Starts; failing-before evidence and installed flows remain retained. The new StopACK discovery showed those passes were not sufficient to certify physical quiescence under Stop refusal. The new rules above require fresh affected evidence. Deployment rollback is not database undo. |
| Cron lifecycle/task identity | The recorded installed proof passed migration/cron UID:GID 1000:1000, file ownership, 70 seconds without new admission after Stop, and successful jobs after Start/Restart ([status](../validation-runs/writeback-task-identity-20261026/status.json)). Stop pauses future cron admission without killing existing jobs; Start/Restart require an existing valid schedule. New deletion/restore writer-admission work is not covered by this older proof. |
| Dashboard | The installed metadata-runtime baseline passed all **15** [main checks](../validation-runs/metadata-dashboard-final-20261027/result.json) and all **four** [supplemental checks](../validation-runs/metadata-dashboard-events-20261027/result.json), with actual wrapper exit 0 for [main](../validation-runs/metadata-dashboard-final-20261027.exit-status) and [supplement](../validation-runs/metadata-dashboard-events-20261027.exit-status). Checks covered lifecycle/data, logs, modal focus, selected restore, safe/forced rollback, offline/reconnect, real scrolling, escaped Inspect/Hide metadata and polling a correlated new backend event without reload. Sandbox remained enabled; cleanup/screenshots were retained. This is not dashboard proof for the new cleanup warning. |
| Guest installation/recovery | Frozen `9126ce1`/`bf1f63f`/`4a83027` passed **all nine integration cases**, installed notes/data, guest reboot and owned-QEMU abrupt shutdown. [Status](../validation-vms/hardening-release-20261026/status.json) records clean exact inputs and all four gates passed; [attempt log](../validation-runs/hardening-metadata-vm-resume-20261027.log) ends passed, with actual owner exit 0. This reused guest is not a newly pristine OS installation, and it does not include the new safety code. |
| Bounded hosted sessions | [Run 40431200938](https://github.com/Yumekaz/Cairn/actions/runs/40431200938), jobs `121949030337` (baseline) and `121949030036` (higher rate), both completed successfully. Downloaded artifacts passed checksums and strict validation: respectively 7200.014 seconds/481 samples/1924 requests and 7200.021 seconds/1434 samples/14340 requests, zero errors/restarts, verified restore, resource evidence, backend stop and required cleanup. These remain **baseline-only**, not proof of the newer repair. The [ledger](../validation-runs/soak-sessions.json) preserves the initial incomplete higher-rate download separately from its revalidated download. |
| Exact-commit GitHub checks | The retained [27 October ordinary-CI snapshot](../validation-runs/closeout-20261026/github-resume-20261027/report.md) records all six baseline revisions above green, including Cairn [40373392710](https://github.com/Yumekaz/Cairn/actions/runs/40373392710). Ordinary CI does not close hosted/native/VM/dashboard acceptance; later uncommitted safety changes are not covered. |

The unchanged [FailForge matrix](../../FAILFORGE/validation-runs/release-matrix-owned-unlock-20261026/summary.json)
retains all 24 passes. Its unlock-routing repair did not change RNG draws,
weights, faults, negative probes, coverage minima or checkers. MiniDB's optional
[etcd fencing](../../Mini-Redis-Cassandra/docs/fenced-writes.md) is experimental,
loopback-only, fixed-membership and ALL-acknowledgement; it is not global
linearizability, dynamic membership, automatic authority renewal or production
split-brain certification. Coordination's lease-clock evidence covers the
process-local monotonic-time boundary, not every concurrent/partition history.
No relevant source/campaign changed in these four projects; completed results
need not be rerun for unrelated Cairn/runtime fixes.

## Historical failures — retained, not relabelled

| Attempt/problem | Retained result and evidence |
| --- | --- |
| Earlier isolation/fixtures | `ad03a78` writable global proc controls, prior red privileged runs and the nodev-`/tmp` fixture failure remain historical failures, not security passes. See the [detailed validation history](VALIDATION_2026-10.md) and [proc review/CI records](../validation-runs/closeout-20261026/github-proc-runtime/). |
| Untraced Stop timeout | The [`b3628db` attempt](../validation-runs/closeout-20261026/github-runtime-b3628db-attempt2-job-121554365250.log) established OverlayFS last-mount writeback (`wb_wait_for_completion → ovl_sync_fs → cleanup_mnt → do_exit`), not merely a traced-process artifact. Repair `9d6b339` pinned the verified mount namespace before signalling, kept the supervisor-exit fence and released it after finalization; it did not disable writeback or lengthen acceptance deadlines. Storage cleanup can still delay Stop. |
| Guest setup/interruption | The 300-second offline-venv setup failure and subsequent host-poweroff interruptions remain in [archived VM statuses/sources](../validation-vms/hardening-release-20261026/). The explicit 900-second venv-only budget did not weaken application/readiness deadlines. The runner records interruption, retains partial gates and owns only its actual QEMU child; stale RUNNING is never a pass. |
| PostgreSQL task exit-code loss | `7ab7d11`/`141b4cc` ran nine guest cases: eight passed, PostgreSQL restore failed with stopped task/missing exit code. [Failed invocation](../validation-runs/closeout-20261026/vm-7ab7d11-failed-invocation.log). Nine deterministic failing-before cases established the metadata race repaired in `bf1f63f`; the later baseline guest pass does not erase this failure. |
| Rootless capture-pipe timeout | A ready detached container left captured stdout held by its supervisor, timing out the CLI caller at 40 seconds. Retained [regression failure](../../Mini-Docker/validation-runs/detached-stdio-finaltest-failing-before-20261027.log), fixed cases and the new owned rootless proof are above. Earlier incomplete rootless attempts receive no pass credit. |
| Browser wrapper/sandbox | Earlier exit 143 and service-context sandbox startup failures remain failed/incomplete. The accepted [bounded-scope proof](../validation-runs/closeout-20261026/browser-scope-proof.md) inherits the application caller's security context; neither Chromium's sandbox nor host security policy was disabled. |
| Campaign/soak failures | The first matrix remains **20 passes/four NO_COVERAGE failures**. Local soak interruptions (1027, 572.716, 2876.970 and 1868.135 seconds), cancelled queued sessions and the [first hosted bootstrap failure](https://github.com/Yumekaz/Cairn/actions/runs/40371609787) receive no duration credit. See the [session ledger](../validation-runs/soak-sessions.json) and retained [bootstrap failure log](../validation-runs/closeout-20261026/hosted-first-failed-job-121743520940.log). |

## Final evidence and explicit boundaries

The final safety-code guest attempt completed eight integration cases before
the unchanged 15-minute **whole-suite** watchdog expired during the final
MongoDB case. The eight passes totalled 781.53 seconds, leaving 118.47 seconds;
the timeout trace is retained, not a successful VM gate. Hardware KVM was then
verified on the host with a paused diskless probe. The verifier now accepts an
explicit `--acceleration kvm` execution profile, requiring existing device
access and KVM API 12; denial or an unsupported device fails closed. It never
changes device permissions or silently falls back. Default TCG behavior,
the two-vCPU/2-GiB/8-GiB fixture limits, all nine tests and their assertions,
the 15-minute watchdog and application deadlines remain unchanged. Acceleration
is recorded in status. This is a verifier-only change: it must get its own
exact-commit CI and hosted provenance, without relabelling earlier binaries or
timed sessions. Software-emulation throughput is not a native-service SLA.

Follow the [final-evidence protocol](RELEASE_CANDIDATE.md#final-evidence-protocol)
after the last safety/helper change: complete regressions and actual
ordinary/rootless/privileged exits; freeze clean exact sources; install a new
checksummed package; collect affected native/VM/dashboard proofs; complete and
validate two independent hosted sessions on the new implementation; then
inspect required GitHub checks for every actual final pushed head. The final
snapshot must use `--require-hosted-soaks`. No pending, failed, skipped,
missing or unverified required gate becomes green from older baseline proof.

- Save commands, exact revisions, manifest/artifact digests, exit codes, seeds,
  logs and screenshots outside `/tmp`. Preserve every failed/interrupted
  attempt; retries are explicitly named new results.
- Distinguish source compilation, checksum-verified binary installation and
  actual installed revision. A source-equivalence record is not a renamed
  binary. Required GitHub race instrumentation is separate evidence from
  ordinary local tests.
- Administrative Unix/runtime socket access is privileged; the runtime socket
  group is root-equivalent. Authenticated TLS and scoped automated isolation
  tests are not a professional independent assessment. Public exposure requires
  explicit trusted certificates and hostnames.
- Physical restore means filesystem/volume restoration, **not physical
  power-loss testing**. QEMU death does not establish safety against every real
  disk/controller/power failure. Never reboot the real host as a shortcut.
- One exclusive managed volume owner does not fence arbitrary host processes
  or other controllers. Cleanup uncertainty retains ownership and refuses
  destructive operations; availability-first cutover may still retain an old
  runtime and emit a warning.
- Deployment rollback does not undo database writes; MongoDB restore is not
  globally atomic. Installation is not database migration or transactional
  activation rollback. Optional etcd fencing does not turn standalone MiniDB
  election into consensus-grade replication.
- Two completed bounded sessions are not uninterrupted 24-hour operation;
  interrupted durations are never added together. No global reliability,
  security, storage or split-brain guarantee is claimed.
