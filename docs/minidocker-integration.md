# Mini-Docker Integration

Cairn uses **Mini-Docker** as its default, low-footprint container runtime backend. Mini-Docker avoids standard Docker overhead, running natively on cheap Linux hardware using standard kernel primitives.

---

## 🔌 Socket Integration

The Cairn daemon communicates with the Mini-Docker daemon via a local UNIX domain socket (defaulting to `/var/run/mini-docker.sock`).

The adapter located at `internal/runtime/minidocker/adapter.go` abstracts this socket communication:
- Sends `POST /containers/create` to provision a new namespace container.
- Sends `POST /containers/{id}/start` to spawn the root process.
- Sends `POST /containers/{id}/stop` to terminate execution.
- Sends `DELETE /containers/{id}` to clean up directory mounts and network links.
- Fetches `/containers/json` to list running containers for Cairn's service reconciliation.

---

## 📂 OverlayFS File Isolation

Mini-Docker uses **OverlayFS** for container rootfs isolation instead of standard heavy image layers.
- **Lower Directory**: The base rootfs directories (e.g., standard Alpine base file hierarchy).
- **Upper Directory**: Transient changes written by the container processes.
- **Merged Directory**: The union view presented to the running container namespace.
- **Host Volumes**: Volume mounts are bind-mounted directly from the host (`~/.cairn/volumes/<name>`) into the overlay merged filesystem, bypassing isolation limits.

---

## 🌐 Bridge Networking

Mini-Docker sets up an internal Linux network bridge to link containers and allow cross-service communication:

1. **Bridge Device (`mini-docker0`)**: Spawns a virtual network bridge interface on the host, assigning it IP `10.0.0.1/24`.
2. **Virtual Ethernet Pairs (`veth`)**: Creates twin virtual ethernet interfaces for each running container:
   - One interface stays attached to the host's `mini-docker0` bridge.
   - The other is moved inside the container's private Linux network namespace.
3. **Bridge IP Allocation**: Mini-Docker automatically dynamically assigns container IPs (e.g. `10.0.0.2`, `10.0.0.3`) from the bridge subnet.
4. **Port Mapping NAT Routing**:
   - For public-facing services (e.g., APIs), the host forwards target traffic from host ports to internal bridge container IPs using `iptables` DNAT rules.
   - Private databases or queue services remain isolated on the bridge network and do not open any host-facing ports.
5. **Cairn Environment Placeholders**:
   - When deploying dependent applications (like a web API that needs to query a Postgres DB), Cairn parses the service environment config and replaces placeholders (like `${DATABASE_HOST}`) with the dynamic bridge network IP of the database container before launching.
