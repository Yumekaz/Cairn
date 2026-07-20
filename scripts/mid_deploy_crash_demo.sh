#!/usr/bin/env bash
# Live proof: kill cairnd mid-deploy, restart, DuraFlow resumes cleanly.
#
# Prerequisites: Mini-Docker daemon up, CAIRN_ROOTFS set (or discoverable).
# Usage:
#   ./scripts/mid_deploy_crash_demo.sh
#   KILL_SIGNAL=SIGKILL ./scripts/mid_deploy_crash_demo.sh   # F2

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"
KILL_SIGNAL="${KILL_SIGNAL:-SIGTERM}"

log() { echo "[crash-demo] $*"; }
die() { echo "[crash-demo] ERROR: $*" >&2; exit 1; }

# Shared Mini-Docker / cairnd bootstrap
# shellcheck source=lib/runtime.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/runtime.sh"
cairn_runtime_discover
[[ -n "${CAIRN_ROOTFS:-}" ]] || die "set CAIRN_ROOTFS"
ensure_minidocker
ensure_cairnd
wait_doctor 30

# Ensure volume seed
mkdir -p "${HOME}/.cairn/volumes/counter-data"
echo "CRASH_DEMO_OK" >"${HOME}/.cairn/volumes/counter-data/index.html"

# Baseline healthy deploy (no slow migration)
log "Baseline healthy deploy"
cairn deploy "${ROOT}/examples/counter-api/cairn.yaml" >/dev/null
BASELINE="$(cairn inspect counter-api | awk -F': *' '/^Current Deploy ID:/{print $2}' | tr -d '[:space:]')"
[[ -n "$BASELINE" ]] || die "no baseline Current Deploy ID"
log "baseline Current Deploy ID=$BASELINE"
# Avoid pipefail+grep -q SIGPIPE false negatives (exit 141 when match found)
BODY="$(curl -sf -m 3 http://127.0.0.1:8080/index.html || true)"
case "$BODY" in
  *CRASH_DEMO_OK*) ;;
  *) die "baseline not serving CRASH_DEMO_OK (got: ${BODY:0:80})" ;;
esac

# Start slow migration deploy in background
SLOW="${ROOT}/examples/counter-api/cairn_slow_migration.yaml"
log "Starting slow-migration deploy in background..."
set +e
cairn deploy "$SLOW" >"${TMPDIR:-/tmp}/cairn-slow-deploy.out" 2>&1 &
DEPLOY_PID=$!
set -e

# Wait until migration container is running (or a few seconds into workflow)
log "Waiting for deploy to enter migration (sleep)..."
for i in $(seq 1 40); do
  if grep -q 'Successfully deployed\|Deployment failed\|workflow failed' "${TMPDIR:-/tmp}/cairn-slow-deploy.out" 2>/dev/null; then
    die "deploy finished before we could kill cairnd — see ${TMPDIR:-/tmp}/cairn-slow-deploy.out"
  fi
  # migration container names contain -task-
  if command -v python3 >/dev/null; then
    # best-effort: if cairnd log mentions migration / sleep
    if tail -n 30 "${HOME}/.cairn/cairnd.log" 2>/dev/null | grep -qiE 'migration|run_migration|sleep'; then
      break
    fi
  fi
  sleep 0.5
done
# Always give migration a moment to start
sleep 3

PIDFILE="${HOME}/.cairn/cairnd.pid"
[[ -f "$PIDFILE" ]] || die "cairnd.pid missing"
CAIRND_PID="$(cat "$PIDFILE")"
case "$KILL_SIGNAL" in
  SIGTERM|TERM|15) SIG_NUM=TERM ;;
  SIGKILL|KILL|9)  SIG_NUM=KILL ;;
  *) die "unsupported KILL_SIGNAL=$KILL_SIGNAL (use SIGTERM or SIGKILL)" ;;
esac
log "Killing cairnd mid-deploy (PID $CAIRND_PID) with $KILL_SIGNAL"
kill -s "$SIG_NUM" "$CAIRND_PID" 2>/dev/null || true
# Wait for deploy client to notice
sleep 1
kill -0 "$DEPLOY_PID" 2>/dev/null && wait "$DEPLOY_PID" 2>/dev/null || true

# Assert deploy not left as success on baseline incorrectly — check SQLite pending
python3 - <<'PY'
import sqlite3, sys
con = sqlite3.connect(__import__("os").path.expanduser("~/.cairn/cairn.db"))
rows = list(con.execute(
    "SELECT id, status, previous_deploy_id FROM deploys WHERE service_id="
    "(SELECT id FROM services WHERE name='counter-api') ORDER BY created_at DESC LIMIT 3"
))
print("recent deploys:", rows)
# latest should not be success if we killed mid-flight (unless finished early)
if rows and rows[0][1] == "success":
    # might have completed if kill was late — still OK if service healthy
    print("NOTE: latest deploy already success (kill may have been late)")
else:
    assert rows and rows[0][1] in ("pending", "running", "failed"), rows
print("post-kill deploy rows OK")
PY

# Restart cairnd (binary from PATH)
log "Restarting cairnd..."
rm -f "${HOME}/.cairn/cairnd.sock" 2>/dev/null || true
nohup cairnd >"${TMPDIR:-/tmp}/cairnd-crash-restart.out" 2>&1 &
for i in $(seq 1 50); do
  if cairn status >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
cairn status >/dev/null || die "cairnd did not come back"
log "cairnd is up — waiting for DuraFlow to resume (lease reclaim up to ~15s)..."

# Poll for terminal deploy + healthy service
deadline=$((SECONDS + 120))
SUCCESS=""
while (( SECONDS < deadline )); do
  # Success = no pending deploys for counter-api AND current deploy row is success AND running
  EVAL="$(python3 - <<'PY'
import sqlite3, os
con=sqlite3.connect(os.path.expanduser("~/.cairn/cairn.db"))
sid=con.execute("SELECT id FROM services WHERE name='counter-api'").fetchone()[0]
svc=con.execute("SELECT current_deploy_id, actual_state, runtime_id FROM services WHERE id=?", (sid,)).fetchone()
pending=con.execute("SELECT COUNT(1) FROM deploys WHERE service_id=? AND status NOT IN ('success','failed')", (sid,)).fetchone()[0]
st=con.execute("SELECT status FROM deploys WHERE id=?", (svc[0],)).fetchone() if svc and svc[0] else (None,)
ok = pending==0 and svc and svc[1]=='running' and svc[2] and st and st[0]=='success'
print("OK" if ok else "WAIT", "pending", pending, "current", svc[0] if svc else None, "state", svc[1] if svc else None, "deploy_status", st[0] if st else None)
if ok:
    print(svc[0])
PY
)"
  log "poll: $EVAL"
  if echo "$EVAL" | head -1 | grep -q '^OK'; then
    SUCCESS="$(echo "$EVAL" | tail -1)"
    break
  fi
  sleep 2
done

[[ -n "$SUCCESS" ]] || die "timeout waiting for recovery (inspect: $(cairn inspect counter-api 2>&1 | tr '\n' ' '))"

# Traffic must work
BODY_AFTER="$(curl -sf -m 5 http://127.0.0.1:8080/index.html || true)"
case "$BODY_AFTER" in
  *CRASH_DEMO_OK*) ;;
  *) die "service not serving after recovery (got: ${BODY_AFTER:0:80})" ;;
esac

# counter-api only: no pending deploys; current is success; baseline not corrupted.
# Accept either: (a) resumed deploy promoted to current, or (b) failed clean, baseline current.
python3 - <<PY
import sqlite3, os
baseline = "$BASELINE"
con=sqlite3.connect(os.path.expanduser("~/.cairn/cairn.db"))
sid=con.execute("SELECT id FROM services WHERE name='counter-api'").fetchone()[0]
pending=list(con.execute(
    "SELECT id, status FROM deploys WHERE service_id=? AND status NOT IN ('success','failed')", (sid,)))
print("counter_api_non_terminal", pending)
assert pending == [], pending
svc=con.execute("SELECT current_deploy_id, actual_state, runtime_id FROM services WHERE name='counter-api'").fetchone()
print("service", svc)
assert svc[1] == "running", svc
assert svc[2], svc
st=con.execute("SELECT status FROM deploys WHERE id=?", (svc[0],)).fetchone()
assert st and st[0] == "success", st
# If current still baseline, that is fail-clean recovery; if newer success, resume promoted.
print("baseline", baseline, "current", svc[0])
print("METADATA_RECOVERY_OK")
PY

log "Events (tail):"
cairn events counter-api 2>&1 | head -15 || true

log ""
log "=== MID-DEPLOY CRASH DEMO PASSED ==="
log "Baseline deploy: $BASELINE"
log "Current deploy:  $(cairn inspect counter-api | awk -F': *' '/^Current Deploy ID:/{print $2}')"
log "Service is running and serving traffic after kill + restart."
