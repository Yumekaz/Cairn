# Cairn PaaS

Cairn is a CLI-first, stateful-first, single-node self-hosted Platform-as-a-Service (PaaS). It runs on a local Linux host, using **Mini-Docker** as its default container runtime backend through a clean abstraction adapter.

Cairn is optimized for running stateful applications by treating persistent volumes and backup operations as core first-class citizens.

---

## 🛠️ Prerequisites & Setup

Cairn is built for a clean Linux system.

* **Go**: v1.26+ (Required to compile the Go binaries)
* **Python**: v3.14+ (Required to run the Mini-Docker daemon)
* **OverlayFS**: The host kernel must have the `overlay` filesystem module loaded.

### 1. Load OverlayFS Kernel Module
Ensure OverlayFS is active:
```bash
sudo modprobe overlay
```
To load it automatically on boot, add it to `/etc/modules`:
```bash
echo "overlay" | sudo tee -a /etc/modules
```

### 2. Start the Mini-Docker Daemon
Run the Mini-Docker daemon as root (in the background or a separate terminal), configuring socket permissions to allow non-root users (like Cairn) to connect:
```bash
sudo python3 -m mini_docker daemon --socket-mode 666
```

---

## 🚀 Building & Running Cairn

Compile both the `cairn` CLI client and the `cairnd` daemon:
```bash
# Clean and compile
make build
```
This outputs compiled Go binaries to the `bin/` directory.

### 1. Initialize Cairn Configuration
Initialize the default data directories and SQLite database under `~/.cairn/`:
```bash
./bin/cairn init
```

### 2. Start the Cairn Daemon
Start the Cairn control plane daemon in the background:
```bash
./bin/cairn daemon start
```

---

## 📖 Minimum Lovable Product (MLP) Demo Script

You can walk through the core proof of stateful deployments, live streaming logs, volume backups, failed-deploy protection, and restores using the commands below:

### 1. Deploy the Stateful App
Deploy the sample `counter-api` service configuration (which spins up a busybox web server routing to `localhost:8080` and mounts the `counter-data` volume):
```bash
./bin/cairn deploy examples/counter-api/cairn.yaml
```

Check the registry state and running container details:
```bash
# Show high-level status of daemon and services
./bin/cairn status

# Show detailed tabular listing of deployed containers
./bin/cairn ps

# Inspect detailed JSON-like registry metadata for the service
./bin/cairn inspect counter-api
```

Write data to the mounted persistent volume:
```bash
# Verify initial response (from examples/counter-api/www/index.html)
curl http://localhost:8080/index.html

# Write new data to the volume host path
echo "Backup Test State A" > ~/.cairn/volumes/counter-data/index.html

# Confirm the web server displays the updated state
curl http://localhost:8080/index.html
```

### 2. Verify Volume Data Across Restarts
Restart the container and confirm data is not lost:
```bash
./bin/cairn restart counter-api

# Confirm data persists
curl http://localhost:8080/index.html
```

### 3. Create a Volume Backup Snapshot
Create a manual backup archive of the volume directory (creates a compressed `.tar.gz` and records a SHA256 integrity checksum in SQLite):
```bash
./bin/cairn backup create counter-data

# List all completed backups for the volume
./bin/cairn backup list counter-data
```

### 4. Failed-Deploy Protection (Rollback Verification)
Try to deploy a broken config that fails its health check (the route points to a nonexistent path `/nonexistent.html`):
```bash
./bin/cairn deploy examples/counter-api/cairn_broken.yaml
```
**Expected Result**: The deployment command will fail. The Cairn daemon detects the candidate health checks are failing, stops and removes the broken candidate, and preserves the route pointing to the original healthy v1 container.
```bash
# Verify original healthy service continues serving traffic
curl http://localhost:8080/index.html
```

### 5. Inspect the Event Audit Timeline
Print out the event log of all operations:
```bash
./bin/cairn events
```

### 6. Restore Volume Data from Backup
Simulate a data corruption by overwriting the volume:
```bash
echo "Backup Test State B" > ~/.cairn/volumes/counter-data/index.html
curl http://localhost:8080/index.html

# Revert to State A from our backup
./bin/cairn restore counter-data <backup_id>

# Verify data is successfully reverted to State A
curl http://localhost:8080/index.html
```

---

## ⚠️ Operational Limitations

This is a **Minimum Lovable Product** proving the core technical mechanics of stateful orchestration on Linux. Note the following limitations:

1. **Single-Node Only**: Cairn handles orchestration solely on the local host. There is no multi-node cluster scheduling or virtual network overlay.
2. **OverlayFS Dependency**: It is strictly dependent on the host kernel loading the OverlayFS module for file isolation.
3. **No Automatic SSL/TLS**: Mapped ports are served over plain HTTP. Automated TLS/SSL routing (e.g. Let's Encrypt / ACME) is not included.
4. **Simple Host Port Mappings**: Reverse proxying is limited to basic port forwarding rule definitions in the YAML configs.
5. **Logs Storage**: Logs are outputted to flat files under `~/.cairn/logs/`. Advanced log rotation, indexing, and shipping (e.g. Elasticsearch/Loki) are not configured.
6. **Backup Engine**: Backups are manual tarball archives. Incremental or block-level volume snapshots are not supported in this version.
