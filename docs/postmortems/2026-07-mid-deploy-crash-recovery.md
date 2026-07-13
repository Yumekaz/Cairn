# Postmortem / proof: mid-deploy `cairnd` kill and recovery

**Date:** 2026-07-13  
**Target:** Cairn + DuraFlow worker lease reclaim  
**Demo:** `scripts/mid_deploy_crash_demo.sh`  
**Automated:** `go test ./tests/integration/ -run TestDuraFlowCrashRecovery`

## Failure mode under test

Operator (or chaos) sends **SIGTERM to `cairnd` while a deploy workflow is mid-step** (migration sleep / container start). Without correct handling:

1. Deploy may be marked **failed** because step code treated `context.Canceled` as a real failure and called `failDeploy`.
2. On restart, reconciliation may race with an in-flight candidate (active deploy not detected after `current_deploy_id` only flips on success).

## Fix

1. **`isWorkflowInterrupted` / `failDeployUnlessInterrupted`** — do not mark deploy failed on `context.Canceled`; leave pending for resume.
2. **Migration wait loop** — on cancel, return without tearing down / failing deploy.
3. **`ServiceHasActiveDeploy`** — reconciliation skips services with any non-terminal deploy (not only when `current_deploy_id` matches).
4. **Startup log** — incomplete workflows/deploys are logged so recovery is visible.
5. DuraFlow worker already reclaims **RUNNING** steps after **lease expiry** (~10s) via `GetIncompleteRuns`.

## How to re-run

```bash
export CAIRN_ROOTFS=/path/to/Mini-Docker/rootfs
# Mini-Docker + cairnd healthy
./scripts/mid_deploy_crash_demo.sh
```

Expected: kill mid slow-migration deploy → restart cairnd → no stuck pending deploys → `current_deploy_id` is a **success** row → HTTP traffic still serves volume data.

## Residual

- Step input/state for candidate container IDs is recovered via deterministic names (`cairn-<svc>-<deploy8>`), not a durable step-state map.
- Full crash during overlay create can still leave runtime orphans; reconciliation cleans non-active `cairn-*` names.
- Lease reclaim waits up to ~10s after kill before the new worker re-runs the interrupted step.
