#!/usr/bin/env bash
# Shared Mini-Docker + cairnd bootstrap for proof/demo scripts.
# Source from scripts that set ROOT to the Cairn repo root:
#   # shellcheck source=lib/runtime.sh
#   source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/runtime.sh"
#   cairn_runtime_discover
#   ensure_minidocker
#   ensure_cairnd
#   wait_doctor   # optional
#
# Env (inputs):
#   CAIRN_ROOTFS / MINI_DOCKER_ROOTFS — Mini-Docker rootfs (bin/busybox)
#   MINI_DOCKER_SOCKET — override socket path
#   MINI_DOCKER_SRC / MINI_DOCKER_PYTHON — Mini-Docker package / interpreter
#   SUDO_PASSWORD — optional non-interactive sudo (same as failure_matrix)
#   CAIRN_RUNTIME_LOG — optional log file append for ensure_* noise
#
# Does not hang on interactive sudo password prompts: uses root, SUDO_PASSWORD,
# or sudo -n only. Missing privileges → clear die.

# Guard against double-source overwriting helpers mid-run is fine; idempotent.
: "${CAIRN_RUNTIME_SOURCED:=1}"

_cairn_runtime_log() {
  local msg="[runtime] $*"
  if declare -F LOG >/dev/null 2>&1; then
    LOG "$*"
  elif declare -F log >/dev/null 2>&1; then
    log "$*"
  else
    echo "$msg"
  fi
  if [[ -n "${CAIRN_RUNTIME_LOG:-}" ]]; then
    echo "$msg" >>"$CAIRN_RUNTIME_LOG" 2>/dev/null || true
  fi
}

_cairn_runtime_die() {
  if declare -F die >/dev/null 2>&1; then
    die "$*"
  else
    echo "[runtime] ERROR: $*" >&2
    exit 1
  fi
}

# --- sudo: never hang waiting for a TTY password ---
# Sets CAIRN_SUDO_MODE to: root | password | noninteractive
_cairn_sudo_setup() {
  if [[ "${EUID}" -eq 0 ]]; then
    CAIRN_SUDO_MODE="root"
    return 0
  fi
  if [[ -n "${SUDO_PASSWORD:-}" ]]; then
    CAIRN_SUDO_MODE="password"
    return 0
  fi
  if ! command -v sudo >/dev/null 2>&1; then
    _cairn_runtime_die "root or sudo required to start Mini-Docker daemon (sudo not found)"
  fi
  # Probe passwordless sudo without hanging (sudo -n).
  if sudo -n true >/dev/null 2>&1; then
    CAIRN_SUDO_MODE="noninteractive"
    return 0
  fi
  _cairn_runtime_die \
    "cannot start Mini-Docker: need root, passwordless sudo (-n), or SUDO_PASSWORD (non-interactive). Refusing to hang on sudo password prompt."
}

# Run a command with the configured privilege model.
# Usage: _cairn_sudo_run command args...
_cairn_sudo_run() {
  case "${CAIRN_SUDO_MODE:-}" in
    root)
      "$@"
      ;;
    password)
      # shellcheck disable=SC2024
      printf '%s\n' "$SUDO_PASSWORD" | sudo -S -p '' "$@"
      ;;
    noninteractive)
      sudo -n "$@"
      ;;
    *)
      _cairn_sudo_setup
      _cairn_sudo_run "$@"
      ;;
  esac
}

# Resolve Mini-Docker package root (sibling-first, no Desktop hard requirement).
# Discovery order:
#   1. MINI_DOCKER_SRC (explicit)
#   2. sibling ../Mini-Docker relative to repo ROOT
#   3. MINI_DOCKER_PATH (optional override for non-sibling layouts)
#   4. legacy fallback: $HOME/Desktop/Mini-Docker (logs "legacy fallback")
# Never requires /home/<user>/Desktop.
_cairn_resolve_minidocker_src() {
  if [[ -n "${MINI_DOCKER_SRC:-}" && -d "${MINI_DOCKER_SRC}" ]]; then
    (cd "${MINI_DOCKER_SRC}" && pwd)
    return 0
  fi
  if [[ -d "${ROOT}/../Mini-Docker" ]]; then
    (cd "${ROOT}/../Mini-Docker" && pwd)
    return 0
  fi
  if [[ -n "${MINI_DOCKER_PATH:-}" && -d "${MINI_DOCKER_PATH}" ]]; then
    (cd "${MINI_DOCKER_PATH}" && pwd)
    return 0
  fi
  # Legacy last-resort only (not required; never hardcodes /home/<user>/Desktop)
  # Log to stderr so command-substitution callers only capture the path.
  if [[ -n "${HOME:-}" && -d "${HOME}/Desktop/Mini-Docker" ]]; then
    echo "[runtime] legacy fallback: using ${HOME}/Desktop/Mini-Docker (prefer sibling ../Mini-Docker or MINI_DOCKER_PATH)" >&2
    (cd "${HOME}/Desktop/Mini-Docker" && pwd)
    return 0
  fi
  # Best-effort default path string even if missing (callers check -d)
  echo "${ROOT}/../Mini-Docker"
  return 0
}

# True if path looks like a usable Mini-Docker rootfs (busybox or sh).
_cairn_rootfs_ok() {
  local d="$1"
  [[ -n "$d" && -d "$d" && ( -x "${d}/bin/busybox" || -x "${d}/bin/sh" ) ]]
}

# Discover CAIRN_ROOTFS, MINI_DOCKER_SOCKET, PYTHONPATH, MD_PY.
# Requires ROOT (Cairn repo root). Safe to call multiple times.
#
# rootfs order:
#   1. CAIRN_ROOTFS env
#   2. MINI_DOCKER_ROOTFS env
#   3. sibling ../Mini-Docker/rootfs (relative to repo ROOT)
#   4. MINI_DOCKER_PATH/rootfs (optional)
#   5. legacy Desktop / $HOME/mini-docker (logs "legacy fallback")
cairn_runtime_discover() {
  if [[ -z "${ROOT:-}" ]]; then
    # Best-effort: this file lives at <repo>/scripts/lib/runtime.sh
    ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  fi

  # --- Mini-Docker package root (sibling first) ---
  local md_src
  md_src="$(_cairn_resolve_minidocker_src)"
  export MINI_DOCKER_SRC="$md_src"

  # --- rootfs ---
  if [[ -z "${CAIRN_ROOTFS:-}" ]]; then
    if [[ -n "${MINI_DOCKER_ROOTFS:-}" ]]; then
      export CAIRN_ROOTFS="$MINI_DOCKER_ROOTFS"
    else
      local cand resolved=""
      # Prefer sibling of ROOT, then MINI_DOCKER_PATH, then PWD sibling
      for cand in \
        "${ROOT}/../Mini-Docker/rootfs" \
        ${MINI_DOCKER_PATH:+"${MINI_DOCKER_PATH}/rootfs"} \
        "${PWD}/../Mini-Docker/rootfs"; do
        if _cairn_rootfs_ok "$cand"; then
          resolved="$(cd "$(dirname "$cand")" && pwd)/$(basename "$cand")"
          break
        fi
      done
      # Legacy last-resort (optional; never required)
      if [[ -z "$resolved" ]]; then
        for cand in \
          ${HOME:+"${HOME}/Desktop/Mini-Docker/rootfs"} \
          ${HOME:+"${HOME}/mini-docker/rootfs"}; do
          if _cairn_rootfs_ok "$cand"; then
            _cairn_runtime_log "legacy fallback: rootfs at $cand (prefer sibling ../Mini-Docker or CAIRN_ROOTFS)"
            resolved="$(cd "$(dirname "$cand")" && pwd)/$(basename "$cand")"
            break
          fi
        done
      fi
      if [[ -n "$resolved" ]]; then
        export CAIRN_ROOTFS="$resolved"
      fi
    fi
  fi

  # --- Mini-Docker python (venv sibling first) ---
  if [[ -z "${MD_PY:-}" ]]; then
    MD_PY="${MINI_DOCKER_PYTHON:-}"
  fi
  if [[ -z "$MD_PY" ]]; then
    local cand
    for cand in \
      "${md_src}/venv/bin/python3" \
      "${ROOT}/../Mini-Docker/venv/bin/python3" \
      ${MINI_DOCKER_PATH:+"${MINI_DOCKER_PATH}/venv/bin/python3"}; do
      if [[ -n "$cand" && -x "$cand" ]]; then
        MD_PY="$cand"
        break
      fi
    done
  fi
  if [[ -z "$MD_PY" ]]; then
    # System python before any Desktop path (Portability A — no machine coupling)
    if command -v python3 >/dev/null 2>&1; then
      MD_PY="$(command -v python3)"
    elif [[ -n "${HOME:-}" && -x "${HOME}/Desktop/Mini-Docker/venv/bin/python3" ]]; then
      _cairn_runtime_log "legacy fallback: Desktop Mini-Docker venv (prefer sibling venv or MINI_DOCKER_PYTHON)"
      MD_PY="${HOME}/Desktop/Mini-Docker/venv/bin/python3"
    else
      MD_PY="python3"
    fi
  fi
  export MD_PY

  # --- PYTHONPATH (Mini-Docker package root; sibling already preferred) ---
  if [[ -n "${PYTHONPATH:-}" ]]; then
    case ":${PYTHONPATH}:" in
      *":${md_src}:"*) ;;
      *) export PYTHONPATH="${md_src}:${PYTHONPATH}" ;;
    esac
  else
    export PYTHONPATH="$md_src"
  fi

  # --- socket: explicit env → cairnd-config.yaml → XDG runtime ---
  if [[ -z "${MINI_DOCKER_SOCKET:-}" ]]; then
    local cfg="${HOME}/.cairn/cairnd-config.yaml"
    if [[ -f "$cfg" ]]; then
      MINI_DOCKER_SOCKET="$(awk -F': *' '/^mini_docker_socket:/{print $2; exit}' "$cfg" | tr -d '[:space:]' || true)"
    fi
  fi
  if [[ -z "${MINI_DOCKER_SOCKET:-}" ]]; then
    if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
      MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR}/mini-docker/mini-docker.sock"
    else
      MINI_DOCKER_SOCKET="/run/user/$(id -u)/mini-docker/mini-docker.sock"
    fi
  fi
  export MINI_DOCKER_SOCKET
  MD_SOCK_DIR="$(dirname "$MINI_DOCKER_SOCKET")"
  export MD_SOCK_DIR
}

# True if Mini-Docker socket accepts a simple HTTP probe.
_cairn_md_socket_ok() {
  local sock="${1:-$MINI_DOCKER_SOCKET}"
  local py="${MD_PY:-python3}"
  [[ -S "$sock" ]] || return 1
  "$py" - <<PY 2>/dev/null
import socket, sys
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.settimeout(2)
try:
    s.connect("$sock")
    s.sendall(b"GET /containers/json HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
    data = s.recv(256)
    sys.exit(0 if data.startswith(b"HTTP") else 1)
except Exception:
    sys.exit(1)
PY
}

# Start Mini-Docker if socket missing/unresponsive. Idempotent.
ensure_minidocker() {
  cairn_runtime_discover

  # Prefer already-running healthy daemon (no root needed).
  if [[ -S "$MINI_DOCKER_SOCKET" ]]; then
    if command -v cairn >/dev/null 2>&1 && \
       MINI_DOCKER_SOCKET="$MINI_DOCKER_SOCKET" cairn doctor >/dev/null 2>&1; then
      _cairn_runtime_log "Mini-Docker already healthy at $MINI_DOCKER_SOCKET"
      return 0
    fi
    if _cairn_md_socket_ok "$MINI_DOCKER_SOCKET"; then
      _cairn_runtime_log "Mini-Docker socket responsive at $MINI_DOCKER_SOCKET"
      return 0
    fi
    _cairn_runtime_log "Stale/unhealthy Mini-Docker socket — will restart"
  fi

  [[ -n "${MD_PY:-}" ]] || _cairn_runtime_die "no python interpreter for Mini-Docker (set MINI_DOCKER_PYTHON)"
  [[ -d "${MINI_DOCKER_SRC:-}" ]] || \
    _cairn_runtime_die "Mini-Docker source not found (set MINI_DOCKER_SRC; expected sibling ../Mini-Docker)"

  _cairn_sudo_setup

  _cairn_runtime_log "Ensuring Mini-Docker socket dir: $MD_SOCK_DIR"
  if [[ ! -d "$MD_SOCK_DIR" ]]; then
    _cairn_sudo_run mkdir -p "$MD_SOCK_DIR" || \
      _cairn_runtime_die "cannot create $MD_SOCK_DIR (need sudo privileges)"
  fi
  if [[ -S "$MINI_DOCKER_SOCKET" ]]; then
    _cairn_sudo_run rm -f "$MINI_DOCKER_SOCKET" || true
  fi

  # On this host /tmp is sticky+usrquota: root often cannot open a *user-owned*
  # log for write. Use a root-created log under sudo, user-owned log otherwise.
  local md_log
  case "${CAIRN_SUDO_MODE}" in
    password|noninteractive)
      md_log="${CAIRN_MD_LOG:-/tmp/cairn-minidocker-root.log}"
      ;;
    *)
      md_log="${CAIRN_MD_LOG:-/tmp/cairn-minidocker-demo.log}"
      : >"$md_log" 2>/dev/null || md_log="/tmp/cairn-minidocker-${USER:-u}-$$.log"
      : >"$md_log" 2>/dev/null || md_log="/dev/null"
      chmod 666 "$md_log" 2>/dev/null || true
      ;;
  esac
  _cairn_runtime_log "Starting Mini-Docker daemon on $MINI_DOCKER_SOCKET (log $md_log)"
  # Background under privilege wrapper. Use nohup + bash -c for sudo so the
  # daemon is reparented and does not die when the password pipe closes.
  case "${CAIRN_SUDO_MODE}" in
    root)
      nohup env PYTHONPATH="$PYTHONPATH" "$MD_PY" -m mini_docker daemon \
        --socket "$MINI_DOCKER_SOCKET" \
        --socket-mode 666 \
        >>"$md_log" 2>&1 &
      ;;
    password)
      # shellcheck disable=SC2024
      # Root creates its own log file (cannot append to user-owned /tmp files here).
      printf '%s\n' "$SUDO_PASSWORD" | sudo -S -p '' bash -c \
        "rm -f $(printf '%q' "$md_log"); umask 000; touch $(printf '%q' "$md_log"); chmod 666 $(printf '%q' "$md_log"); nohup env PYTHONPATH=$(printf '%q' "$PYTHONPATH") $(printf '%q' "$MD_PY") -m mini_docker daemon --socket $(printf '%q' "$MINI_DOCKER_SOCKET") --socket-mode 666 >>$(printf '%q' "$md_log") 2>&1 &"
      ;;
    noninteractive)
      sudo -n bash -c \
        "rm -f $(printf '%q' "$md_log"); umask 000; touch $(printf '%q' "$md_log"); chmod 666 $(printf '%q' "$md_log"); nohup env PYTHONPATH=$(printf '%q' "$PYTHONPATH") $(printf '%q' "$MD_PY") -m mini_docker daemon --socket $(printf '%q' "$MINI_DOCKER_SOCKET") --socket-mode 666 >>$(printf '%q' "$md_log") 2>&1 &"
      ;;
  esac

  local i
  for i in $(seq 1 40); do
    if [[ -S "$MINI_DOCKER_SOCKET" ]] && _cairn_md_socket_ok "$MINI_DOCKER_SOCKET"; then
      _cairn_runtime_log "Mini-Docker ready at $MINI_DOCKER_SOCKET"
      return 0
    fi
    sleep 0.25
  done
  _cairn_runtime_die \
    "Mini-Docker socket did not become usable at $MINI_DOCKER_SOCKET (see $md_log). Start manually with sudo if needed."
}

# Start cairnd if down; align mini_docker_socket in config. Idempotent.
# Optional: CAIRN_CAIRND_LOG for nohup output (default /tmp/cairnd-demo.out).
ensure_cairnd() {
  cairn_runtime_discover

  if command -v cairn >/dev/null 2>&1; then
    cairn init >/dev/null 2>&1 || true
  fi

  local cfg="${HOME}/.cairn/cairnd-config.yaml"
  if [[ -f "$cfg" && -n "${MINI_DOCKER_SOCKET:-}" ]]; then
    if grep -q 'mini_docker_socket:' "$cfg" 2>/dev/null; then
      sed -i.bak "s|mini_docker_socket:.*|mini_docker_socket: ${MINI_DOCKER_SOCKET}|" "$cfg" || true
    else
      echo "mini_docker_socket: ${MINI_DOCKER_SOCKET}" >>"$cfg"
    fi
  fi

  if command -v cairn >/dev/null 2>&1 && cairn status >/dev/null 2>&1; then
    _cairn_runtime_log "cairnd already ready"
    return 0
  fi

  command -v cairnd >/dev/null 2>&1 || \
    _cairn_runtime_die "cairnd binary not found on PATH (build: go build -o bin/cairnd ./cmd/cairnd)"

  _cairn_runtime_log "Starting cairnd"
  if command -v cairn >/dev/null 2>&1; then
    cairn daemon stop >/dev/null 2>&1 || true
  fi
  rm -f "${HOME}/.cairn/cairnd.sock" "${HOME}/.cairn/cairnd.pid" 2>/dev/null || true

  local cd_log="${CAIRN_CAIRND_LOG:-/tmp/cairnd-demo.out}"
  nohup cairnd >"$cd_log" 2>&1 &
  local i
  for i in $(seq 1 50); do
    if command -v cairn >/dev/null 2>&1 && cairn status >/dev/null 2>&1; then
      _cairn_runtime_log "cairnd ready"
      return 0
    fi
    sleep 0.15
  done
  _cairn_runtime_die "cairnd did not become ready (see $cd_log)"
}

# Optional readiness: wait for cairn doctor, or die.
# Usage: wait_doctor [timeout_seconds]
wait_doctor() {
  local timeout="${1:-30}"
  local deadline=$((SECONDS + timeout))
  command -v cairn >/dev/null 2>&1 || _cairn_runtime_die "cairn binary not found for doctor"
  while (( SECONDS < deadline )); do
    if MINI_DOCKER_SOCKET="${MINI_DOCKER_SOCKET:-}" cairn doctor >/dev/null 2>&1; then
      _cairn_runtime_log "cairn doctor OK"
      return 0
    fi
    sleep 0.5
  done
  _cairn_runtime_die "cairn doctor failed within ${timeout}s (Mini-Docker / cairnd not healthy)"
}

# Convenience: full live-runtime bootstrap used by proof scripts.
# Dies if CAIRN_ROOTFS missing after discovery.
cairn_runtime_bootstrap() {
  cairn_runtime_discover
  [[ -n "${CAIRN_ROOTFS:-}" && -d "$CAIRN_ROOTFS" ]] || \
    _cairn_runtime_die "Set CAIRN_ROOTFS to a Mini-Docker rootfs (bin/busybox required). Sibling ../Mini-Docker recommended."
  ensure_minidocker
  ensure_cairnd
  if [[ "${CAIRN_WAIT_DOCTOR:-1}" == "1" ]]; then
    wait_doctor "${CAIRN_DOCTOR_TIMEOUT:-30}"
  fi
}
