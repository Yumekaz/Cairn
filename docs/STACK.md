# Stack story — Cairn and friends

**Honest map:** spine vs lab. Multi-node, Rune/SETU, and MiniDB-as-platform-store are **deferred**.

---

## Spine vs lab

| Layer | Projects | Role for Closeout A |
| --- | --- | --- |
| **Spine** (required for MLP) | **Cairn → Mini-Docker → DuraFlow** | Single-node deploy, recoverability, backups, events |
| **Lab** (portfolio-adjacent) | **FailForge**, **MiniDB** (Mini-Redis-Cassandra), **Coordination-service** | Local chaos / educational targets; **not** required to close A |

FailForge continuous Cairn CI is **OUT of Closeout A** (optional lab). See [roadmap.md](roadmap.md).

```text
                    Clients / CLI
                          │
                          ▼
                   ┌─────────────┐
                   │    Cairn    │  (this repo: SERVER / Cairn)
                   │  control    │
                   │   plane     │
                   └──────┬──────┘
            runtime       │ workflows
            adapter       │
               │          │
               ▼          ▼
        ┌────────────┐  ┌──────────┐
        │ Mini-Docker│  │ DuraFlow │
        │  containers│  │ durable  │
        └────────────┘  │  steps   │
                        └──────────┘
              ▲ spine (MLP closeout)

        ── lab below (not required for A) ──

        FailForge (chaos lab, local only)
              │                    │
              ▼                    ▼
     Mini-Redis-Cassandra    Coordination-service
          (MiniDB KV)         (locks / meta)
```

**Read order:** Cairn reliability claim (README) → postmortems → optional FailForge MiniDB / Coordination seed 42.

---

## Repo map

| Repo | Role | GitHub |
| --- | --- | --- |
| **Cairn** (`SERVER`) | Single-node PaaS: deploy, proxy, volumes, backups, heal | [Yumekaz/Cairn](https://github.com/Yumekaz/Cairn) |
| **Mini-Docker** | Linux container runtime under Cairn | [Yumekaz/Mini-Docker](https://github.com/Yumekaz/Mini-Docker) |
| **DURAFLOW** | Durable workflow engine used by Cairn deploys | [Yumekaz/DURAFLOW](https://github.com/Yumekaz/DURAFLOW) |
| **FAILFORGE** | Seeded failure lab (local) | [Yumekaz/FAILFORGE](https://github.com/Yumekaz/FAILFORGE) |
| **Mini-Redis-Cassandra** | Educational RF-replicated KV; FailForge target | [Yumekaz/Mini-Redis-Cassandra](https://github.com/Yumekaz/Mini-Redis-Cassandra) |
| **Coordination-service** | Sessions, leases, locks; FailForge target | [Yumekaz/Coordination-service](https://github.com/Yumekaz/Coordination-service) |

Spine sibling layout (MLP / cold clone):

```text
parent/
  Cairn/                 # or SERVER checkout of Cairn
  DURAFLOW/
  Mini-Docker/
```

Lab siblings (optional demos only):

```text
  FAILFORGE/
  Mini-Redis-Cassandra/
  Coordination-service/
```

---

## What we proved (postmortems)

| Bug class | System | Postmortem |
| --- | --- | --- |
| Failed deploy left wrong `current_deploy_id` | Cairn | [2026-07-failed-deploy-metadata.md](postmortems/2026-07-failed-deploy-metadata.md) |
| Mid-deploy `cairnd` kill + heal / reconcile | Cairn | [2026-07-mid-deploy-crash-recovery.md](postmortems/2026-07-mid-deploy-crash-recovery.md) |
| FailForge seed 42 read-after-write on MiniDB | MiniDB + FailForge (lab) | [Mini-Redis-Cassandra postmortem](https://github.com/Yumekaz/Mini-Redis-Cassandra/blob/main/docs/postmortems/2026-07-failforge-seed42-raw.md) |

Spine bugs are the Closeout A proofs. MiniDB seed 42 is lab portfolio evidence, not a gate for A.

---

## Demo tracks

Assume **sibling checkouts**. Linux + Go **1.26.x** (`go.mod`) + Python 3.10+.

### Shared env (spine / Track A)

```bash
# From the Cairn repo root
export CAIRN_ROOTFS="$(pwd)/../Mini-Docker/rootfs"
export PYTHONPATH="$(pwd)/../Mini-Docker${PYTHONPATH:+:$PYTHONPATH}"
export MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock"
# Mini-Docker daemon must be running and rootfs populated
```

### Track A — Cairn reliability (Closeout A)

```bash
cd Cairn   # this repo

# One command for MLP closeout
./scripts/prove_mlp.sh

# Interim if prove_mlp.sh is not present yet:
N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh
./scripts/failure_matrix.sh
./scripts/rollback_safety_demo.sh
```

Also useful:

```bash
./scripts/clean_demo.sh                      # deploy / backup / broken-deploy / restore / events
./scripts/cold_clone_verify.sh               # private cold clone of three spine repos
./scripts/mid_deploy_crash_demo.sh
KILL_SIGNAL=SIGKILL ./scripts/mid_deploy_crash_demo.sh
./scripts/demo_reset.sh
N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh  # unit + bash -n (CI-shaped; no Mini-Docker)
```

GitHub Actions: **unit + build + bash -n only**. Full Track A needs local Mini-Docker + DURAFLOW.

### Track B — FailForge MiniDB (lab, not Closeout A)

```bash
cd FAILFORGE
go build -o bin/failforge ./cmd/failforge
./bin/failforge run failforge_minidb.yml --seed 42
./bin/failforge report runs/minidb-42
```

### Track C — FailForge Coordination (lab, not Closeout A)

```bash
cd FAILFORGE
go build -o bin/failforge ./cmd/failforge
./bin/failforge run failforge_coordination.yml --seed 42
./bin/failforge report runs/coordination-42
```

---

## What we deliberately defer

- Multi-node Cairn / remote hosts / platform Raft (Phase 18)  
- Rune / SETU product work  
- Dashboard redesign  
- MiniDB as Cairn production store  
- Continuous GitHub FailForge / Cairn live matrix (local lab only; **OUT of A**)  
- Chasing “seed 42 always 0 ERROR” on MiniDB unless explicitly prioritized  

---

## Related docs in this repo

- [Quickstart](quickstart.md)  
- [Roadmap](roadmap.md) (Closeout A)  
- [Architecture](architecture.md)  
- [Limitations](limitations.md)  
- [Mini-Docker integration](minidocker-integration.md)  
