# Cairn Roadmap

This document outlines upcoming phases and **honest status** for reliability work.

---

## 📡 Phase 19: Single-node operational maturity (**done**)

**Goal**: Honest ops depth — complete event story, crash-loop visibility, rollback safety. **Not** multi-node (Phase 18).

| Track | Focus | Status |
| --- | --- | --- |
| A | Event taxonomy (MLP §17) | **Done** — full deploy/health/route/backup/restore story + `clean_demo` asserts |
| B | Continuous health / crash-loop | **Done** — reconcile emits `ServiceRestarted`; crash-loop stop thrash (5/10m); F4 asserts event |
| C | Rollback safety / state-touched | **Done** — `RollbackBlocked`/`RollbackForced` events + unit tests + `scripts/rollback_safety_demo.sh` |
| D | Demo hygiene | **Done** — portable FailForge bootstrap paths, `demo_reset.sh`, gate/docs list Phase 19 proofs |

**Proof scripts:** `clean_demo.sh` (events), `failure_matrix.sh` (F4 restart events), `rollback_safety_demo.sh`, `demo_reset.sh`, `stability_gate.sh` (`RUN_ROLLBACK_DEMO=1` optional).  
**Unit always:** `N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh`. Live Mini-Docker demos need local Linux + root networking.

Event reference: [events.md](events.md).

---

## 🔬 Phase 17: Failure testing (single-node)

**Goal**: Systematic failure testing to validate platform correctness — **not** multi-node chaos.

### Status

| ID | Failure | Automation | Status |
| --- | --- | --- | --- |
| F1 | SIGTERM `cairnd` mid-migration | `scripts/mid_deploy_crash_demo.sh` / `CASE=F1` | **Done** |
| F2 | SIGKILL `cairnd` mid-migration | `KILL_SIGNAL=SIGKILL` / `CASE=F2` | **Done** (scripted) |
| F3 | Mini-Docker daemon death | `CASE=F3` | **Done** (scripted; may need sudo to restart MD) |
| F4 | App container dies after healthy deploy | `CASE=F4` | **Done** (scripted; uses restart/recreate) |
| F5 | Backup interrupted | `CASE=F5` | **Scripted** (light) |
| F6 | Broken deploy after healthy | `scripts/clean_demo.sh` / `CASE=F6` | **Done** |

### How to run

```bash
export CAIRN_ROOTFS=../Mini-Docker/rootfs
export PYTHONPATH=../Mini-Docker
export MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR}/mini-docker/mini-docker.sock"

./scripts/stability_gate.sh              # unit + demos
./scripts/failure_matrix.sh              # F1,F2,F3,F4,F6
CASE=F2 ./scripts/failure_matrix.sh      # one case
```

### Postmortems

- [Failed deploy metadata](postmortems/2026-07-failed-deploy-metadata.md)
- [Mid-deploy crash recovery](postmortems/2026-07-mid-deploy-crash-recovery.md)

### Still optional (not blocking)

- FailForge campaign as a continuous Cairn CI gate (harness exists; live Mini-Docker not available on GitHub runners)
- Network delay injection between bridge containers
- Intentional SQLite metadata corruption drills

---

## 🌐 Phase 18: Optional multi-node / advanced mode

**Status: Deferred.** Do not build multi-node until single-node Phase 17 matrix stays green without babysitting.

> **Strict guideline**: multi-node primitives before single-node maturity is orchestration bloat.

### Eventually (not this phase)

- Remote deploy targets  
- Coordinator / replicated metadata  
- S3 backup targets  
- Placement rules  

---

## Proof gate (daily)

```bash
N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh         # unit + bash -n (no Mini-Docker)
N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh   # + clean_demo + mid-deploy
RUN_ROLLBACK_DEMO=1 N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh
CASE=F1,F2,F3,F4 ./scripts/failure_matrix.sh
./scripts/demo_reset.sh                             # optional cleanup before demos
```

GitHub Actions (`smoke.yml`) runs **unit + build + bash -n only**. Full gate is local Linux + Mini-Docker.
