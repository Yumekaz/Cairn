# Cairn Roadmap

Honest status for single-node reliability and closeout. **No multi-node work until Phase 18 is deliberately un-deferred.**

---

## Closeout A — single-node MLP (**code Done; live proofs need privileged Mini-Docker**)

**Scope:** One Linux host. Deploy stateful services on Mini-Docker, durable deploys via DuraFlow, recover from mid-deploy `cairnd` death, failed-deploy protection, backup/restore, event story, rollback safety. **Not** multi-node. **Not** FailForge-as-CI.

### Definition of done

| # | Criterion | Status |
| --- | --- | --- |
| 1 | Single-node reliability claim stated at top of README | **Done** |
| 2 | One local prove command: `./scripts/prove_mlp.sh` (also `make prove` / `make prove-mlp`) | **Done** (script landed; shared `scripts/lib/runtime.sh` bootstrap) |
| 3 | Phase 17 failure matrix F1–F6 scripted and re-runnable (incl. **F5 hard**) | **Done** (scripted) · **needs live verify** |
| 4 | Phase 19 ops: events, crash-loop visibility, rollback safety demos | **Done** (scripted) · **needs live verify** |
| 5 | CI honesty: GHA = unit + build + `bash -n` only; full proofs = local Linux + privileged Mini-Docker + DURAFLOW sibling | **Done** |
| 6 | Go version badges/docs match `go.mod` (1.26.x) | **Done** |
| 7 | Spine vs lab clear in STACK (lab not required for A); FailForge **OUT of A** | **Done** |
| 8 | Full green `./scripts/prove_mlp.sh` on a clean host | **needs live verify** (sudo/root Mini-Docker networking) |

**“Needs live verify”** means automation and unit coverage exist; a human still has to green the live demos on Linux with Mini-Docker started under sufficient privilege. Do not treat those rows as false **Done** until that run is green.

### Landed for A (code / scripts)

- `./scripts/prove_mlp.sh` — primary one-command Closeout A proof
- `scripts/lib/runtime.sh` — shared Mini-Docker / cairnd discovery + non-hanging sudo bootstrap
- **F5 hard:** on restart, incomplete backups are failed (`failIncompleteBackupsOnStartup`); matrix asserts no success→missing/corrupt archive, no lingering non-terminal rows, app still healthy
- FailForge continuous CI harness under `tests/failure/` is **optional lab only** — does **not** block A

### Out of Closeout A (optional / lab)

- FailForge as a **continuous Cairn CI** gate (harness under `tests/failure/` exists; live Mini-Docker not on GitHub runners). **OUT of A.**
- Network delay injection, intentional SQLite corruption drills
- Multi-node / Phase 18 items

### Prove command

```bash
./scripts/prove_mlp.sh
# or
make prove
```

Unit/syntax only (matches CI shape): `N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh` or `make smoke`.

See also [CLOSEOUT_A.md](CLOSEOUT_A.md).

---

## 📡 Phase 19: Single-node operational maturity (**done**; live demos **need live verify**)

**Goal**: Honest ops depth — complete event story, crash-loop visibility, rollback safety. **Not** multi-node (Phase 18).

| Track | Focus | Status |
| --- | --- | --- |
| A | Event taxonomy (MLP §17) | **Done** — full deploy/health/route/backup/restore story + `clean_demo` asserts |
| B | Continuous health / crash-loop | **Done** — reconcile emits `ServiceRestarted`; crash-loop stop thrash (5/10m); F4 asserts event |
| C | Rollback safety / state-touched | **Done** — `RollbackBlocked`/`RollbackForced` events + unit tests + `scripts/rollback_safety_demo.sh` |
| D | Demo hygiene | **Done** — portable FailForge bootstrap paths, `demo_reset.sh`, gate/docs list Phase 19 proofs |

**Proof scripts:** `prove_mlp.sh` (umbrella), `clean_demo.sh` (events), `failure_matrix.sh` (F4 restart events), `rollback_safety_demo.sh`, `demo_reset.sh`, `stability_gate.sh` (`RUN_ROLLBACK_DEMO=1` optional).  
**Unit always:** `N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh`. Live Mini-Docker demos need local Linux + **privileged** (sudo/root) networking.

Event reference: [events.md](events.md).

---

## 🔬 Phase 17: Failure testing (single-node)

**Goal**: Systematic failure testing to validate platform correctness — **not** multi-node chaos.

### Status

| ID | Failure | Automation | Status |
| --- | --- | --- | --- |
| F1 | SIGTERM `cairnd` mid-migration | `scripts/mid_deploy_crash_demo.sh` / `CASE=F1` | **Done** (scripted) · **needs live verify** |
| F2 | SIGKILL `cairnd` mid-migration | `KILL_SIGNAL=SIGKILL` / `CASE=F2` | **Done** (scripted) · **needs live verify** |
| F3 | Mini-Docker daemon death | `CASE=F3` | **Done** (scripted; may need sudo to restart MD) · **needs live verify** |
| F4 | App container dies after healthy deploy | `CASE=F4` | **Done** (scripted; uses restart/recreate) · **needs live verify** |
| F5 | Backup interrupted | `CASE=F5` | **Done (hard)** — restart fails incomplete backups; matrix asserts no success→missing/corrupt archive, terminal status only, app healthy · **needs live verify** |
| F6 | Broken deploy after healthy | `scripts/clean_demo.sh` / `CASE=F6` | **Done** (scripted) · **needs live verify** |

### How to run

```bash
export CAIRN_ROOTFS=../Mini-Docker/rootfs
export PYTHONPATH=../Mini-Docker
export MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR}/mini-docker/mini-docker.sock"

./scripts/prove_mlp.sh                   # full Closeout A (preferred)
./scripts/stability_gate.sh              # unit + demos
./scripts/failure_matrix.sh              # F1–F6
CASE=F2 ./scripts/failure_matrix.sh      # one case
CASE=F5 ./scripts/failure_matrix.sh      # backup interrupt (hard)
make gate                                # units + optional live demos
make matrix                              # failure matrix F1–F6
```

### Postmortems

- [Failed deploy metadata](postmortems/2026-07-failed-deploy-metadata.md)
- [Mid-deploy crash recovery](postmortems/2026-07-mid-deploy-crash-recovery.md)

### Still optional (not blocking Closeout A)

- FailForge continuous Cairn CI (harness exists; not on GHA runners) — **OUT of A**
- Network delay injection between bridge containers
- Intentional SQLite metadata corruption drills

---

## 🌐 Phase 18: Optional multi-node / advanced mode

**Status: Deferred.** Outside Closeout A. Do not build multi-node until single-node proofs stay green without babysitting.

> **Strict guideline**: multi-node primitives before single-node maturity is orchestration bloat.

### Eventually (not this phase)

- Remote deploy targets  
- Coordinator / replicated metadata  
- S3 backup targets  
- Placement rules  

---

## Proof gate (daily)

```bash
./scripts/prove_mlp.sh                              # Closeout A primary
make prove                                          # same via Makefile
N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh         # unit + bash -n (no Mini-Docker; CI-shaped)
N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh   # + clean_demo + mid-deploy
RUN_ROLLBACK_DEMO=1 N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh
CASE=F1,F2,F3,F4,F5,F6 ./scripts/failure_matrix.sh
./scripts/demo_reset.sh                             # optional cleanup before demos
```

GitHub Actions (`smoke.yml`) runs **unit + build + bash -n only**. Full gate is local Linux + **privileged** Mini-Docker + DURAFLOW sibling.
