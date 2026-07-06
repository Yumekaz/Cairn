#!/usr/bin/env bash
# FailForge node bootstrap script for Cairn.
# Starts either cairnd daemon (node-1) or mini-docker daemon (node-2).

set -euo pipefail

NODE_ID="${1:?missing node ID}"
PORT="${2:?missing port}"
DATA_DIR="${3:?missing data directory}"

# Resolve absolute path of DATA_DIR
DATA_DIR="$(mkdir -p "$DATA_DIR" && cd "$DATA_DIR" && pwd)"

if [ "$NODE_ID" = "node-1" ]; then
    # We are Node 1 (Cairn Daemon)
    NODE2_DIR="$(dirname "$DATA_DIR")/node-2"
    mkdir -p "$NODE2_DIR"
    
    # Generate daemon config dynamically
    CONFIG_PATH="${DATA_DIR}/cairnd-config.yaml"
    cat <<EOF > "$CONFIG_PATH"
socket_path: ${DATA_DIR}/cairnd.sock
database_path: ${DATA_DIR}/cairn.db
data_dir: ${DATA_DIR}
volume_dir: ${DATA_DIR}/volumes
backup_dir: ${DATA_DIR}/backups
mini_docker_socket: ${NODE2_DIR}/mini-docker.sock
dashboard_addr: 127.0.0.1:${PORT}
EOF

    echo "Starting cairnd daemon using config: $CONFIG_PATH"
    exec /home/yumekaz/Desktop/SERVER/bin/cairnd -config "$CONFIG_PATH"
else
    # We are Node 2 (Mini-Docker Daemon)
    SOCKET_PATH="${DATA_DIR}/mini-docker.sock"
    echo "Starting mini-docker daemon listening on: $SOCKET_PATH"
    exec /home/yumekaz/Desktop/Mini-Docker/venv/bin/python3 -m mini_docker daemon --socket "$SOCKET_PATH"
fi
