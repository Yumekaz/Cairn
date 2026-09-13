# Migration safety

`state_touched` means a migration **may** have changed persistent state. Cairn
persists this marker before creating or starting the migration task. Once set,
ordinary deployment updates cannot clear it, including updates made with stale
workflow input after a health-check failure.

Rollback without `--force` rejects targets older than any marked deployment,
including failed and interrupted deployments. A pre-deployment backup does not
prove that data was restored or that an older application remains compatible.

If DuraFlow re-enters a migration step whose deployment is already marked,
Cairn fails that deployment with an explicit uncertainty message instead of
running the migration again. This deliberately includes the window where the
marker was committed but the task had not started. If DuraFlow durably completed
the migration step, later steps can resume normally.

## Recovery procedure

1. Inspect the migration task, its logs, and application data. An interrupted
   daemon does not necessarily stop the migration container.
2. Ensure the task has stopped before restoring a backup or starting another
   migration. Preserve evidence before removing the task.
3. Determine whether to complete the migration, restore data, or deploy a
   compatible application. Submit a new deployment only after that decision.
4. Use forced rollback only after checking schema and data compatibility.

This is a replay and rollback guard, not an atomic database migration protocol.
It does not undo partial writes, stop all old migration tasks automatically,
or establish compatibility between a preserved application and modified data.
Existing records from before this change cannot retrospectively identify
unrecorded migration side effects. Migrations should remain idempotent and
backward compatible where possible.

## Validation

Regression tests cover mutation followed by failure, an interrupted/lost start
response, health failure after successful migration, stale marker updates,
reconstructed-server replay, and rollback guards for successful, failed, and
running deployments. The reconstructed-server tests use a controlled runtime;
they are not substitutes for the privileged live crash proof.

Source revisions for validation on 2026-09-12:

- Cairn `afee632ea33167e43fb7a86077819ce560322a19` plus this working-tree patch.
- DuraFlow `e969c49ed7e76d3eff6da5005f8a9d73ad336b24`.
- Mini-Docker `8c62d1922f3c8807047cc8d7500b3f604db24f6e`.

Passed: daemon/store regression suites, core stability gate, existing DuraFlow
crash-recovery integration test, script syntax, Go vet for daemon/store, fresh
CLI/daemon builds, live clean deploy/backup/restore, rollback guard, and F1/F2
SIGTERM/SIGKILL migration crash cases. Live deployment records retained
`state_touched=1` and reported automatic replay blocked after interruption.

The full proof did not pass: F3 stopped at its baseline HTTP assertion
(`expected F3_OK`, empty response), before runtime fault injection.
The cause of that HTTP failure has not been established.

A separate remaining-cases run passed F4. F5 reported green with
`SAW_PENDING=0` (backup completed before the pending row was observed), so it
does **not** establish a mid-backup interruption. F6 initially reached successful
restore but exited 23 at a `curl | head` dashboard capture. The demo now captures
the full HTTP response before truncating display output, avoiding early pipe
closure under `pipefail`.

On 2026-09-13 the core gate passed again. F3 and F6 both passed on rerun,
including runtime death/restart and the corrected dashboard capture. F3's earlier
baseline failure was not reproduced; its original cause remains unknown.

Preserved logs:

- [Core gate](validation/migration-safety-20260913/stability-gate.txt)
- [F3 and F6 rerun](validation/migration-safety-20260913/f3-f6.txt)

The following original temporary logs were cleared between sessions; their
observed results are summarized above, but the raw files are no longer available:

- `/tmp/cairn-proof-runs/stability_gate_20260912-204216.log`
- `/tmp/cairn-proof-runs/prove_mlp_20260912-204249.log`
- `/tmp/cairn-proof-runs/failure_matrix_20260912-204444.log`
- `/tmp/cairn-proof-runs/failure_matrix_20260912-210412.log` (remaining cases)
