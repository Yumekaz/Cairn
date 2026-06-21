# Sane Limitations

Cairn is designed to be a lightweight, understandable, and robust PaaS for stateful backends on single Linux servers. It does not try to solve all enterprise orchestration problems.

Here is an honest listing of Cairn's operational boundaries and limits.

---

## 🚫 What Cairn Cannot Do

### 1. Single-Node Only
Cairn does not manage multi-host clusters, virtual overlay networks, or distributed container orchestration. If your workload spans multiple servers, you will need a tool like Nomad or Kubernetes.

### 2. Linux Kernel Dependency
Cairn and its default Mini-Docker runtime rely strictly on standard Linux kernel primitives:
- **OverlayFS** module for filesystem layers.
- **Network Namespaces** for container network isolation.
- **iptables** for host port NAT forwarding.
Because of this, Cairn cannot run natively on macOS or Windows without being inside a Linux virtual machine.

### 3. Privilege Model & Root Access
Mini-Docker requires root privileges (`sudo`) to create namespaces, attach bridge devices, adjust host routing, and perform mount operations. While the `cairnd` control daemon can run as a normal user, it needs read/write permissions on the Mini-Docker UNIX socket (`/var/run/mini-docker.sock`).

### 4. Plain HTTP Proxy
Cairn's built-in reverse routing is limited to basic port forwarding.
- There is **no built-in Let's Encrypt / ACME automatic TLS certificate management**.
- If your application requires SSL/TLS, you must run Nginx, Caddy, or a Cloudflare Tunnel on the host to terminate SSL and proxy traffic to Cairn.

### 5. Single Replica Architecture
For stateful databases and volume mounts, scaling replicas automatically causes volume write conflicts. Cairn does not implement distributed locks or replica scaling; all services run exactly as single instances.

### 6. SQLite Concurrency Scale
Cairn uses a local SQLite database (`cairn.db`) to record metadata and workflows. While SQLite is fast, lightweight, and single-file, it is not optimized for thousands of simultaneous high-frequency concurrent writes. For standard developer/homelab PaaS use cases, it is extremely robust.
