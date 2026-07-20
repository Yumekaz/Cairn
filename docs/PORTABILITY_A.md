# Portability A — definition of done

Cairn must work from a **sibling layout** on any Linux host without requiring
`/home/<user>/Desktop` (or any fixed home path).

## Sibling layout (required)

```text
parent/
  Cairn/          # this repo (or SERVER checkout)
  DURAFLOW/       # go.mod replace => ../DURAFLOW
  Mini-Docker/    # rootfs + Python package
```

## Discovery order (`scripts/lib/runtime.sh`)

**rootfs / Mini-Docker source / venv:**

1. Explicit env: `CAIRN_ROOTFS`, `MINI_DOCKER_ROOTFS`, `MINI_DOCKER_SRC`, `MINI_DOCKER_PYTHON`
2. Sibling `../Mini-Docker` relative to the Cairn repo root (script location)
3. Optional `MINI_DOCKER_PATH` (non-sibling layouts)
4. **Legacy only (last resort):** `$HOME/Desktop/Mini-Docker` — logs `legacy fallback`; never required

Same sibling-first rule applies to FailForge bootstrap (`tests/failure/failforge_bootstrap.sh`).

## Definition of done

| # | Check | How |
| --- | --- | --- |
| 1 | No Desktop hard requirement | runtime + failforge resolve sibling/`MINI_DOCKER_PATH` first |
| 2 | Clean tree under `/tmp` (or `PORT_DIR`) works | `./scripts/prove_portability.sh` |
| 3 | Build via `install.sh` in that tree | included in prove |
| 4 | Unit/syntax always | `N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh` |
| 5 | Live demos when privileged | `PROVE_QUICK=1 ./scripts/prove_mlp.sh` if root / `sudo -n` / `SUDO_PASSWORD` |
| 6 | Clear LIVE_SKIPPED if no privilege | prove script prints it; still exits green on unit path |
| 7 | No `/home/yumekaz` in **scripts** of the copied tree | hardcode guard in prove (optional skip: `SKIP_HARDCODE_GREP=1`) |

## Prove on the same machine (no second PC)

```bash
# From a Cairn checkout that already has sibling DURAFLOW + Mini-Docker:
./scripts/prove_portability.sh

# Custom location / keep tree for inspection:
PORT_DIR=/tmp/cairn-port KEEP_PORT_DIR=1 ./scripts/prove_portability.sh

# Live Mini-Docker path (non-interactive):
SUDO_PASSWORD='…' ./scripts/prove_portability.sh   # never commit passwords
# or passwordless: sudo -n true && ./scripts/prove_portability.sh
```

Creates `${PORT_DIR:-/tmp/cairn-portability-$$}` with **Cairn + DURAFLOW + Mini-Docker**
siblings (local `git clone --local` / rsync / `cp -a`), **never Desktop paths**, builds,
runs stability gate, and optionally live prove.

Related: [CLOSEOUT_A.md](CLOSEOUT_A.md) (MLP spine), [quickstart.md](quickstart.md).

## Live verification (this machine, no second PC)

`./scripts/prove_portability.sh` with `PORT_DIR=/tmp/cairn-portability-prove` completed **ALL GREEN**:

- Working-tree rsync to neutral `/tmp` sibling layout (not Desktop)
- Hardcode guard: no `/home/yumekaz` in scripts/internal/cmd/tests
- `install.sh` + unit stability gate
- Live `PROVE_QUICK=1 ./scripts/prove_mlp.sh` under the cold tree

Re-run after material changes. Does **not** replace a real second machine, but does prove “stranger path on same disk.”
