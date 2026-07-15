#!/usr/bin/env bash
# Prove rollback safety: intervening StateTouched (migration) deploy blocks
# rollback without --force and emits RollbackBlocked.
# Optional: FORCE=1 also exercises forced rollback (emits RollbackForced).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"
LOG() { echo "[rollback-demo] $*"; }
die() { echo "[rollback-demo] ERROR: $*" >&2; exit 1; }

command -v cairn >/dev/null || die "cairn not on PATH (build bin/ first)"
command -v cairnd >/dev/null || die "cairnd not on PATH"

# Reuse clean_demo env discovery for Mini-Docker / rootfs
if [[ -z "${CAIRN_ROOTFS:-}" ]]; then
  for cand in \
    "${ROOT}/../Mini-Docker/rootfs" \
    "${HOME}/Desktop/Mini-Docker/rootfs"; do
    if [[ -x "${cand}/bin/busybox" || -x "${cand}/bin/sh" ]]; then
      export CAIRN_ROOTFS="$(cd "$(dirname "$cand")" && pwd)/$(basename "$cand")"
      break
    fi
  done
fi
[[ -n "${CAIRN_ROOTFS:-}" && -d "$CAIRN_ROOTFS" ]] || die "Set CAIRN_ROOTFS"

if [[ -z "${MINI_DOCKER_SOCKET:-}" ]]; then
  if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
    export MINI_DOCKER_SOCKET="${XDG_RUNTIME_DIR}/mini-docker/mini-docker.sock"
  else
    export MINI_DOCKER_SOCKET="/run/user/$(id -u)/mini-docker/mini-docker.sock"
  fi
fi

cairn status >/dev/null 2>&1 || die "cairnd not running (start Mini-Docker + cairnd first, e.g. via clean_demo)"

# Unique service name to avoid clobbering counter-api demos if desired.
# Default reuses counter-api for a simple path; set RB_SERVICE to override.
SVC="${RB_SERVICE:-counter-api}"
BASE_YAML="${ROOT}/examples/counter-api/cairn.yaml"
MIG_YAML="${ROOT}/examples/counter-api/cairn_with_migration.yaml"
# If custom service name, rewrite name: line into temp configs
if [[ "$SVC" != "counter-api" ]]; then
  TMPD="$(mktemp -d)"
  trap 'rm -rf "$TMPD"' EXIT
  sed "s/^name: counter-api/name: ${SVC}/" "$BASE_YAML" >"${TMPD}/base.yaml"
  sed "s/^name: counter-api/name: ${SVC}/" "$MIG_YAML" >"${TMPD}/mig.yaml"
  BASE_YAML="${TMPD}/base.yaml"
  MIG_YAML="${TMPD}/mig.yaml"
fi

mkdir -p "${HOME}/.cairn/volumes/counter-data"
echo "RB_OK" >"${HOME}/.cairn/volumes/counter-data/index.html"
if [[ -d "${ROOT}/examples/counter-api/www" ]]; then
  cp -a "${ROOT}/examples/counter-api/www/." "${HOME}/.cairn/volumes/counter-data/" 2>/dev/null || true
  echo "RB_OK" >"${HOME}/.cairn/volumes/counter-data/index.html"
fi

LOG "Deploy baseline (no migration)"
cairn deploy "$BASE_YAML"
BASE_DEPLOY="$(cairn inspect "$SVC" | awk -F': *' '/^Current Deploy ID:/{print $2}' | head -1 | tr -d '[:space:]')"
[[ -n "$BASE_DEPLOY" ]] || die "could not parse baseline deploy id"
LOG "baseline deploy=$BASE_DEPLOY"

LOG "Deploy with migration (StateTouched=true)"
cairn deploy "$MIG_YAML"
TOUCHED_DEPLOY="$(cairn inspect "$SVC" | awk -F': *' '/^Current Deploy ID:/{print $2}' | head -1 | tr -d '[:space:]')"
[[ -n "$TOUCHED_DEPLOY" ]] || die "could not parse migration deploy id"
[[ "$TOUCHED_DEPLOY" != "$BASE_DEPLOY" ]] || die "migration deploy id did not change"
LOG "state-touched deploy=$TOUCHED_DEPLOY"

LOG "Attempt rollback without --force (expect safety warning / 409)"
set +e
OUT="$(cairn rollback "$SVC" --to "$BASE_DEPLOY" 2>&1)"
RC=$?
set -e
echo "$OUT"
# CLI prints warning and returns 0 on 409, or may return error — either is OK if message present
echo "$OUT" | grep -qiE 'ROLLBACK SAFETY|unsafe|state' || die "expected rollback safety warning in CLI output"
# Current must still be migration deploy
CUR="$(cairn inspect "$SVC" | awk -F': *' '/^Current Deploy ID:/{print $2}' | head -1 | tr -d '[:space:]')"
[[ "$CUR" == "$TOUCHED_DEPLOY" ]] || die "current_deploy_id changed without force: want $TOUCHED_DEPLOY got $CUR"

LOG "Assert RollbackBlocked event"
EVENTS="$(cairn events 2>/dev/null || true)"
echo "$EVENTS" | grep -q RollbackBlocked || die "missing RollbackBlocked in event timeline"
LOG "RollbackBlocked: OK (rc=$RC)"

if [[ "${FORCE:-0}" == "1" ]]; then
  LOG "Force rollback (FORCE=1)"
  cairn rollback "$SVC" --to "$BASE_DEPLOY" --force
  EVENTS2="$(cairn events 2>/dev/null || true)"
  echo "$EVENTS2" | grep -q RollbackForced || die "missing RollbackForced after --force"
  LOG "RollbackForced: OK"
else
  LOG "Skipping forced path (set FORCE=1 to exercise RollbackForced live)"
fi

LOG "=== ROLLBACK SAFETY DEMO PASSED ==="
exit 0
