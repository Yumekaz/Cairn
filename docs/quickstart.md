# Cairn Quickstart Guide

Single-node Linux. Three sibling repos. No multi-node.

---

## Preferred: bootstrap (one script)

From a Cairn-only clone (or after any path that gives you this repo):

```bash
git clone https://github.com/Yumekaz/Cairn.git
cd Cairn
./scripts/bootstrap_stack.sh --start-runtime   # needs sudo for Mini-Docker
cairn doctor
./scripts/prove_mlp.sh   # optional full MLP proof
```

From an empty parent directory (clones **Cairn**, **DURAFLOW**, and **Mini-Docker**):

```bash
# copy or run from a Cairn checkout:
./scripts/bootstrap_stack.sh --parent ~/src/cairn-stack --start-runtime
```

What the script does:

1. Resolves parent + Cairn/SERVER root (keeps a `SERVER` checkout name if that is the repo)
2. Clones missing siblings (HTTPS by default; `--ssh` for `git@`; remotes overridable via env)
3. Ensures `go.mod` replace is `../DURAFLOW` (not an absolute `/home/...` path)
4. Runs `./scripts/install.sh`
5. Prints `CAIRN_ROOTFS`, `PYTHONPATH`, `MINI_DOCKER_SOCKET`
6. `cairn init`
7. With `--start-runtime` / `START_RUNTIME=1`: starts Mini-Docker + `cairnd` via `scripts/lib/runtime.sh` (root / `sudo -n` / `SUDO_PASSWORD` only — never hangs on interactive sudo)
8. Runs `cairn doctor` when the runtime is available

### Privileges

| Need | Detail |
| --- | --- |
| OS | **Linux only** |
| Mini-Docker | **sudo/root** for host-managed networking (daemon start) |
| Non-interactive sudo | passwordless `sudo -n`, or `SUDO_PASSWORD` in the **local shell only** (never commit) |
| Toolchain | **Go 1.26+**, **Python 3.10+**, OverlayFS |

Without privilege, omit `--start-runtime`: install still succeeds; print the exact next commands for MD + cairnd + doctor.

Shorter overview: [GETTING_STARTED.md](GETTING_STARTED.md).

---

## Prerequisites

1. **Go**: **1.26.x** (matches `go.mod`; older toolchains will not build this module).
2. **Python**: 3.10+ (Mini-Docker daemon).
3. **OverlayFS**: kernel support.

```bash
sudo modprobe overlay
# optional persist: echo overlay | sudo tee -a /etc/modules
```

---

## Advanced: manual stranger path

Use this if you prefer step-by-step control (or bootstrap is unavailable).

### 1. Three siblings + install

```bash
mkdir -p ~/src && cd ~/src
git clone https://github.com/Yumekaz/Cairn.git
git clone https://github.com/Yumekaz/DURAFLOW.git
git clone https://github.com/Yumekaz/Mini-Docker.git
cd Cairn
./scripts/install.sh
export PATH="$HOME/.local/bin:$PATH"
```

`go.mod` uses `replace => ../DURAFLOW`. Installer refuses the build if that sibling is missing (or set `DURAFLOW_PATH`).

### 2. Runtime env + Mini-Docker

```bash
export CAIRN_ROOTFS="$(pwd)/../Mini-Docker/rootfs"
export PYTHONPATH="$(pwd)/../Mini-Docker${PYTHONPATH:+:$PYTHONPATH}"
export MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock"

sudo mkdir -p "$(dirname "$MINI_DOCKER_SOCKET")"
sudo env PYTHONPATH="$PYTHONPATH" python3 -m mini_docker daemon \
  --socket "$MINI_DOCKER_SOCKET" \
  --socket-mode 666
```

Start **one** Mini-Docker daemon (dual daemons on the same socket cause create failures). Root (or careful rootless) required for the daemon.

### 3. Init + doctor

```bash
cairn init
cairn doctor
```

### 4. Prove MLP (or short demo)

```bash
# Closeout A — full single-node proof
./scripts/prove_mlp.sh

# If prove_mlp.sh is not present yet, either:
./scripts/clean_demo.sh
# or interim compose:
N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh
./scripts/failure_matrix.sh
./scripts/rollback_safety_demo.sh
```

`clean_demo.sh` starts `cairnd` if needed, deploys `examples/counter-api`, restart/backup/broken-deploy/restore, and checks the event story.

**CI note:** GitHub Actions runs unit + build + `bash -n` only. Full proofs need this local path (Linux + Mini-Docker + DURAFLOW).

Private cold clone of the three spine repos: `./scripts/cold_clone_verify.sh`.

---

## Deploy counter-api yourself

```bash
export CAIRN_ROOTFS="$(pwd)/../Mini-Docker/rootfs"
cairn daemon start   # if not already running
cairn deploy examples/counter-api
cairn ps
cairn status
curl http://localhost:8080/index.html
```

---

## State persistence & recovery

1. Write volume data under `~/.cairn/volumes/counter-data/`, curl again.
2. `cairn restart counter-api` — data should persist.
3. Backup / corrupt / restore:

```bash
cairn backup create counter-data
cairn backup list counter-data
echo "Corrupted" > ~/.cairn/volumes/counter-data/index.html
cairn restore counter-data <backup_id>
```

---

## Rollback safety (stateful deploys)

When a deploy runs a **`migration:`** step successfully, Cairn marks `state_touched=true`. Rolling back **across** such deploys is blocked without `--force`.

```bash
cairn rollback counter-api --to <older_deploy_id>
# → HTTP 409 / ROLLBACK SAFETY WARNING; RollbackBlocked event

cairn rollback counter-api --to <older_deploy_id> --force
# → RollbackForced, then a new rollback deploy
```

Live script: `./scripts/rollback_safety_demo.sh` (and `FORCE=1` for the force path).

---

## Dashboard

```bash
cairn dashboard
# http://127.0.0.1:2476/dashboard/
```

---

## Lab (optional, not Closeout A)

FailForge / MiniDB / Coordination are **not** required for MLP closeout. See [STACK.md](STACK.md) tracks B/C and [roadmap.md](roadmap.md).
