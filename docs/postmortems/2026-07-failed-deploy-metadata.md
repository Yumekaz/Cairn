# Postmortem: Failed deploy left wrong `current_deploy_id`

**Date:** 2026-07-13  
**Target:** Cairn (SERVER) — FailForge / reliability closed loop  
**FailForge config:** `tests/failure/cairn_failforge.yml`  
**Seed:** `42`  
**Retained FailForge history:** `FAILFORGE/runs/cairn-42/` (`events.jsonl`, `history.sqlite`, `faults.json`)  
**Re-run:** `failforge run tests/failure/cairn_failforge.yml --seed 42` on 2026-07-13 → **Run completed successfully** (see implementer `failforge_rerun.log`)

## Failure mode

After a **successful** deploy, a subsequent **failed** deploy (health-check 404) correctly:

- left the healthy container runtime serving traffic, and
- removed the failed candidate container.

But SQLite metadata was wrong:

- `services.current_deploy_id` pointed at the **failed** deploy row
- `deploys.previous_deploy_id` on the candidate was often empty

This broke the product story for failed-deploy protection: runtime was safe, control-plane history was not.

The same premature `CurrentDeployID = deployID` assignment also existed on the **env/secret redeploy** path (`triggerServiceRedeploy`), which would clear current on failure via `AfterFailure("")` if `PreviousDeployID` was unset.

### Root cause

1. `handleCreateService` set `svc.CurrentDeployID = deployID` **before** the DuraFlow deploy workflow finished.
2. `triggerServiceRedeploy` did the same for env updates.
3. `failDeploy` marked the deploy as failed but did not restore `current_deploy_id` to the last successful deploy (and had no `PreviousDeployID` to restore).

## Fix

1. Pure rules in `internal/deploymeta` (`PrepareCandidate`, `AfterSuccess`, `AfterFailure`).
2. Candidate deploys record `PreviousDeployID` and **do not** become current until success (create + env redeploy paths).
3. `failDeploy` restores `CurrentDeployID` via `AfterFailure(PreviousDeployID)`.
4. Success path sets `CurrentDeployID` via `AfterSuccess(deploy.ID)`.
5. `cairn inspect` prints `Current Deploy ID`; `scripts/clean_demo.sh` asserts it is unchanged after a broken deploy.

## Re-run outcome

| Check | Result |
|---|---|
| `go test ./internal/daemon/ -run 'FailDeploy\|TriggerService'` | **PASS** |
| Live broken deploy after healthy deploy | `Current Deploy ID` unchanged; failed row has `previous_deploy_id` |
| `scripts/clean_demo.sh` | **PASS** (asserts `AFTER_DEPLOY == SUCCESS_DEPLOY`) |
| FailForge seed 42 re-run (`cairn_failforge.yml`) | **Run completed successfully** (2026-07-13); history under `FAILFORGE/runs/cairn-42/` |

Residual: FailForge workload checkers do not yet deep-inspect SQLite `current_deploy_id`; metadata correctness is gated by unit tests + clean_demo inspect assert. MiniDB FailForge loop is out of scope for this postmortem.

## Lessons

- Runtime protection without metadata protection is incomplete.
- Deploy identity must flip only on commit (success), like a two-phase release.
- Every deploy entry path (CLI deploy, env redeploy, rollback) must share the same metadata rules.
