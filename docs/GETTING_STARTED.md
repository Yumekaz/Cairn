# Getting started with Cairn

One-page path for a stranger on a single Linux host. No multi-node. No FailForge. No published DuraFlow module.

---

## Prerequisites

| Item | Requirement |
| --- | --- |
| OS | **Linux** (namespaces, OverlayFS, bridge networking) |
| Go | **1.26+** (see `go.mod`) |
| Python | **3.10+** (Mini-Docker daemon) |
| Kernel | OverlayFS (`sudo modprobe overlay`) |
| Privilege | **sudo/root** to start Mini-Docker for host-managed networking |

**Sudo options (never hang the bootstrap on a TTY password prompt):**

* run as root, or
* passwordless `sudo -n`, or
* `SUDO_PASSWORD` set in the **local shell only** (never write passwords into files or git)

---

## Bootstrap (recommended)

```bash
git clone https://github.com/Yumekaz/Cairn.git
cd Cairn
./scripts/bootstrap_stack.sh --start-runtime
cairn doctor
```

What you get:

* Sibling checkouts: **Cairn** (or keep a **SERVER** folder name), **DURAFLOW**, **Mini-Docker**
* `go.mod` replace `../DURAFLOW`
* Built/installed `cairn` + `cairnd`
* Optional Mini-Docker + `cairnd` when privilege is available
* `cairn doctor` when the runtime is up

Without sudo yet:

```bash
./scripts/bootstrap_stack.sh          # install only
# then start Mini-Docker + cairnd using the commands the script prints
cairn doctor
```

From an empty parent:

```bash
./scripts/bootstrap_stack.sh --parent ~/src/cairn-stack --start-runtime
```

Flags: `--https` (default), `--ssh`, `--parent DIR`, `--start-runtime`.  
Env: `CAIRN_REMOTE`, `DURAFLOW_REMOTE`, `MINI_DOCKER_REMOTE`, `START_RUNTIME=1`, `SUDO_PASSWORD`.

---

## Doctor

```bash
export CAIRN_ROOTFS="$(pwd)/../Mini-Docker/rootfs"
export PYTHONPATH="$(pwd)/../Mini-Docker${PYTHONPATH:+:$PYTHONPATH}"
export MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock"
export PATH="${HOME}/.local/bin:${PATH}"

cairn doctor
```

Green doctor means Mini-Docker socket + `cairnd` are healthy enough for deploys.

---

## First deploy

```bash
cairn deploy examples/counter-api
cairn ps
curl http://localhost:8080/index.html
```

---

## Prove the MLP (optional)

```bash
./scripts/prove_mlp.sh
```

Needs the same privileges as live Mini-Docker. See [CLOSEOUT_A.md](CLOSEOUT_A.md) and the reliability section in the [README](../README.md).

---

## More detail

* [quickstart.md](quickstart.md) — bootstrap + advanced manual steps, demos, rollback
* [STACK.md](STACK.md) — spine vs lab projects
* [PORTABILITY_A.md](PORTABILITY_A.md) — sibling layout / no Desktop hard requirement
