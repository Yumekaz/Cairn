#!/usr/bin/env bash
# Cairn local stability gate (Phase 17/19 proof loop).
# Full demos need Linux + Mini-Docker. CI only runs unit/build via smoke.yml.
#
# Closeout A (full MLP spine, single command):
#   ./scripts/prove_mlp.sh
#   PROVE_QUICK=1 ./scripts/prove_mlp.sh   # skip F2 SIGKILL duplicate of F1
# prove_mlp = units + bash -n + clean_demo + mid_deploy + rollback + failure_matrix.
# This gate remains the modular / CI-friendly partial path (SKIP_LIVE, optional demos).
#
# Usage:
#   ./scripts/stability_gate.sh
#   N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh   # faster local loop
#   N=1 SKIP_LIVE=1 ./scripts/stability_gate.sh         # unit + bash -n only
#   N=5 ./scripts/stability_gate.sh                     # release-ish
#   RUN_MATRIX=1 N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh
#   RUN_ROLLBACK_DEMO=1 N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh
#
# Env:
#   N                   times to run each live demo (default 1)
#   SKIP_UNIT=1         skip go test
#   SKIP_COLD_CLONE=1   skip cold_clone_verify (use clean_demo instead if SKIP_CLEAN_DEMO!=1)
#   SKIP_CLEAN_DEMO=1   skip clean_demo
#   SKIP_MID_DEPLOY=1   skip mid_deploy_crash_demo
#   RUN_ROLLBACK_DEMO=1 also run scripts/rollback_safety_demo.sh (needs healthy Mini-Docker)
#   RUN_MATRIX=1        also run scripts/failure_matrix.sh (CASE default F1,F2,F3,F4,F6)
#   MATRIX_CASE         override CASE for failure_matrix when RUN_MATRIX=1
#   SKIP_LIVE=1         skip all live Mini-Docker demos
#   LOG_DIR             default: /tmp/cairn-proof-runs
#
# Full Closeout A (includes rollback + matrix by default): ./scripts/prove_mlp.sh

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"

N="${N:-1}"
LOG_DIR="${LOG_DIR:-/tmp/cairn-proof-runs}"
mkdir -p "$LOG_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
LOG="${LOG_DIR}/stability_gate_${STAMP}.log"

log() { echo "[gate] $*" | tee -a "$LOG"; }
die() { echo "[gate] RED: $*" | tee -a "$LOG" >&2; exit 1; }

log "ROOT=$ROOT log=$LOG N=$N"
log "full Closeout A entrypoint: ./scripts/prove_mlp.sh"

# Shared Mini-Docker / cairnd bootstrap (discover only until live section)
# shellcheck source=lib/runtime.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/runtime.sh"
cairn_runtime_discover
export CAIRN_RUNTIME_LOG="$LOG"

run_step() {
  local name="$1"
  shift
  log "=== $name ==="
  if "$@" >>"$LOG" 2>&1; then
    log "GREEN $name"
  else
    die "$name failed (see $LOG)"
  fi
}

# 1) Units
if [[ "${SKIP_UNIT:-0}" != "1" ]]; then
  run_step "unit packages" go test ./internal/deploymeta/ ./internal/daemon/ ./internal/config/ ./internal/preflight/ ./internal/store/ -count=1
  run_step "integration TestDuraFlowCrashRecovery" go test ./tests/integration/ -count=1 -run TestDuraFlowCrashRecovery -timeout 120s
fi

# 2) Script syntax
run_step "bash -n scripts" bash -c '
  for s in scripts/*.sh scripts/lib/*.sh; do
    [[ -f "$s" ]] || continue
    bash -n "$s" || exit 1
  done
'

if [[ "${SKIP_LIVE:-0}" == "1" ]]; then
  log "SKIP_LIVE=1 — unit/syntax only"
  log "ALL GREEN (partial)"
  log "for full Closeout A: ./scripts/prove_mlp.sh"
  exit 0
fi

# Ensure binaries
if [[ ! -x bin/cairn || ! -x bin/cairnd ]]; then
  log "building binaries"
  go build -o bin/cairn ./cmd/cairn
  go build -o bin/cairnd ./cmd/cairnd
fi
cp -f bin/cairn bin/cairnd "${HOME}/.local/bin/" 2>/dev/null || true

# Bootstrap Mini-Docker + cairnd once before live demos (no hanging sudo)
log "ensuring Mini-Docker + cairnd (CAIRN_ROOTFS=${CAIRN_ROOTFS:-<unset>})"
ensure_minidocker
ensure_cairnd

# Live demos
for i in $(seq 1 "$N"); do
  log "----- live pass $i/$N -----"
  if [[ "${SKIP_COLD_CLONE:-0}" != "1" ]]; then
    run_step "cold_clone_verify ($i)" bash scripts/cold_clone_verify.sh
  elif [[ "${SKIP_CLEAN_DEMO:-0}" != "1" ]]; then
    run_step "clean_demo ($i)" bash scripts/clean_demo.sh
  fi
  if [[ "${SKIP_MID_DEPLOY:-0}" != "1" ]]; then
    ensure_cairnd
    run_step "mid_deploy_crash_demo ($i)" bash scripts/mid_deploy_crash_demo.sh
  fi
  if [[ "${RUN_ROLLBACK_DEMO:-0}" == "1" ]]; then
    ensure_cairnd
    run_step "rollback_safety_demo ($i)" bash scripts/rollback_safety_demo.sh
  fi
  if [[ "${RUN_MATRIX:-0}" == "1" ]]; then
    ensure_cairnd
    run_step "failure_matrix ($i)" \
      env CASE="${MATRIX_CASE:-F1,F2,F3,F4,F6}" LOG_DIR="$LOG_DIR" \
      bash scripts/failure_matrix.sh
  fi
done

log "ALL GREEN"
log "summary log: $LOG"
log "for full Closeout A (rollback + matrix included): ./scripts/prove_mlp.sh"
exit 0
