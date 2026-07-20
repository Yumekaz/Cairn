#!/usr/bin/env bash
# =============================================================================
# prove_portability.sh — Portability A proof on the SAME machine (no second PC)
# =============================================================================
#
# Creates a clean sibling tree under ${PORT_DIR:-/tmp/cairn-portability-$$},
# copies (or git-clones) Cairn + DURAFLOW + Mini-Docker there, builds via
# install.sh, runs stability_gate (always), and optionally live prove/clean_demo
# when sudo/root is available.
#
# Does NOT use Desktop paths. Prefer local copy over remotes so proofs work
# offline / without a second machine.
#
# Usage:
#   ./scripts/prove_portability.sh
#   PORT_DIR=/tmp/cairn-port ./scripts/prove_portability.sh
#   KEEP_PORT_DIR=1 ./scripts/prove_portability.sh   # leave tree after success
#
# Env:
#   PORT_DIR          target parent (default /tmp/cairn-portability-$$)
#   KEEP_PORT_DIR=1   do not rm -rf PORT_DIR on success
#   SUDO_PASSWORD     optional non-interactive sudo for live Mini-Docker
#   SKIP_HARDCODE_GREP=1  skip /home/yumekaz guard in copied Cairn tree
#   COPY_MODE=rsync|cp|git   force copy strategy (default: auto)
#
# Exit: 0 GREEN; non-zero RED.
# =============================================================================

set -euo pipefail

SRC_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT_DIR="${PORT_DIR:-/tmp/cairn-portability-$$}"
KEEP_PORT_DIR="${KEEP_PORT_DIR:-0}"
SKIP_HARDCODE_GREP="${SKIP_HARDCODE_GREP:-0}"
COPY_MODE="${COPY_MODE:-auto}"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
BLUE=$'\033[0;34m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

LOG_DIR="${LOG_DIR:-/tmp/cairn-proof-runs}"
mkdir -p "$LOG_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
LOG="${LOG_DIR}/prove_portability_${STAMP}.log"

log() { echo "[portability] $*" | tee -a "$LOG"; }
green() { echo -e "[portability] ${GREEN}GREEN${NC} $*" | tee -a "$LOG"; }
red() { echo -e "[portability] ${RED}RED${NC} $*" | tee -a "$LOG" >&2; }
yellow() { echo -e "[portability] ${YELLOW}$*${NC}" | tee -a "$LOG"; }
die() { red "$* (see $LOG)"; exit 1; }

cleanup_on_fail() {
  local rc=$?
  if (( rc != 0 )); then
    red "FAILED (exit $rc) — tree left at $PORT_DIR for inspection"
  fi
}
trap cleanup_on_fail EXIT

log "${BLUE}=== Portability A: prove_portability ===${NC}"
log "SRC_ROOT=$SRC_ROOT"
log "PORT_DIR=$PORT_DIR"
log "log=$LOG"

# Refuse Desktop-based PORT_DIR by default (prove must not depend on it)
case "$PORT_DIR" in
  */Desktop/*|*/Desktop)
    die "PORT_DIR must not be under Desktop (got $PORT_DIR); use /tmp or another path"
    ;;
esac

# Locate sibling sources next to this Cairn checkout
SRC_PARENT="$(cd "$SRC_ROOT/.." && pwd)"
SRC_DURAFLOW="${DURAFLOW_PATH:-$SRC_PARENT/DURAFLOW}"
SRC_MINI="${MINI_DOCKER_PATH:-$SRC_PARENT/Mini-Docker}"

[[ -d "$SRC_ROOT" ]] || die "Cairn source missing: $SRC_ROOT"
[[ -f "$SRC_DURAFLOW/go.mod" ]] || die "DURAFLOW sibling not found at $SRC_DURAFLOW (set DURAFLOW_PATH)"
[[ -d "$SRC_MINI" ]] || die "Mini-Docker sibling not found at $SRC_MINI (set MINI_DOCKER_PATH)"

log "Source layout:"
log "  Cairn:      $SRC_ROOT"
log "  DURAFLOW:   $SRC_DURAFLOW"
log "  Mini-Docker:$SRC_MINI"

# --- clean tree ---
log "Wiping and recreating $PORT_DIR"
rm -rf "$PORT_DIR"
mkdir -p "$PORT_DIR"

# --- copy helper: prefer local clone/rsync/cp; never Desktop ---
copy_repo() {
  local src="$1"
  local dest_name="$2"
  local dest="$PORT_DIR/$dest_name"
  local mode="$COPY_MODE"

  # Prefer rsync/cp of the *working tree* so uncommitted Portability A fixes
  # are tested. git clone --local only copies HEAD and would miss WIP.
  if [[ "$mode" == "auto" ]]; then
    if command -v rsync >/dev/null 2>&1; then
      mode="rsync"
    else
      mode="cp"
    fi
  fi

  log "Copying $dest_name via $mode from $src"
  case "$mode" in
    git)
      # Local clone: fast, preserves history shallow, no remote needed
      if git -C "$src" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        git clone --local --shared "$src" "$dest" >>"$LOG" 2>&1 \
          || git clone --local "$src" "$dest" >>"$LOG" 2>&1 \
          || {
            log "git clone --local failed; falling back to rsync/cp"
            if command -v rsync >/dev/null 2>&1; then
              rsync -a --exclude '.git' --exclude 'bin/' --exclude 'venv/' \
                --exclude '__pycache__/' --exclude '*.pyc' --exclude '.cache/' \
                "$src/" "$dest/"
            else
              mkdir -p "$dest"
              cp -a "$src/." "$dest/"
            fi
          }
      else
        mkdir -p "$dest"
        if command -v rsync >/dev/null 2>&1; then
          rsync -a --exclude '.git' "$src/" "$dest/"
        else
          cp -a "$src/." "$dest/"
        fi
      fi
      ;;
    rsync)
      mkdir -p "$dest"
      # For Mini-Docker keep rootfs; drop heavy venv if present (system python ok)
      if [[ "$dest_name" == "Mini-Docker" ]]; then
        rsync -a \
          --exclude '.git/' \
          --exclude 'venv/' \
          --exclude '__pycache__/' \
          --exclude '*.pyc' \
          --exclude 'mini_docker_runtime.egg-info/' \
          --exclude '.pytest_cache/' \
          "$src/" "$dest/"
      elif [[ "$dest_name" == "Cairn" || "$dest_name" == "SERVER" ]]; then
        rsync -a \
          --exclude '.git/' \
          --exclude 'bin/cairn' --exclude 'bin/cairnd' \
          --exclude '*.test' \
          "$src/" "$dest/"
      else
        rsync -a --exclude '.git/' "$src/" "$dest/"
      fi
      ;;
    cp)
      mkdir -p "$dest"
      cp -a "$src/." "$dest/"
      # Best-effort drop heavy/noise dirs after cp
      rm -rf "$dest/venv" "$dest/__pycache__" 2>/dev/null || true
      ;;
    *)
      die "unknown COPY_MODE=$mode"
      ;;
  esac
  [[ -d "$dest" ]] || die "copy failed for $dest_name"
}

# Destination names: sibling layout Cairn + DURAFLOW + Mini-Docker
# (source may be named SERVER; dest is always Cairn for stranger-like layout)
copy_repo "$SRC_ROOT" "Cairn"
copy_repo "$SRC_DURAFLOW" "DURAFLOW"
copy_repo "$SRC_MINI" "Mini-Docker"

PORT_CAIRN="$PORT_DIR/Cairn"
PORT_DF="$PORT_DIR/DURAFLOW"
PORT_MD="$PORT_DIR/Mini-Docker"

# Sanity: no Desktop paths embedded as required layout
log "Port tree layout:"
log "  $PORT_CAIRN"
log "  $PORT_DF"
log "  $PORT_MD"

[[ -f "$PORT_CAIRN/go.mod" ]] || die "copied Cairn missing go.mod"
[[ -f "$PORT_DF/go.mod" ]] || die "copied DURAFLOW missing go.mod"
[[ -d "$PORT_MD" ]] || die "copied Mini-Docker missing"

# Ensure sibling replace in go.mod (no absolute /home paths)
cd "$PORT_CAIRN"
if grep -E 'replace .*duraflow => /home/' go.mod >/dev/null 2>&1; then
  die "go.mod hardcodes a /home path for duraflow"
fi
if ! grep -q 'replace github.com/yumekaz/duraflow => ../DURAFLOW' go.mod; then
  log "Rewriting go.mod replace → ../DURAFLOW"
  go mod edit -replace="github.com/yumekaz/duraflow=../DURAFLOW"
fi

# Optional hardcode guard: fail if /home/yumekaz remains in copied Cairn source
if [[ "${SKIP_HARDCODE_GREP:-0}" != "1" ]]; then
  log "Hardcode guard: grepping copied Cairn for /home/yumekaz (scripts, internal, cmd, tests)"
  if grep -RIn --exclude-dir=.git --exclude-dir=bin --exclude='*.log' \
    '/home/yumekaz' \
    "$PORT_CAIRN/scripts" "$PORT_CAIRN/internal" "$PORT_CAIRN/cmd" "$PORT_CAIRN/tests" \
    2>/dev/null | grep -v 'prove_portability.sh' | grep -v 'PORTABILITY_A.md' | head -20; then
    die "hardcode guard: /home/yumekaz found in copied Cairn source (Portability A)"
  fi
  green "hardcode guard clean"
else
  yellow "SKIP_HARDCODE_GREP=1 — skipping /home/yumekaz guard"
fi

# Prove discovery does not require Desktop: unset Desktop-ish env noise
unset MINI_DOCKER_SRC 2>/dev/null || true
# Do not set CAIRN_ROOTFS yet — install.sh + runtime should find sibling

log "Building via install.sh (sibling layout under $PORT_DIR)"
chmod +x scripts/install.sh scripts/*.sh 2>/dev/null || true
chmod +x scripts/lib/*.sh 2>/dev/null || true
./scripts/install.sh >>"$LOG" 2>&1 || die "install.sh failed"

export PATH="$PORT_CAIRN/bin:${HOME}/.local/bin:${PATH}"
export CAIRN_ROOTFS="$PORT_MD/rootfs"
export PYTHONPATH="$PORT_MD${PYTHONPATH:+:$PYTHONPATH}"
export MINI_DOCKER_SOCKET="${MINI_DOCKER_SOCKET:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock}"
export MINI_DOCKER_SRC="$PORT_MD"
export MINI_DOCKER_PATH="$PORT_MD"
export DURAFLOW_PATH="$PORT_DF"

# Prefer copied tree for all discovery
export ROOT="$PORT_CAIRN"

if [[ ! -x "$CAIRN_ROOTFS/bin/busybox" && ! -x "$CAIRN_ROOTFS/bin/sh" ]]; then
  yellow "WARN: rootfs at $CAIRN_ROOTFS looks incomplete (busybox/sh missing)"
  yellow "Unit/smoke may still pass; live demos will fail without a real rootfs"
fi

# Verify runtime.sh resolves sibling without Desktop
# shellcheck source=lib/runtime.sh
source "$PORT_CAIRN/scripts/lib/runtime.sh"
# Clear env rootfs briefly to test discovery from ROOT sibling
_save_rootfs="${CAIRN_ROOTFS:-}"
unset CAIRN_ROOTFS MINI_DOCKER_ROOTFS
ROOT="$PORT_CAIRN"
cairn_runtime_discover
if [[ -z "${CAIRN_ROOTFS:-}" ]]; then
  yellow "WARN: runtime discover did not find rootfs (ok if rootfs incomplete)"
else
  case "$CAIRN_ROOTFS" in
    */Desktop/*)
      die "runtime discover returned Desktop path: $CAIRN_ROOTFS"
      ;;
  esac
  log "runtime discover CAIRN_ROOTFS=$CAIRN_ROOTFS"
fi
# Restore explicit sibling paths for demos
export CAIRN_ROOTFS="${_save_rootfs:-$PORT_MD/rootfs}"
export MINI_DOCKER_SRC="$PORT_MD"
export PYTHONPATH="$PORT_MD${PYTHONPATH:+:$PYTHONPATH}"
green "sibling discovery (no Desktop required)"

# --- always: stability gate unit/syntax ---
log "=== N=1 SKIP_LIVE=1 stability_gate.sh ==="
if N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh >>"$LOG" 2>&1; then
  green "stability_gate (SKIP_LIVE=1)"
else
  die "stability_gate (SKIP_LIVE=1) failed"
fi

# --- live path only if privilege available ---
can_live=0
if [[ "${EUID}" -eq 0 ]]; then
  can_live=1
  log "live: running as root"
elif [[ -n "${SUDO_PASSWORD:-}" ]]; then
  can_live=1
  log "live: SUDO_PASSWORD set"
elif command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
  can_live=1
  log "live: passwordless sudo (-n) available"
fi

if (( can_live )); then
  log "=== LIVE: PROVE_QUICK=1 prove_mlp.sh (or clean_demo fallback) ==="
  if [[ -x ./scripts/prove_mlp.sh ]]; then
    if PROVE_QUICK=1 ./scripts/prove_mlp.sh >>"$LOG" 2>&1; then
      green "prove_mlp (PROVE_QUICK=1)"
    else
      die "prove_mlp (PROVE_QUICK=1) failed"
    fi
  else
    if ./scripts/clean_demo.sh >>"$LOG" 2>&1; then
      green "clean_demo"
    else
      die "clean_demo failed"
    fi
  fi
else
  yellow "LIVE_SKIPPED: need root, passwordless sudo (-n), or SUDO_PASSWORD for Mini-Docker live proofs"
  yellow "Unit/smoke path still ran. Re-run with privileges for full Portability A live check."
fi

if [[ "$KEEP_PORT_DIR" != "1" ]]; then
  log "Cleaning $PORT_DIR (KEEP_PORT_DIR=1 to retain)"
  rm -rf "$PORT_DIR"
else
  log "KEEP_PORT_DIR=1 — left tree at $PORT_DIR"
fi

echo ""
green "========================================"
green " Portability A: ALL GREEN"
green "========================================"
log "Log: $LOG"
trap - EXIT
exit 0
