#!/usr/bin/env bash
# FailForge node bootstrap script for Cairn.
# Starts either cairnd daemon (node-1) or mini-docker daemon (node-2).
#
# Portable: no hardcoded /home/... paths. Resolve Cairn repo from this script
# or CAIRN_ROOT; Mini-Docker via MINI_DOCKER_ROOT / sibling layout / PYTHONPATH.
set -euo pipefail

NODE_ID="${1:?missing node ID}"
PORT="${2:?missing port}"
DATA_DIR="${3:?missing data directory}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CAIRN_ROOT="${CAIRN_ROOT:-$(cd "${SCRIPT_DIR}/../.." && pwd)}"

# Resolve absolute path of DATA_DIR
DATA_DIR="$(mkdir -p "$DATA_DIR" && cd "$DATA_DIR" && pwd)"

resolve_cairnd() {
  if [[ -n "${CAIRND_BIN:-}" && -x "$CAIRND_BIN" ]]; then
    echo "$CAIRND_BIN"
    return
  fi
  if [[ -x "${CAIRN_ROOT}/bin/cairnd" ]]; then
    echo "${CAIRN_ROOT}/bin/cairnd"
    return
  fi
  if command -v cairnd >/dev/null 2>&1; then
    command -v cairnd
    return
  fi
  echo "cairnd not found (build ${CAIRN_ROOT}/bin/cairnd or set CAIRND_BIN)" >&2
  exit 1
}

resolve_minidocker_python() {
  if [[ -n "${MINI_DOCKER_PYTHON:-}" && -x "$MINI_DOCKER_PYTHON" ]]; then
    echo "$MINI_DOCKER_PYTHON"
    return
  fi
  local root="${MINI_DOCKER_ROOT:-}"
  if [[ -z "$root" ]]; then
    for cand in \
      "${CAIRN_ROOT}/../Mini-Docker" \
      "${HOME}/Desktop/Mini-Docker"; do
      if [[ -d "$cand" ]]; then
        root="$(cd "$cand" && pwd)"
        break
      fi
    done
  fi
  if [[ -n "$root" && -x "${root}/venv/bin/python3" ]]; then
    export PYTHONPATH="${root}${PYTHONPATH:+:$PYTHONPATH}"
    echo "${root}/venv/bin/python3"
    return
  fi
  if command -v python3 >/dev/null 2>&1; then
    echo "python3"
    return
  fi
  echo "Mini-Docker python not found (set MINI_DOCKER_ROOT or MINI_DOCKER_PYTHON)" >&2
  exit 1
}

if [ "$NODE_ID" = "node-1" ]; then
  NODE2_DIR="$(dirname "$DATA_DIR")/node-2"
  mkdir -p "$NODE2_DIR"

  CONFIG_PATH="${DATA_DIR}/cairnd-config.yaml"
  cat <<EOF >"$CONFIG_PATH"
socket_path: ${DATA_DIR}/cairnd.sock
database_path: ${DATA_DIR}/cairn.db
data_dir: ${DATA_DIR}
volume_dir: ${DATA_DIR}/volumes
backup_dir: ${DATA_DIR}/backups
mini_docker_socket: ${NODE2_DIR}/mini-docker.sock
dashboard_addr: 127.0.0.1:${PORT}
EOF

  CAIRND="$(resolve_cairnd)"
  echo "Starting cairnd daemon using config: $CONFIG_PATH ($CAIRND)"
  exec "$CAIRND" -config "$CONFIG_PATH"
else
  SOCKET_PATH="${DATA_DIR}/mini-docker.sock"
  MD_PY="$(resolve_minidocker_python)"
  echo "Starting mini-docker daemon on: $SOCKET_PATH ($MD_PY)"
  exec env PYTHONPATH="${PYTHONPATH:-}" "$MD_PY" -m mini_docker daemon --socket "$SOCKET_PATH"
fi
