# Cairn Quickstart Guide

This guide gets you up and running with Cairn PaaS and Mini-Docker on a local Linux host.

---

## 🛠️ Prerequisites

Before installing, make sure your Linux system meets these requirements:

1. **Go**: Version 1.22+ (to compile binaries).
2. **Python**: Version 3.10+ (to run the Mini-Docker daemon).
3. **OverlayFS**: The Linux kernel must support OverlayFS.

### Load OverlayFS Module
Ensure OverlayFS is active:
```bash
sudo modprobe overlay
```
To load it automatically on system boot, add it to `/etc/modules`:
```bash
echo "overlay" | sudo tee -a /etc/modules
```

---

## ⚙️ Step 1: Sibling layout + Install Cairn

Cairn depends on a **local DuraFlow checkout** (`go.mod` replace → `../DURAFLOW`). Clone three repos side by side:

```bash
mkdir -p ~/src && cd ~/src
git clone git@github.com:Yumekaz/Cairn.git
git clone git@github.com:Yumekaz/DURAFLOW.git
git clone git@github.com:Yumekaz/Mini-Docker.git
cd Cairn
./scripts/install.sh
```

The installer will:
- Refuse to build if `../DURAFLOW` is missing (or honor `DURAFLOW_PATH`)
- Compile `cairn` / `cairnd` into `./bin/` and install under `~/.local/bin/`
- Create `~/.cairn/` layout

```bash
export PATH="$HOME/.local/bin:$PATH"
```

**Private verification without other people:** `./scripts/cold_clone_verify.sh` re-clones into `~/Desktop/cold-clone-check` and runs the full demo.

---

## 🐋 Step 2: Start Mini-Docker Daemon

Cairn uses Mini-Docker as its containerization runtime backend. Start **one** Mini-Docker daemon as root (dual daemons on the same socket cause EOF create failures):

```bash
# Point this at your Mini-Docker checkout rootfs for examples
export CAIRN_ROOTFS=/path/to/Mini-Docker/rootfs
export MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock"

sudo mkdir -p "$(dirname "$MINI_DOCKER_SOCKET")"
sudo python3 -m mini_docker daemon \
  --socket "$MINI_DOCKER_SOCKET" \
  --socket-mode 666
```

---

## 🚀 Step 3: Start the Cairn Daemon

1. Initialize the SQLite metadata store and default configuration:
   ```bash
   cairn init
   ```
2. Check readiness:
   ```bash
   cairn doctor
   ```
3. Start the control plane daemon:
   ```bash
   cairnd
   # or
   cairn daemon start
   ```

### One-shot portable demo

From a cold machine (after Mini-Docker rootfs is available):

```bash
export CAIRN_ROOTFS=/path/to/Mini-Docker/rootfs
./scripts/clean_demo.sh
# or: make demo
```

This starts Mini-Docker if needed, starts `cairnd`, deploys `examples/counter-api` **by directory**, restarts, backs up, rejects a broken deploy, restores, and checks the dashboard.

---

## 📦 Step 4: Deploy your First App

Deploy the bundled stateful `counter-api` application (file **or** directory containing `cairn.yaml`):
```bash
export CAIRN_ROOTFS=/path/to/Mini-Docker/rootfs
cairn deploy examples/counter-api
# equivalent:
cairn deploy examples/counter-api/cairn.yaml
```

Check the status of your services:
```bash
# General daemon status (uptime, active count, disk free/warnings)
cairn status

# Tabular process list of running containers
cairn ps

# View the full JSON metadata registered for your service
cairn inspect counter-api
```

Interact with the running API (serving on `http://localhost:8080/`):
```bash
curl http://localhost:8080/index.html
```

---

## 💾 Step 5: Test State Persistence & Recovery

1. **State Persistence**: Write something to the persistent volume mounted under `~/.cairn/volumes/counter-data/`:
   ```bash
   echo "Stateful Data A" > ~/.cairn/volumes/counter-data/index.html
   curl http://localhost:8080/index.html
   ```
2. **Container Restarts**: Restart the service. The data should persist:
   ```bash
   cairn restart counter-api
   curl http://localhost:8080/index.html
   ```
3. **Backup & Restore**: Create a compressed volume snapshot:
   ```bash
   cairn backup create counter-data
   cairn backup list counter-data
   ```
   Now corrupt the data:
   ```bash
   echo "Corrupted State B" > ~/.cairn/volumes/counter-data/index.html
   curl http://localhost:8080/index.html
   ```
   Restore from the backup:
   ```bash
   cairn restore counter-data <backup_id>
   curl http://localhost:8080/index.html
   ```

---

## 📊 Step 6: View Dashboard

You can access the embedded web dashboard:
```bash
cairn dashboard
```
This opens your system browser to `http://127.0.0.1:2476/dashboard/` where you can view daemon health, start/stop services, look at logs, manage backups, and view chronological audit events.
