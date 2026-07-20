# Closeout A — definition of done

Single-node MLP spine only: **Cairn + Mini-Docker + DuraFlow**. Not multi-node. **Not FailForge-as-CI** (lab harness under `tests/failure/` is optional and does not block A).

## Done means

1. Reliability claim is honest and single-node (README top).
2. One prove command greens end-to-end on a Linux host with siblings.
3. Failure matrix F1–F6 is re-runnable; **F5 is hard** (incomplete backups failed on restart; no success→missing/corrupt archive).
4. Events, crash-loop visibility, rollback safety demos exist.
5. CI stays unit + build + `bash -n`; full proofs stay local.
6. Go docs match `go.mod` (**1.26.x**).
7. STACK marks FailForge / MiniDB / Coordination as lab, not A.

Code/scripts for the above are in tree (`prove_mlp.sh`, `scripts/lib/runtime.sh`, hard F5 recovery). Rows that still require a privileged Mini-Docker run are marked **needs live verify** in [roadmap.md](roadmap.md) until a full prove is green.

## How to prove

```bash
# Siblings: ../DURAFLOW, ../Mini-Docker
export CAIRN_ROOTFS="$(pwd)/../Mini-Docker/rootfs"
export PYTHONPATH="$(pwd)/../Mini-Docker${PYTHONPATH:+:$PYTHONPATH}"
export MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock"

./scripts/prove_mlp.sh    # primary
# make prove              # same
```

CI-shaped only: `N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh` or `make smoke`.

## Known live requirement

Live demos need **Linux + Mini-Docker with sudo/root (or passwordless sudo / `SUDO_PASSWORD`)** for daemon start and networking. Scripts use `scripts/lib/runtime.sh` and refuse to hang on interactive sudo prompts. Without privilege, expect **needs live verify** — not a false green.

## Portability A (sibling layout)

No Desktop hard requirement; prove on one machine: [PORTABILITY_A.md](PORTABILITY_A.md) · `./scripts/prove_portability.sh`.

## Out of A

FailForge continuous Cairn CI, multi-node (Phase 18), product-B items (TLS termination, Docker Hub pulls, multi-host placement).

## Live verification (this machine)

`./scripts/prove_mlp.sh` completed **ALL GREEN** (units, clean_demo, mid-deploy SIGTERM, rollback safety, failure matrix F1–F6) after privileged Mini-Docker was available. Re-run after material changes.

**Security:** never put sudo passwords in git. Use env `SUDO_PASSWORD` only in the local shell (not committed). If a password was ever committed historically, rotate it.
