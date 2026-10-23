# Hardening follow-up — started 2026-10-23

This is a new hardening milestone, not a relabelling of October 14 acceptance
as a security certification. Earlier acceptance, separately completed two-hour
soaks, GitHub runs and interrupted attempts retain their original results.

## Current implementation and evidence

| Gate | State / retained evidence |
| --- | --- |
| Native TCP admin authentication/TLS | Implemented private random credential creation, Basic authentication, Host/Origin checks, explicit hostname allowlists, TLS >=1.2 and fail-closed public bind. Core tests pass in `validation-runs/hardening-cairn-core-20261023.log`; focused tests also exercise actual trusted/untrusted TLS handshakes. No public listener/certificate/DNS deployment has been performed. |
| Runtime process identity and exec safety | Boot ID/start ticks plus pidfds prevent persisted numeric PIDs from authorizing signals. Unknown legacy live identities fail closed. Namespace/root handles are pinned; PID namespace entry forks correctly. Exec now applies cgroups, capabilities/seccomp, workload identity and an isolated environment. A CLOEXEC startup acknowledgement prevents exec entering an unfinished root. Six privileged cases passed in `hardening-runtime-root-ready-20261023.{log,xml}`. Earlier failing attempts are retained. |
| Normal-user runtime and security scanner | Final readiness suite: 99 passed; six root-only cases reported skipped in the ordinary-user run and passed separately under root. Evidence: `hardening-minidocker-ready-20261023.{log,xml}`. Medium/high Bandit scan passed with zero source suppressions in `hardening-runtime-bandit-20261023.log`; low-severity findings are not claimed absent. |
| Root-owned permanent installation | Installed and boot-enabled `cairn.service` and `cairn-runtime.service`, both API-ready. Six installer regressions and all fourteen runner regressions passed. Actual installed notes write/restart/redeploy/backup/restore passed (`hardening-installed-notes-20261023.log`); missing/wrong credentials return 401 and hostile Host/Origin return 403 (`hardening-installed-admin-20261023.log`). Existing state/config backup retained; no host reboot occurred. Initial missing-venv failure remains retained; official venv prerequisite was installed. Daemon-restart proof exposed a runner readiness gap and repeated Start failure; fixes and final replay pending. |
| Final-revision guest installation/reboot/crash | Pending new proof. The previously passed guest revision is historical, not proof of these changes. |
| MiniDB distributed fencing | In progress in a disjoint agent scope. Optional established consensus authority is preferred over claiming the custom election is consensus-grade. Integration and partition tests must be reviewed before any guarantee is changed. |
| Coordination process-local clock safety | In progress in a disjoint agent scope. Tests/review pending. |
| Broader repeatable fault campaigns | In progress in a disjoint FailForge scope. Tests, causal fault audit and retained seed range proofs pending. |
| Exact-commit GitHub checks | Mini-Docker `9a2e5b4f6d7a7d38f7d636bb87fbe8dcf7574925` passed Tests, Lint and Root Runtime Validation (`github-hardening-runtime-20261023/report.json`). Cairn/agent hardening changes still require their own final pushed-head evidence; other green heads in that report predate these working-tree changes. |

Evidence paths in the table without a prefix are under `validation-runs/`.
Offline installation bundles contain checksummed artifacts and source revision/
dirty-state metadata; they do not contain admin tokens, host keys or TLS keys.
An earlier bundle is retained as superseded, not installed as final code.

The local race-detector attempts remain non-green prerequisite evidence: cgo
was disabled, and explicit `CGO_ENABLED=1` then found no C compiler. The required
GitHub core job now runs `CGO_ENABLED=1 go test -race ./internal/...`; its result
must be checked rather than inferring a race pass from ordinary tests. Core
tests after the repeated-Start fix passed in
`validation-runs/hardening-cairn-start-recovery-20261023.log`. The installed
binary predates that final lifecycle fix until the next installation receipt.

## Limits that remain explicit

- Authenticated administration is not multi-user authorization/MFA or an independent isolation audit.
- No public service will be exposed without an explicitly configured trusted certificate and hostname.
- VM process death is not an emulation of every physical disk controller/power-loss failure.
- Separate 2–4-hour sessions must remain individually recorded; they never become an uninterrupted 24-hour claim.
- Etcd ownership, if implemented, is not by itself proof of global linearizability, atomic multi-shard writes or replica durability. Resource-side stale-owner rejection must be tested.
- An independent adversarial audit requires a reviewer independent of the implementation. Local coding agents and automated scans do not establish that gate.

Do not mark the whole six-project hardening milestone complete while the
required implementation, final-revision proof or exact-commit checks above
are pending/failed/unverified.
