#!/usr/bin/env bash
# Best-effort demo hygiene: remove leftover demo services and optional FailForge
# workload names so the next clean_demo / matrix run starts quieter.
#
# Does NOT wipe ~/.cairn entirely (keeps config, volumes data optional).
# Usage:
#   ./scripts/demo_reset.sh
#   WIPE_VOLUMES=1 ./scripts/demo_reset.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"
LOG() { echo "[demo-reset] $*"; }

if ! command -v cairn >/dev/null 2>&1; then
  LOG "cairn not on PATH — nothing to reset via CLI"
  exit 0
fi

if ! cairn status >/dev/null 2>&1; then
  LOG "cairnd not running — skip service remove (ok)"
  exit 0
fi

# Known demo / matrix service names (counter-api + occasional FailForge leftovers)
NAMES=(
  counter-api
  migrated-service
  rb-svc
  rb-force
)

# Also strip any service whose name starts with ff-service (FailForge harness)
if cairn ps >/dev/null 2>&1; then
  while read -r name; do
    [[ -z "$name" || "$name" == "NAME" ]] && continue
    if [[ "$name" == ff-service* || "$name" == ff-* ]]; then
      NAMES+=("$name")
    fi
  done < <(cairn ps 2>/dev/null | awk 'NR>1 {print $1}' || true)
fi

# Dedup
declare -A SEEN=()
for name in "${NAMES[@]}"; do
  [[ -n "${SEEN[$name]:-}" ]] && continue
  SEEN[$name]=1
  if cairn inspect "$name" >/dev/null 2>&1; then
    LOG "removing service $name"
    cairn rm "$name" 2>/dev/null || true
  fi
done

if [[ "${WIPE_VOLUMES:-0}" == "1" ]]; then
  LOG "WIPE_VOLUMES=1 — clearing ~/.cairn/volumes/counter-data content"
  rm -rf "${HOME}/.cairn/volumes/counter-data" 2>/dev/null || true
  mkdir -p "${HOME}/.cairn/volumes/counter-data"
fi

LOG "done (events/history retained in SQLite unless you delete ~/.cairn/cairn.db)"
exit 0
