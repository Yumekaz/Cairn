#!/usr/bin/env bash
# =============================================================================
# prove_mlp.sh — THE single command for Closeout A (single-node MLP spine)
# =============================================================================
#
# What A IS:
#   End-to-end proof that a stranger can install Cairn (sibling DURAFLOW +
#   Mini-Docker layout), pass unit + crash-recovery tests, and green the
#   live single-node demos: clean deploy story, mid-deploy cairnd kill,
#   rollback safety, and the failure matrix (F1–F6).
#
# What A is NOT:
#   Multi-node / Phase 18. FailForge campaigns. Product B (TLS termination,
#   Docker Hub pulls, multi-host placement). Dashboard polish. CI runner
#   substitutes for Mini-Docker (GitHub Actions stays unit/build only).
#
# Usage:
#   ./scripts/prove_mlp.sh
#   PROVE_QUICK=1 ./scripts/prove_mlp.sh   # skip F2 (SIGKILL duplicate of F1)
#   SKIP_UNIT=1 ./scripts/prove_mlp.sh     # live demos only (rare)
#
# Prerequisites:
#   - Linux, Go matching go.mod, sibling ../DURAFLOW and ../Mini-Docker (or envs)
#   - sudo (or root) to start Mini-Docker if not already running
#   - CAIRN_ROOTFS discoverable or set
#
# Exit: 0 all green; non-zero on first failure (fail-fast).
# =============================================================================

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"

LOG_DIR="${LOG_DIR:-/tmp/cairn-proof-runs}"
mkdir -p "$LOG_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
LOG="${LOG_DIR}/prove_mlp_${STAMP}.log"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
BLUE=$'\033[0;34m'
NC=$'\033[0m'

log() { echo "[prove] $*" | tee -a "$LOG"; }
green() { echo -e "[prove] ${GREEN}GREEN${NC} $*" | tee -a "$LOG"; }
red() { echo -e "[prove] ${RED}RED${NC} $*" | tee -a "$LOG" >&2; }
die() { red "$* (see $LOG)"; exit 1; }

log "${BLUE}=== Closeout A: prove_mlp ===${NC}"
log "ROOT=$ROOT log=$LOG"
log "PROVE_QUICK=${PROVE_QUICK:-0} SKIP_UNIT=${SKIP_UNIT:-0}"

# Shared Mini-Docker / cairnd discovery (+ ensure before live demos)
# shellcheck source=lib/runtime.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/runtime.sh"
export CAIRN_RUNTIME_LOG="$LOG"
cairn_runtime_discover

log "CAIRN_ROOTFS=${CAIRN_ROOTFS:-<unset>}"
log "MINI_DOCKER_SOCKET=$MINI_DOCKER_SOCKET"
log "PYTHONPATH=$PYTHONPATH"

run_step() {
  local name="$1"
  shift
  log "=== $name ==="
  if "$@" >>"$LOG" 2>&1; then
    green "$name"
  else
    die "$name failed"
  fi
}

# --- build binaries if needed ---
ensure_binaries() {
  if [[ ! -x bin/cairn || ! -x bin/cairnd ]]; then
    log "building binaries"
    go build -o bin/cairn ./cmd/cairn
    go build -o bin/cairnd ./cmd/cairnd
  fi
  mkdir -p "${HOME}/.local/bin"
  cp -f bin/cairn bin/cairnd "${HOME}/.local/bin/" 2>/dev/null || true
  command -v cairn >/dev/null || die "cairn not on PATH after build"
  command -v cairnd >/dev/null || die "cairnd not on PATH after build"
}

# ---------------------------------------------------------------------------
# (a)+(b) Units + integration crash recovery + bash -n
# Reuse stability_gate SKIP_LIVE so we do not re-implement unit lists.
# ---------------------------------------------------------------------------
if [[ "${SKIP_UNIT:-0}" != "1" ]]; then
  ensure_binaries
  run_step "stability_gate SKIP_LIVE (units + bash -n)" \
    env N=1 SKIP_LIVE=1 LOG_DIR="$LOG_DIR" bash scripts/stability_gate.sh
else
  log "SKIP_UNIT=1 — skipping unit packages / integration"
  run_step "bash -n scripts" bash -c '
    for s in scripts/*.sh scripts/lib/*.sh; do
      [[ -f "$s" ]] || continue
      bash -n "$s" || exit 1
    done
  '
  ensure_binaries
fi

# ---------------------------------------------------------------------------
# (c) Ensure Mini-Docker + cairnd via scripts/lib/runtime.sh
# (d) clean_demo.sh
# ---------------------------------------------------------------------------
[[ -n "${CAIRN_ROOTFS:-}" && -d "$CAIRN_ROOTFS" ]] || \
  die "Set CAIRN_ROOTFS to a Mini-Docker rootfs (bin/busybox). Sibling ../Mini-Docker recommended."

log "=== ensure Mini-Docker + cairnd ==="
ensure_minidocker
ensure_cairnd
wait_doctor 30
green "ensure Mini-Docker + cairnd"

run_step "clean_demo (starts Mini-Docker + cairnd if needed)" \
  env CAIRN_ROOTFS="$CAIRN_ROOTFS" \
      MINI_DOCKER_SOCKET="$MINI_DOCKER_SOCKET" \
      PYTHONPATH="$PYTHONPATH" \
      bash scripts/clean_demo.sh

# Doctor readiness after clean_demo
wait_doctor 15
green "doctor after clean_demo"

# ---------------------------------------------------------------------------
# (e) mid_deploy_crash_demo SIGTERM
# ---------------------------------------------------------------------------
run_step "mid_deploy_crash_demo (SIGTERM)" \
  env CAIRN_ROOTFS="$CAIRN_ROOTFS" \
      MINI_DOCKER_SOCKET="$MINI_DOCKER_SOCKET" \
      PYTHONPATH="$PYTHONPATH" \
      KILL_SIGNAL=SIGTERM \
      bash scripts/mid_deploy_crash_demo.sh

# ---------------------------------------------------------------------------
# (f) rollback_safety_demo
# ---------------------------------------------------------------------------
run_step "rollback_safety_demo" \
  env CAIRN_ROOTFS="$CAIRN_ROOTFS" \
      MINI_DOCKER_SOCKET="$MINI_DOCKER_SOCKET" \
      PYTHONPATH="$PYTHONPATH" \
      bash scripts/rollback_safety_demo.sh

# ---------------------------------------------------------------------------
# (g) failure_matrix F1–F6 (PROVE_QUICK=1 skips F2 SIGKILL duplicate of F1)
# ---------------------------------------------------------------------------
if [[ "${PROVE_QUICK:-0}" == "1" ]]; then
  MATRIX_CASE="F1,F3,F4,F5,F6"
  log "PROVE_QUICK=1 — matrix CASE=$MATRIX_CASE (skip F2)"
else
  MATRIX_CASE="F1,F2,F3,F4,F5,F6"
  log "full matrix CASE=$MATRIX_CASE"
fi

run_step "failure_matrix ($MATRIX_CASE)" \
  env CAIRN_ROOTFS="$CAIRN_ROOTFS" \
      MINI_DOCKER_SOCKET="$MINI_DOCKER_SOCKET" \
      PYTHONPATH="$PYTHONPATH" \
      CASE="$MATRIX_CASE" \
      LOG_DIR="$LOG_DIR" \
      bash scripts/failure_matrix.sh

log ""
green "=== ALL GREEN — Closeout A prove_mlp complete ==="
log "summary log: $LOG"
log "Not in A: multi-node, FailForge, TLS/Docker Hub product B"
exit 0
