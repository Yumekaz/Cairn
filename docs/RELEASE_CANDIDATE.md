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

Do not declare a production release solely from the isolated tests. Record a
successful full F5 HTTP interruption, fresh Linux installation, and host reboot
recovery. A clean checkout on the development host is not a fresh VM. This host
has no available QEMU/KVM setup; rebooting it would interrupt unrelated work.
Use a disposable Linux VM for host boot testing. External users and sustained
operation are additional evidence, not something a one-off smoke run establishes.

## Current evidence

The recovery endpoint and isolated SIGKILL tests pass, as do the Cairn internal
Go packages and shell/Python syntax checks. The isolated backup test observed
pending metadata and archive bytes before SIGKILL, then verified startup repair
marked the backup failed. The notes workload and updated F1/F2 scripts are
implemented but their latest live run is not certified. The combined live
validation launch was blocked twice by automatic execution-approval review
timeouts. The existing live F5 HTTP experiment also failed to observe its
barrier and remains an open integration issue; F5ARCHIVE is not its replacement.
