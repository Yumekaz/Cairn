# Release-candidate validation

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

Do not declare a production release solely from these tests. Record a fresh
Linux installation and host reboot recovery. A clean checkout on the development host is not a fresh VM. This host
has no available QEMU/KVM setup; rebooting it would interrupt unrelated work.
Use a disposable Linux VM for host boot testing. External users and sustained
operation are additional evidence, not something a one-off smoke run establishes.

## Current evidence

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

## Complete matrix observation (2026-09-16)

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
Fresh-VM boot/reboot and sustained-operation validation remain outstanding.
