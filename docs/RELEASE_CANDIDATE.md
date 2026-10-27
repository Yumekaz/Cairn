# Release-candidate validation

Updated 27 October 2026. This protocol separates the passed frozen baseline
from the StopACK, restore, deletion and runtime-cleanup repair. The
[hardening record](HARDENING_2026-10.md) records provenance; acceptance of the
commit containing this document requires the exact-source gates below. Older
baseline results alone never establish release closeout.

Frozen Cairn `9126ce1e1bf3c2f21b508720ed2b400fda4703e2` with Mini-Docker
`bf1f63f44f09f90e0209a74e1eed1a5da88bdb1a` passed all nine guest integration
cases, installed-data checks, guest reboot and owned-QEMU abrupt shutdown;
the owner exited 0. See the retained
[VM status](../validation-vms/hardening-release-20261026/status.json) and
[attempt log](../validation-runs/hardening-metadata-vm-resume-20261027.log).
Those results predate the new safety changes. Hosted run
[40431200938](https://github.com/Yumekaz/Cairn/actions/runs/40431200938) was
completed both independently validated two-hour sessions on that same baseline;
it cannot prove the later code. Local evidence links require the retained
validation directories; they are not a promise that ignored artifacts are
published in GitHub.

The dependency pair is fixed by `stack.lock`; the Cairn commit containing it
identifies the complete source set. Run `bash scripts/verify_stack.sh` before
collecting evidence. Bootstrap checks existing siblings without changing them;
new sibling clones are checked out at the locked commits. CI reads the same
DuraFlow revision. Cold-clone verification refuses nonempty output directories.

## Operator recovery

```
cairn recovery counter-api
cairn recovery counter-api --json
```

The read-only report lists marked unsuccessful deployments, task names and
observed task states, configured volumes, earlier successful backup candidates,
and recovery instructions. Unknown task state requires inspection. A listed
backup is not automatically integrity-checked or schema-compatible. Configuration
contents and environment variables are not returned. Historical resolved entries
remain visible because the safety marker is intentionally permanent.

## Proofs

```
N=1 SKIP_LIVE=1 bash scripts/stability_gate.sh
CASE=F1,F2,F3,F4,F5ARCHIVE,F6 bash scripts/failure_matrix.sh
python3 scripts/notes_smoke.py
SKIP_DEMO=1 bash scripts/cold_clone_verify.sh
```

F1/F2 now wait for an actual migration write, then assert exactly one recorded
execution, persisted uncertainty, blocked replay, and retention of the baseline.
F5ARCHIVE is explicitly an isolated subprocess test using the production archive
and startup-repair functions. It SIGKILLs only after pending metadata and nonempty
archive output are observable. This is different from F5, the full live HTTP
backup test. F5 now fails if it misses the in-flight operation; it cannot report
a fast completed backup as a successful interruption proof.

The notes proof creates unique service and volume names, checks HTTP writes,
restart, redeployment and backup restoration, and stops the service afterwards.
Data and backups remain for inspection. It uses port 8088 by default and refuses
an occupied port (`NOTES_PROOF_PORT` overrides it). Run on a trusted test host.

## Remaining release gates

The September statement that this host had no QEMU/KVM setup is historical,
not a current limitation. QEMU guest installation/reboot/abrupt-shutdown proof
now exists for the frozen baseline linked above. Reusing an existing guest is
not a newly pristine operating-system installation. Never reboot the real host
as a verification shortcut; QEMU SIGKILL does not test every physical disk,
controller or power-loss failure.

The new safety implementation requires its own evidence. Its supported storage
scope is one exclusive recorded service owner per volume, with no untracked
external writers. Exclusive-owner enforcement and atomic admission of
restore/deploy/backup workflows are still being completed and verified; neither
the baseline nor a process-local lock establishes globally atomic storage.

### Final-evidence protocol

The VM runner defaults to software TCG for compatibility. Where `/dev/kvm` is
already accessible, select `--acceleration kvm` explicitly for hardware-backed
proof. The verifier checks the kernel API and records the selected mode; it
does not grant permissions, switch modes on failure, extend the 15-minute
integration watchdog, reduce the test list or change application deadlines.
Keep a failed TCG timing attempt separate from a later KVM result.

1. Finish the safety implementation and review its failure paths. Retain
   failing-before/passing-after StopACK, idempotent Stop, cleanup-retention,
   physical-restore replay, desired-stopped, deletion/job-admission,
   exclusive-volume-owner and workflow-admission regressions. Include detached
   CLI capture-pipe and attached-control cases. Run the complete relevant
   Cairn/runtime suites, lint/vet, race checks and all real privileged cases
   without skips after the **last** runtime/helper change; intermediate counts
   are not final results. Record actual command exits and an owned rootless
   Start/readiness/Stop/repeated-Stop/Remove proof with unchanged terminal data.
2. Freeze clean source revisions for all six repositories, update the runtime
   pin, and retain exact 40-character commits, raw working-tree status, relevant
   diffs and artifact hashes. Evidence must name the commit that was tested,
   not require this document to contain its own eventual commit hash.
   Documentation-only follow-ups need an explicit source-equivalence record;
   they must not relabel a binary built from an earlier commit.
3. Build and checksum a new package from that frozen source set. Record the
   manifest digest and actual installed revisions. Verify authenticated admin,
   notes write/restart/redeploy/backup/restore, both daemon-restart PID/data
   checks, CLI refusal/retry behavior and the new durable
   [`RuntimeCleanupFailed`](events.md#runtime-cleanup-warning-and-uncertainty)
   warning. Preserve prior packages, configuration backups and failed evidence.
4. Use a new named, bounded VM attempt with exact source/artifact provenance.
   Require all nine integration cases without skips, installation and retained
   data, guest reboot, owned-QEMU abrupt shutdown, **a newly acknowledged write
   and exact readback after crash**, terminal passed status and a durable actual
   owner exit 0 receipt. A transient unit's default inactive/exit-zero fields
   after garbage collection are not a retained raw exit. Retain each earlier failed/interrupted status and source
   archive. Verify dashboard lifecycle/restore/rollback and the warning's event
   metadata/polling with backend data assertions, sandbox enabled, cleanup and
   actual browser-wrapper exit 0; the baseline's 15+4 checks are not a replay
   of the new code.
5. Run two separately recorded 7200-second sessions on independent standard
   GitHub-hosted Linux runners using the new frozen implementation. Use the
   opt-in `bounded-soaks` workflow (manual dispatch or an intentionally pushed
   `codex/hosted-soaks-*` acceptance branch); ordinary main CI is separate.
   Do not count short smokes, interrupted durations, or baseline run
   `40431200938`. Require successful terminal jobs, each sampler's actual exit
   0, a post-target terminal sample, full retained samples/resources, zero
   errors/unexpected controller/runtime/workload restarts, verified final
   restoration, exact six-repository clean-source checks, actual backend
   fixture-stop proof and marked-unit cleanup. Preserve failed attempts; no
   automatic retries or summing durations.
6. Download and checksum the allowlisted non-secret evidence for both jobs.
   Run `scripts/verify_soak_session.py --status <session-status.json>
   --expected-revisions <six-commit-map.json>` separately for each, with all
   six exact frozen SHAs. Require `verified.json`, `sampler.exit-status` equal
   to 0, `fixture-cleanup.json` proving stopped/PID 0, and successful
   `cleanup.txt`; a GitHub success badge alone is insufficient. Then capture
   every final pushed head's required GitHub checks, including Cairn race,
   stateful spine, real PostgreSQL/Redis/MongoDB recovery and package jobs.

From the frozen checkout, use a **new, nonexistent output directory** for each
snapshot, for example:

```sh
python3 scripts/github_ci_snapshot.py --require-hosted-soaks --output validation-runs/github-release-final
```

This command reads the actual six local heads and requires completed successful
`bounded-soaks` on the exact Cairn commit. It does not replace per-artifact
validation or the native/VM/dashboard gates. Pending, missing, failed, skipped
or unverified required evidence stays non-green. A later source change requires
new affected evidence. Unchanged DuraFlow, FailForge, MiniDB and Coordination
results remain scoped to their recorded revisions; their completed campaigns
need not be repeated for unrelated controller/runtime repairs.

## Historical evidence — September 2026

The observations below retain their original dates, revisions and limitations.
They are not proof for the October safety changes.

The recovery endpoint and isolated SIGKILL tests pass, as do the Cairn internal
Go packages and shell/Python syntax checks. The isolated backup test observed
pending metadata and archive bytes before SIGKILL, then verified startup repair
marked the backup failed. F5ARCHIVE remains separate from the live F5 test.

On 2026-09-16, strengthened F1 exposed missing workflow input during recovery.
Workflow input is now persisted with checked errors before DuraFlow scheduling.
Metadata snapshots now use SQLite VACUUM INTO, including committed WAL contents,
instead of copying the open main database file; snapshot files are private and
synced. Tests cover snapshot WAL contents and persistence-before-scheduling.
With these changes, F1 and F2 passed exactly-once-observed migration execution,
persisted uncertainty, replay blocking and baseline retention. The live F5
HTTP run then passed with SAW_PENDING=1 and observed archive bytes before SIGKILL.
These results validate the tested failures, not general exactly-once execution
or compatibility of arbitrary database migrations.

The build-only cold-clone check subsequently passed on 2026-09-13 for Cairn
`a8de35e0fd79ae1f1d6c7988f6fdefba6240f96a` and the two locked sibling revisions.
It fetched all three repositories into `/tmp/cairn-cold-clone.1BsZix`, built and
installed the CLI/daemon, and passed deploymeta, daemon, config, preflight and
store tests with Go 1.26.4. This validates a clean checkout on the same host;
it does not certify a fresh operating system, boot persistence or live operation.

The notes HTTP workload subsequently passed write/restart/redeploy/backup/restore
on 2026-09-13 in 37.21 seconds after correcting the smoke client's duration
encoding to JSON nanoseconds. Service `notes-proof-62a235f5` was stopped after
the proof; volume `notes-proof-62a235f5-data` and backup
`9d3b6e39-9aac-42c7-963c-bc5fe6105791` were retained for inspection. The journal
contained both notes after restart and redeploy, and only the first after restore.

## Historical complete matrix observation (2026-09-16)

On Cairn `ce88d200368a4bcbcf7bf211fb4c4b7f3e03be27` with the locked siblings,
the full `prove_mlp.sh` invocation reported green core/stability checks, clean
deploy/backup/restore, migration crash recovery, and rollback safety. Its live
matrix reported GREEN for F1 through F6, including F5 with `SAW_PENDING=1`,
and ended with `ALL REQUESTED CASES GREEN`. The recovery JSON command also
returned marked deployments and operator actions against the live daemon.

This summary records tool output observed during the session. The wrapper's
final process exit was not collected before interruption; its temporary logs
were cleared before continuation on 2026-09-17. Do not treat this as a retained
raw-log certificate for the wrapper. The earlier focused migration/backup run
is retained in [migration-and-backup.txt](validation/release-20260916/migration-and-backup.txt).

For future runs, set `LOG_DIR` to a persistent evidence directory. Runtime startup
now appends to its log instead of truncating prior matrix output on restart.
At that September checkpoint, fresh-VM boot/reboot and sustained-operation
validation were outstanding. Use the [October hardening record](HARDENING_2026-10.md)
for the completed frozen-baseline guest proof and the still-open final gates.
