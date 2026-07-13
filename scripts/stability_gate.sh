#!/usr/bin/env bash
# Cairn local stability gate (Phase A).
# Full demos need Linux + Mini-Docker. CI only runs unit/build via smoke.yml.
#
# Usage:
#   ./scripts/stability_gate.sh
#   N=1 SKIP_COLD_CLONE=1 ./scripts/stability_gate.sh   # faster local loop
#   N=5 ./scripts/stability_gate.sh                     # release-ish
#
# Env:
#   N                 times to run each live demo (default 1)
#   SKIP_UNIT=1       skip go test
#   SKIP_COLD_CLONE=1 skip cold_clone_verify (use clean_demo instead if SKIP_CLEAN_DEMO!=1)
#   SKIP_CLEAN_DEMO=1 skip clean_demo
#   SKIP_MID_DEPLOY=1 skip mid_deploy_crash_demo
#   SKIP_LIVE=1       skip all live Mini-Docker demos
#   LOG_DIR           default: /tmp/cairn-proof-runs

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

# Discover rootfs if unset
if [[ -z "${CAIRN_ROOTFS:-}" ]]; then
  for cand in "${ROOT}/../Mini-Docker/rootfs" "${HOME}/Desktop/Mini-Docker/rootfs"; do
    if [[ -x "${cand}/bin/busybox" ]]; then
      export CAIRN_ROOTFS="$(cd "$(dirname "$cand")" && pwd)/$(basename "$cand")"
      break
    fi
  done
fi
export MINI_DOCKER_SOCKET="${MINI_DOCKER_SOCKET:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock}"
export PYTHONPATH="${PYTHONPATH:-${ROOT}/../Mini-Docker}"

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
  for s in scripts/*.sh; do bash -n "$s" || exit 1; done
'

if [[ "${SKIP_LIVE:-0}" == "1" ]]; then
  log "SKIP_LIVE=1 — unit/syntax only"
  log "ALL GREEN (partial)"
  exit 0
fi

# Ensure binaries
if [[ ! -x bin/cairn || ! -x bin/cairnd ]]; then
  log "building binaries"
  go build -o bin/cairn ./cmd/cairn
  go build -o bin/cairnd ./cmd/cairnd
fi
cp -f bin/cairn bin/cairnd "${HOME}/.local/bin/" 2>/dev/null || true

# Live demos
for i in $(seq 1 "$N"); do
  log "----- live pass $i/$N -----"
  if [[ "${SKIP_COLD_CLONE:-0}" != "1" ]]; then
    run_step "cold_clone_verify ($i)" bash scripts/cold_clone_verify.sh
  elif [[ "${SKIP_CLEAN_DEMO:-0}" != "1" ]]; then
    # Ensure cairnd up for clean_demo
    if ! cairn status >/dev/null 2>&1; then
      nohup cairnd >>"$LOG" 2>&1 &
      sleep 1
    fi
    run_step "clean_demo ($i)" bash scripts/clean_demo.sh
  fi
  if [[ "${SKIP_MID_DEPLOY:-0}" != "1" ]]; then
    if ! cairn status >/dev/null 2>&1; then
      nohup cairnd >>"$LOG" 2>&1 &
      sleep 1
    fi
    run_step "mid_deploy_crash_demo ($i)" bash scripts/mid_deploy_crash_demo.sh
  fi
done

log "ALL GREEN"
log "summary log: $LOG"
exit 0
