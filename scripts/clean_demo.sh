#!/usr/bin/env bash
# Portable cold-start demo for Cairn (no hardcoded /home/<user>/Desktop paths).
# Steps: Mini-Docker → cairnd → deploy counter-api → restart → backup →
#        broken deploy (must fail) → restore → dashboard check.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRATCH="${CAIRN_DEMO_SCRATCH:-}"
LOG() { echo "[demo] $*"; }
die() { echo "[demo] ERROR: $*" >&2; exit 1; }
assert_http_body() {
  local needle="$1"
  local body
  body="$(curl -sf -m 5 http://127.0.0.1:8080/index.html || true)"
  case "$body" in
    *"$needle"*) ;;
    *) die "expected body to contain $needle (got: ${body:0:80})" ;;
  esac
}

# --- discover tools ---
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"
command -v cairn >/dev/null || die "cairn binary not found (build: make build or go build -o bin/cairn ./cmd/cairn)"
command -v cairnd >/dev/null || die "cairnd binary not found"

# Shared Mini-Docker / cairnd bootstrap
# shellcheck source=lib/runtime.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/runtime.sh"
cairn_runtime_discover
[[ -n "${CAIRN_ROOTFS:-}" && -d "$CAIRN_ROOTFS" ]] || die "Set CAIRN_ROOTFS to a Mini-Docker rootfs (bin/busybox required)"
LOG "CAIRN_ROOTFS=$CAIRN_ROOTFS"
LOG "MINI_DOCKER_SOCKET=$MINI_DOCKER_SOCKET"

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
ensure_minidocker
ensure_cairnd

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
assert_http_body STATE_OK
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
assert_http_body STATE_OK

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
assert_http_body STATE_OK
LOG "restore: OK"

LOG "Dashboard"
DASH_HTML="$(curl -sf -m 5 -L http://127.0.0.1:2476/dashboard/ | head -c 4000)"
echo "$DASH_HTML" | grep -qi 'Cairn' || die "dashboard missing Cairn title/content"
LOG "dashboard: OK"

LOG "Event story (MLP §17 subset)"
# Global timeline (backup/restore attach volume_id, not service_id)
EVENTS_OUT="$(cairn events 2>/dev/null || true)"
# Display only (never fail demo on truncation/SIGPIPE)
printf '%s\n' "$EVENTS_OUT" | sed -n '1,50p' || true

has_event() {
  # Bash substring match — avoids pipefail/grep quirks on large timelines
  case "$EVENTS_OUT" in
    *"$1"*) return 0 ;;
    *) return 1 ;;
  esac
}

# Explicit checks (die if missing) — full counter-api cycle + broken deploy
for need in \
  DeployStarted RuntimeCreateStarted RuntimeCreateCompleted \
  HealthCheckPassed RouteUpdated DeploySucceeded \
  DeployFailed RoutePreserved \
  BackupStarted BackupSucceeded \
  RestoreStarted; do
  has_event "$need" || die "missing event type in timeline: $need"
done
if ! has_event RestoreCompleted && ! has_event BackupRestored; then
  die "missing restore completion event"
fi
# Optional MLP aliases (emitted alongside canonical names)
has_event DeployCompleted && LOG "alias DeployCompleted: present"
has_event BackupCompleted && LOG "alias BackupCompleted: present"
LOG "event story: OK"

LOG ""
LOG "=== CLEAN DEMO PASSED ==="
LOG "Dashboard: http://127.0.0.1:2476/dashboard/"
LOG "Service:   http://127.0.0.1:8080/"
exit 0
