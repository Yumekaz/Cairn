#!/usr/bin/env bash
# Portable cold-start demo for Cairn (no hardcoded /home/<user>/Desktop paths).
# Steps: Mini-Docker → cairnd → deploy counter-api → restart → backup →
#        broken deploy (must fail) → restore → dashboard check.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRATCH="${CAIRN_DEMO_SCRATCH:-}"
LOG() { echo "[demo] $*"; }
die() { echo "[demo] ERROR: $*" >&2; exit 1; }

# --- discover tools ---
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"
command -v cairn >/dev/null || die "cairn binary not found (build: make build or go build -o bin/cairn ./cmd/cairn)"
command -v cairnd >/dev/null || die "cairnd binary not found"

# --- rootfs discovery ---
if [[ -z "${CAIRN_ROOTFS:-}" ]]; then
  if [[ -n "${MINI_DOCKER_ROOTFS:-}" ]]; then
    export CAIRN_ROOTFS="$MINI_DOCKER_ROOTFS"
  else
    for cand in \
      "${ROOT}/../Mini-Docker/rootfs" \
      "${PWD}/../Mini-Docker/rootfs" \
      "${HOME}/Desktop/Mini-Docker/rootfs" \
      "${HOME}/mini-docker/rootfs"; do
      if [[ -x "${cand}/bin/busybox" || -x "${cand}/bin/sh" ]]; then
        export CAIRN_ROOTFS="$(cd "$(dirname "$cand")" && pwd)/$(basename "$cand")"
        break
      fi
    done
  fi
fi
[[ -n "${CAIRN_ROOTFS:-}" && -d "$CAIRN_ROOTFS" ]] || die "Set CAIRN_ROOTFS to a Mini-Docker rootfs (bin/busybox required)"
LOG "CAIRN_ROOTFS=$CAIRN_ROOTFS"

# --- Mini-Docker python ---
MD_PY="${MINI_DOCKER_PYTHON:-}"
if [[ -z "$MD_PY" ]]; then
  for cand in \
    "${ROOT}/../Mini-Docker/venv/bin/python3" \
    "${HOME}/Desktop/Mini-Docker/venv/bin/python3" \
    "python3"; do
    if [[ -x "$cand" ]] || command -v "$cand" >/dev/null 2>&1; then
      MD_PY="$cand"
      break
    fi
  done
fi
export PYTHONPATH="${MINI_DOCKER_SRC:-${ROOT}/../Mini-Docker}:${PYTHONPATH:-}"

# --- socket path ---
if [[ -z "${MINI_DOCKER_SOCKET:-}" ]]; then
  if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
    MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR}/mini-docker/mini-docker.sock"
  else
    MINI_DOCKER_SOCKET="/run/user/$(id -u)/mini-docker/mini-docker.sock"
  fi
fi
export MINI_DOCKER_SOCKET
MD_SOCK_DIR="$(dirname "$MINI_DOCKER_SOCKET")"

SUDO=""
if [[ "${EUID}" -ne 0 ]]; then
  if command -v sudo >/dev/null; then
    SUDO="sudo"
  else
    die "root or sudo required to start Mini-Docker daemon"
  fi
fi

# --- start Mini-Docker (single daemon) ---
start_minidocker() {
  # Prefer an already-running healthy daemon (no root needed).
  if [[ -S "$MINI_DOCKER_SOCKET" ]]; then
    if MINI_DOCKER_SOCKET="$MINI_DOCKER_SOCKET" cairn doctor >/dev/null 2>&1; then
      LOG "Mini-Docker already healthy at $MINI_DOCKER_SOCKET"
      return 0
    fi
    # Socket present — try probe via python without doctor (cairnd may be down)
    if "$MD_PY" - <<PY 2>/dev/null
import socket,sys
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
s.settimeout(2)
s.connect("$MINI_DOCKER_SOCKET")
s.sendall(b"GET /containers/json HTTP/1.1\\r\\nHost: localhost\\r\\nConnection: close\\r\\n\\r\\n")
data=s.recv(256)
sys.exit(0 if data.startswith(b"HTTP") else 1)
PY
    then
      LOG "Mini-Docker socket responsive at $MINI_DOCKER_SOCKET"
      return 0
    fi
    LOG "Stale/unhealthy Mini-Docker socket — will restart (requires sudo)"
  fi

  LOG "Ensuring Mini-Docker socket dir: $MD_SOCK_DIR"
  if [[ ! -d "$MD_SOCK_DIR" ]]; then
    $SUDO mkdir -p "$MD_SOCK_DIR" || die "cannot create $MD_SOCK_DIR (need sudo)"
  fi
  if [[ -S "$MINI_DOCKER_SOCKET" ]]; then
    $SUDO rm -f "$MINI_DOCKER_SOCKET" || true
  fi
  LOG "Starting Mini-Docker daemon on $MINI_DOCKER_SOCKET"
  $SUDO env PYTHONPATH="$PYTHONPATH" "$MD_PY" -m mini_docker daemon \
    --socket "$MINI_DOCKER_SOCKET" \
    --socket-mode 666 \
    >/tmp/cairn-minidocker-demo.log 2>&1 &
  # wait for socket
  for i in $(seq 1 30); do
    if [[ -S "$MINI_DOCKER_SOCKET" ]]; then
      sleep 0.3
      break
    fi
    sleep 0.2
  done
  [[ -S "$MINI_DOCKER_SOCKET" ]] || die "Mini-Docker socket did not appear (see /tmp/cairn-minidocker-demo.log). Start it manually with sudo."
}

# --- start cairnd ---
start_cairnd() {
  cairn init >/dev/null 2>&1 || true
  # Point config at our Mini-Docker socket if config exists
  local cfg="${HOME}/.cairn/cairnd-config.yaml"
  if [[ -f "$cfg" ]]; then
    # ensure mini_docker_socket line
    if grep -q 'mini_docker_socket:' "$cfg"; then
      # portable sed
      sed -i.bak "s|mini_docker_socket:.*|mini_docker_socket: ${MINI_DOCKER_SOCKET}|" "$cfg" || true
    else
      echo "mini_docker_socket: ${MINI_DOCKER_SOCKET}" >>"$cfg"
    fi
  fi
  if ! cairn status >/dev/null 2>&1; then
    LOG "Starting cairnd"
    # stop stale
    cairn daemon stop >/dev/null 2>&1 || true
    rm -f "${HOME}/.cairn/cairnd.sock" "${HOME}/.cairn/cairnd.pid" 2>/dev/null || true
    nohup cairnd >/tmp/cairnd-demo.out 2>&1 &
    for i in $(seq 1 40); do
      if cairn status >/dev/null 2>&1; then
        break
      fi
      sleep 0.15
    done
  fi
  cairn status >/dev/null || die "cairnd did not become ready"
  LOG "cairnd ready"
}

# --- seed volume content for httpd ---
seed_volume() {
  local vol="${HOME}/.cairn/volumes/counter-data"
  mkdir -p "$vol"
  if [[ ! -f "$vol/index.html" ]]; then
    echo "OK" >"$vol/index.html"
  fi
  # copy example www if present
  if [[ -d "${ROOT}/examples/counter-api/www" ]]; then
    cp -a "${ROOT}/examples/counter-api/www/." "$vol/" 2>/dev/null || true
  fi
}

# --- main flow ---
start_minidocker
start_cairnd

LOG "Preflight (cairn doctor)"
cairn doctor

seed_volume

LOG "Deploy counter-api (directory path)"
cairn deploy "${ROOT}/examples/counter-api"

LOG "ps / status"
cairn ps
curl -sf -m 5 http://127.0.0.1:8080/index.html | head -c 200
echo

LOG "Mutate volume + restart"
echo "STATE_OK" >"${HOME}/.cairn/volumes/counter-data/index.html"
cairn restart counter-api
sleep 1
curl -sf -m 5 http://127.0.0.1:8080/index.html | grep -q STATE_OK
LOG "persistence after restart: OK"

LOG "Backup"
BACKUP_OUT="$(cairn backup create counter-data)"
echo "$BACKUP_OUT"
BACKUP_ID="$(echo "$BACKUP_OUT" | sed -n "s/.*Backup '\([^']*\)'.*/\1/p" | head -1)"
[[ -n "$BACKUP_ID" ]] || BACKUP_ID="$(cairn backup list counter-data | awk 'NR==2 {print $1}')"
[[ -n "$BACKUP_ID" ]] || die "could not parse backup id"
LOG "backup id=$BACKUP_ID"

SUCCESS_DEPLOY="$(cairn inspect counter-api | awk -F': *' '/^Current Deploy ID:/{print $2}' | head -1 | tr -d '[:space:]')"
[[ -n "$SUCCESS_DEPLOY" ]] || die "could not parse Current Deploy ID from cairn inspect (need rebuilt cairn CLI)"
LOG "success deploy id (pre-broken)=$SUCCESS_DEPLOY"

LOG "Broken deploy (must fail and keep healthy current)"
set +e
BROKEN_OUT="$(cairn deploy "${ROOT}/examples/counter-api/cairn_broken.yaml" 2>&1)"
BROKEN_RC=$?
set -e
echo "$BROKEN_OUT"
[[ "$BROKEN_RC" -ne 0 ]] || die "broken deploy should have failed"
# still serving
curl -sf -m 5 http://127.0.0.1:8080/index.html | grep -q STATE_OK

INSPECT="$(cairn inspect counter-api)"
echo "$INSPECT"
AFTER_DEPLOY="$(echo "$INSPECT" | awk -F': *' '/^Current Deploy ID:/{print $2}' | head -1 | tr -d '[:space:]')"
[[ -n "$AFTER_DEPLOY" ]] || die "post-broken inspect missing Current Deploy ID"
[[ "$AFTER_DEPLOY" == "$SUCCESS_DEPLOY" ]] || die "current_deploy_id changed after broken deploy: want $SUCCESS_DEPLOY got $AFTER_DEPLOY"
# Must not equal a failed deploy id from the broken attempt message if present
if echo "$BROKEN_OUT" | grep -qi 'failed'; then
  LOG "metadata assertion: Current Deploy ID stayed $SUCCESS_DEPLOY (not a failed candidate)"
fi
LOG "broken deploy rejected; traffic + current_deploy_id healthy"

LOG "Corrupt volume + restore"
echo "CORRUPTED" >"${HOME}/.cairn/volumes/counter-data/index.html"
cairn restore counter-data "$BACKUP_ID"
sleep 1
curl -sf -m 5 http://127.0.0.1:8080/index.html | grep -q STATE_OK
LOG "restore: OK"

LOG "Dashboard"
DASH_HTML="$(curl -sf -m 5 -L http://127.0.0.1:2476/dashboard/ | head -c 4000)"
echo "$DASH_HTML" | grep -qi 'Cairn' || die "dashboard missing Cairn title/content"
LOG "dashboard: OK"

LOG ""
LOG "=== CLEAN DEMO PASSED ==="
LOG "Dashboard: http://127.0.0.1:2476/dashboard/"
LOG "Service:   http://127.0.0.1:8080/"
exit 0
