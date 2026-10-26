# Hardening follow-up — October 2026

Updated 26 October. This milestone does not relabel earlier acceptance as a
security certification. Historical passes, failures and interruptions retain
their original revisions and results. **The hardening closeout is not complete.**

## Implementation and retained evidence

Evidence paths below are relative to this checkout unless a repository is named.
They identify retained local results, not files promised to exist on GitHub.

| Gate | Current evidence and remaining work |
| --- | --- |
| Authenticated administration | Private credential creation, constant-time credential verification, Host/Origin checks, explicit hostname allowlists, TLS >=1.2 and fail-closed public binding are implemented. Native trusted/untrusted TLS handshake tests passed. Installed missing/wrong credentials return 401, correct credentials 200 and hostile Host/Origin 403 (`validation-runs/hardening-release-admin-20261026.jsonl`). No public deployment or multi-user authorization/MFA is claimed. |
| Runtime identity and isolation | Boot/start identity and pidfds protect lifecycle signals; exec uses pinned namespaces/root, cgroups, reduced capabilities, filtering and an isolated environment. Revision `ad03a78f74de51cc6bd6c39ef08c76dd6735e58c` additionally confines staging with no-follow directory handles, requires recursive nodev/nosuid, mandatory pivot/old-root detach, private allowlisted devices and bounded private shared memory. No chroot downgrade or fail-open mount boundary remains. Linux x86_64 kernel 5.12+ is required; missing enforcement fails closed. |
| Runtime verification | All **15 privileged** namespace/container cases passed without skips (`validation-runs/runtime-final-root.{log,xml}`). All **152 ordinary-user** cases passed; the same 15 privileged cases are separately skipped in that user run, not omitted from privileged proof (`runtime-final-user.{log,xml}`). Formatting/fatal lint passed; Bandit found no medium/high findings (`runtime-final-format.log`, `runtime-final-bandit.log`). Low findings are not claimed absent. This is scoped testing, not an independent hostile-tenant audit or proof on every rootless host. |
| Permanent installation | Actual boot-enabled `cairn.service` and `cairn-runtime.service` use root-owned packages and a restricted root-equivalent runtime socket. The ordinary-user controller retains its private data layout. Bundle `c10efb6a474518c8fb82f800846441d7c7dddc1c9c6b609f79f9e9e03e1d4ca2` installed the new runtime; the previous package and configuration backup remain (`validation-runs/hardening-release-apply-fixed-20261026.log`). The first attempt rejected the documented default prefix before changing either service; a narrow validation repair and 18 installer regressions pass. No real-host reboot occurred. |
| Restore/reconciliation reliability | A new live notes restore reproduced competing restore/reconciler Starts and network-setup failure. Its journal remains in `validation-runs/restore-reconcile-live-failure-20261026.log`. The deterministic regression fails on the old code for both pending and running durable restores (`restore-reconcile-before-20261026.log`). A durable restore reservation plus lifecycle-serialized restore steps passes ten fixed repetitions and the complete internal suite (`restore-reconcile-after-20261026.log`, `cairn-restore-repair-all-20261026.log`). Installation and repeated live proof of this repair are still required. |
| Dashboard | Earlier navigation/control/backend/data proofs remain historical. A fresh authenticated, sandbox-enabled browser runner now checks named disposable fixtures, real journal bytes, lifecycle controls, logs, backup/restore/rollback, modal focus and offline/reconnect without page reload. The first new fixture failed during restore before browser execution; it is not a dashboard pass. A final authenticated browser result and screenshots remain required. |
| Final-revision guest proof | Required and still open. The old guest's interrupted provisioning/dpkg failure and subsequent SSH boot timeout are retained in `validation-vms/hardening-final-20261023/`. Neither reached the new application gates. A fresh bounded guest with deterministic private fixture networking must prove the actual packaged units, boot enablement, notes data, all integration cases, guest reboot and owned-QEMU abrupt shutdown recovery. The old successful guest revision is historical only. |
| MiniDB fencing | Revision `12fae5025421026cbe06b042a823719bee5c27cc` implements optional etcd-backed resource-side stale-owner rejection. All 68 tests passed, including eight actual-etcd cases; exact-commit GitHub verification passed. The supported mode is explicitly experimental, loopback-only, fixed membership and ALL acknowledgements. It is not global linearizability, dynamic membership, automatic authority renewal or production split-brain certification. See MiniDB's `docs/fenced-writes.md`. |
| Coordination | Revision `6c2026d840985f7084cfc81bebe7c12f924676a0` passed all 368 tests with no failures/errors/skips and exact-commit GitHub verification. The lease-clock tests cover the process-local monotonic-time boundary; selected campaigns do not establish every partition/concurrent history. |
| Fault framework and campaigns | Repeated-kill scheduling, TTL-aware lock checking and deterministic minimization regressions are repaired. Both complete recorded lock histories are mandatory corpus tests, not optional external fixtures. The first final matrix retained **20 passes and four NO_COVERAGE failures**, not a pass (`FAILFORGE/validation-runs/release-matrix-20261026/`). Workload revision `c7dd846c304f6994737a18750abc829aad6b6ffb` fixes unlock routing to actually observed owned keys without changing RNG draws, operation weights, faults, negative probes, coverage minima or checkers. The full Go suite and vet passed; its fresh 24-attempt matrix is in `FAILFORGE/validation-runs/release-matrix-owned-unlock-20261026/` and must finish before closing this gate. No matrix sources may change while its owner is live. |
| Bounded load sessions | Historical October two-hour sessions remain passed only for their original revisions. The new 30-second runner smoke passed with final restoration and resource evidence (`validation-runs/hardening-release-soak-smoke-20261026/`), but does not count as an acceptance session. Two separately recorded 7200-second hardening sessions remain required after the reliability repair is installed. Each must retain terminal sampled duration, zero errors, restoration and CPU/memory/disk/process-identity evidence. |
| Exact-head GitHub checks | Runtime `ad03a78` passed all three actual workflows, including Root Runtime Validation. DuraFlow `4a83027`, MiniDB `12fae50` and Coordination `6c2026d` have exact-head green evidence. Cairn and FailForge's newest changes still require completed exact-head evidence; pending is not green. Fresh six-repository snapshots are retained under `validation-runs/closeout-20261026/`. |

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
