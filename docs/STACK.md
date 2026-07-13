# Stack story — Cairn and friends

**Portfolio claim:** I built a single-node PaaS stack (**Cairn → Mini-Docker → DuraFlow**) and a local failure lab (**FailForge**) that found real bugs in **MiniDB** (and Cairn recoverability) — and you can re-run the proofs from a cold clone.

This is the map of the six Desktop infrastructure projects, how they fit, what is proved, and how to demo it. Multi-node, Rune/SETU, and MiniDB-as-platform-store are **intentionally deferred**.

---

## Architecture

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

        FailForge (chaos lab, local only)
              │                    │
              ▼                    ▼
     Mini-Redis-Cassandra    Coordination-service
          (MiniDB KV)         (locks / meta)
```

**Read order for humans:** Cairn reliability claim → postmortems → FailForge MiniDB postmortem → optional Coordination seed 42.

---

## Repo map

| Repo | Role | GitHub |
| --- | --- | --- |
| **Cairn** (`SERVER`) | Single-node PaaS: deploy, proxy, volumes, backups, heal | [Yumekaz/Cairn](https://github.com/Yumekaz/Cairn) |
| **Mini-Docker** | Linux container runtime (namespaces/cgroups) under Cairn | [Yumekaz/Mini-Docker](https://github.com/Yumekaz/Mini-Docker) |
| **DURAFLOW** | Durable workflow engine used by Cairn deploys | [Yumekaz/DURAFLOW](https://github.com/Yumekaz/DURAFLOW) |
| **FAILFORGE** | Seeded failure lab: process/network faults + checkers + minimize | [Yumekaz/FAILFORGE](https://github.com/Yumekaz/FAILFORGE) |
| **Mini-Redis-Cassandra** | Educational RF-replicated KV; FailForge MiniDB target | [Yumekaz/Mini-Redis-Cassandra](https://github.com/Yumekaz/Mini-Redis-Cassandra) |
| **Coordination-service** | Sessions, leases, locks, leader/follower; FailForge target | [Yumekaz/Coordination-service](https://github.com/Yumekaz/Coordination-service) |

Sibling layout (cold clone and local Desktop):

```text
parent/
  Cairn/                 # or SERVER checkout of Cairn
  DURAFLOW/
  Mini-Docker/
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
| FailForge seed 42 read-after-write on MiniDB | MiniDB + FailForge | [Mini-Redis-Cassandra postmortem](https://github.com/Yumekaz/Mini-Redis-Cassandra/blob/main/docs/postmortems/2026-07-failforge-seed42-raw.md) |

These are the “we found and fixed real bugs” proofs — not green CI alone.

---

## Demo tracks (cold-clone friendly)

Assume **sibling checkouts** as above. Use Linux + Go 1.22+ + Python 3.10+.

### Shared env (Cairn tracks)

```bash
# From the Cairn repo root
export CAIRN_ROOTFS="$(pwd)/../Mini-Docker/rootfs"
export PYTHONPATH="$(pwd)/../Mini-Docker${PYTHONPATH:+:$PYTHONPATH}"
export MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock"
# Mini-Docker daemon must be running and rootfs populated
```

### Track A — Cairn reliability

```bash
cd Cairn   # this repo

# Fast local gate: unit + crash recovery + clean demo (skip full cold clone)
N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh

# Private cold clone (wipe → clone three repos → build → clean_demo)
./scripts/cold_clone_verify.sh

# Mid-deploy kill: SIGTERM or SIGKILL cairnd during slow migration
./scripts/mid_deploy_crash_demo.sh
KILL_SIGNAL=SIGKILL ./scripts/mid_deploy_crash_demo.sh

# Failure matrix F1–F4, F6
./scripts/failure_matrix.sh
```

Also: `./scripts/clean_demo.sh` (deploy / restart / backup / broken-deploy / restore / dashboard).

### Track B — FailForge MiniDB (seed 42)

```bash
cd FAILFORGE
go build -o bin/failforge ./cmd/failforge

# Optional if MiniDB is not at ../Mini-Redis-Cassandra
# export MINIDB_ROOT=/path/to/Mini-Redis-Cassandra

./bin/failforge run failforge_minidb.yml --seed 42
./bin/failforge report runs/minidb-42
# If FAILED: ./bin/failforge minimize runs/minidb-42
```

Expect dramatic improvement after the QUORUM/RAW fixes; intermittent residual ERROR under restarts is documented in the MiniDB postmortem.

### Track C — FailForge Coordination (seed 42)

```bash
cd FAILFORGE
go build -o bin/failforge ./cmd/failforge

# Prefer portable sibling path (see failforge_coordination.yml).
# If the yml still embeds an absolute path, set:
#   export COORD_ROOT="$(pwd)/../Coordination-service"
# and adjust the command line until portableize lands (Phase 3).

./bin/failforge run failforge_coordination.yml --seed 42
./bin/failforge report runs/coordination-42
```

Checkers: `lock_exclusivity`, `no_two_leaders`.

---

## What we deliberately defer

- Multi-node Cairn / remote hosts / platform Raft  
- Rune / SETU product work  
- Dashboard redesign  
- MiniDB as Cairn production store  
- Continuous GitHub FailForge matrix (local proof first)  
- Chasing “seed 42 always 0 ERROR” on MiniDB unless explicitly prioritized  

---

## Related docs in this repo

- [Quickstart](quickstart.md)  
- [Architecture](architecture.md)  
- [Roadmap](roadmap.md)  
- [Limitations](limitations.md)  
- [Mini-Docker integration](minidocker-integration.md)  
