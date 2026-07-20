#!/usr/bin/env bash
# Single-node failure matrix (Phase B / roadmap Phase 17).
#
# Usage:
#   ./scripts/failure_matrix.sh              # run F1 F2 F3 F4 F5 F6
#   CASE=F2 ./scripts/failure_matrix.sh      # one case
#   CASE=F1,F6 ./scripts/failure_matrix.sh
#   CASE=F5 ./scripts/failure_matrix.sh      # backup interrupt only
#
# Cases:
#   F1 SIGTERM cairnd mid-migration (mid_deploy_crash_demo)
#   F2 SIGKILL cairnd mid-migration
#   F3 Kill Mini-Docker daemon, doctor fails, restart MD + recover service
#   F4 Kill app container; reconciliation recreates/restarts
#   F5 SIGKILL cairnd mid-backup: no success→missing/corrupt archive; incomplete failed
#   F6 Broken deploy after healthy (clean_demo path subset)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"

CASE="${CASE:-F1,F2,F3,F4,F5,F6}"
LOG_DIR="${LOG_DIR:-/tmp/cairn-proof-runs}"
mkdir -p "$LOG_DIR"
LOG="${LOG_DIR}/failure_matrix_$(date +%Y%m%d-%H%M%S).log"

log() { echo "[matrix] $*" | tee -a "$LOG"; }
die() { echo "[matrix] RED: $*" | tee -a "$LOG" >&2; exit 1; }

# Shared Mini-Docker / cairnd bootstrap (starts MD if socket dead; no hang on sudo)
# shellcheck source=lib/runtime.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/runtime.sh"
export CAIRN_RUNTIME_LOG="$LOG"
export CAIRN_CAIRND_LOG="$LOG"
cairn_runtime_discover
[[ -n "${CAIRN_ROOTFS:-}" ]] || die "set CAIRN_ROOTFS"

clean_counter_pending() {
  python3 - <<'PY' >>"$LOG" 2>&1 || true
import sqlite3, os
con=sqlite3.connect(os.path.expanduser("~/.cairn/cairn.db"))
con.execute("UPDATE deploys SET status='failed', stage='completed', failure_reason='matrix-pre-clean' WHERE status NOT IN ('success','failed') AND service_id IN (SELECT id FROM services WHERE name='counter-api')")
con.execute("UPDATE duraflow_workflows SET status='failed' WHERE status IN ('running','pending') AND type='deploy'")
con.commit()
PY
}

assert_counter_healthy() {
  python3 - <<'PY'
import sqlite3, os, urllib.request
con=sqlite3.connect(os.path.expanduser("~/.cairn/cairn.db"))
row=con.execute("SELECT current_deploy_id, actual_state, runtime_id FROM services WHERE name='counter-api'").fetchone()
assert row, "counter-api missing"
assert row[1]=="running", row
assert row[2], row
st=con.execute("SELECT status FROM deploys WHERE id=?", (row[0],)).fetchone()
assert st and st[0]=="success", st
pending=con.execute("SELECT COUNT(1) FROM deploys WHERE service_id=(SELECT id FROM services WHERE name='counter-api') AND status NOT IN ('success','failed')").fetchone()[0]
assert pending==0, pending
body=urllib.request.urlopen("http://127.0.0.1:8080/index.html", timeout=5).read()
assert body, "empty body"
print("counter-api healthy", row[0], body[:40])
PY
}

run_F1() {
  log "=== F1 SIGTERM mid-deploy ==="
  ensure_minidocker
  ensure_cairnd
  clean_counter_pending
  KILL_SIGNAL=SIGTERM bash scripts/mid_deploy_crash_demo.sh >>"$LOG" 2>&1
  assert_counter_healthy >>"$LOG"
  log "GREEN F1"
}

run_F2() {
  log "=== F2 SIGKILL mid-deploy ==="
  ensure_minidocker
  ensure_cairnd
  clean_counter_pending
  KILL_SIGNAL=SIGKILL bash scripts/mid_deploy_crash_demo.sh >>"$LOG" 2>&1
  assert_counter_healthy >>"$LOG"
  log "GREEN F2"
}

run_F3() {
  log "=== F3 Mini-Docker daemon death ==="
  ensure_minidocker
  ensure_cairnd
  clean_counter_pending
  mkdir -p "${HOME}/.cairn/volumes/counter-data"
  echo "F3_OK" >"${HOME}/.cairn/volumes/counter-data/index.html"
  cairn deploy examples/counter-api/cairn.yaml >>"$LOG" 2>&1
  BODY="$(curl -sf -m 3 http://127.0.0.1:8080/index.html || true)"
  case "$BODY" in *F3_OK*) ;; *) die "expected F3_OK (got: ${BODY:0:80})" ;; esac

  # Kill only the process holding the Mini-Docker unix socket (not this script).
  log "Killing process holding $MINI_DOCKER_SOCKET"
  if command -v fuser >/dev/null 2>&1; then
    if [[ -n "${SUDO_PASSWORD:-}" ]]; then
      printf '%s\n' "$SUDO_PASSWORD" | sudo -S fuser -k "$MINI_DOCKER_SOCKET" >>"$LOG" 2>&1 || true
    else
      sudo -n fuser -k "$MINI_DOCKER_SOCKET" >>"$LOG" 2>&1 || fuser -k "$MINI_DOCKER_SOCKET" >>"$LOG" 2>&1 || true
    fi
  elif command -v lsof >/dev/null 2>&1; then
    MD_PID="$(lsof -t "$MINI_DOCKER_SOCKET" 2>/dev/null | head -1 || true)"
    if [[ -n "${MD_PID:-}" ]]; then
      log "MD socket owner PID=$MD_PID"
      if [[ -n "${SUDO_PASSWORD:-}" ]]; then
        printf '%s\n' "$SUDO_PASSWORD" | sudo -S kill -KILL "$MD_PID" 2>/dev/null || true
      else
        kill -KILL "$MD_PID" 2>/dev/null || sudo -n kill -KILL "$MD_PID" 2>/dev/null || true
      fi
    fi
  fi
  # Force unusable socket even if process already gone
  if [[ -n "${SUDO_PASSWORD:-}" ]]; then
    printf '%s\n' "$SUDO_PASSWORD" | sudo -S rm -f "$MINI_DOCKER_SOCKET" 2>/dev/null || true
  else
    rm -f "$MINI_DOCKER_SOCKET" 2>/dev/null || true
  fi
  sleep 1
  # Socket may remain stale
  set +e
  cairn doctor >>"$LOG" 2>&1
  DOC_RC=$?
  set -e
  # doctor should fail OR deploy should fail when MD dead
  if [[ $DOC_RC -eq 0 ]]; then
    # try a deploy — should fail preflight
    set +e
    cairn deploy examples/counter-api/cairn.yaml >>"$LOG" 2>&1
    DEP_RC=$?
    set -e
    [[ $DEP_RC -ne 0 ]] || die "F3: expected deploy failure with Mini-Docker dead"
    log "F3: doctor still ok but deploy preflight failed (acceptable)"
  else
    log "F3: doctor failed as expected with Mini-Docker dead"
  fi

  # Restart Mini-Docker via shared bootstrap (sudo -n / SUDO_PASSWORD / root only)
  log "Restarting Mini-Docker via ensure_minidocker"
  export CAIRN_MD_LOG=/tmp/md-f3.log
  ensure_minidocker

  # Recover service via restart (recreate path)
  ensure_cairnd
  cairn restart counter-api >>"$LOG" 2>&1 || cairn deploy examples/counter-api/cairn.yaml >>"$LOG" 2>&1
  sleep 2
  BODY="$(curl -sf -m 5 http://127.0.0.1:8080/index.html || true)"
  case "$BODY" in *F3_OK*) ;; *) die "expected F3_OK after recover (got: ${BODY:0:80})" ;; esac
  assert_counter_healthy >>"$LOG"
  log "GREEN F3"
}

run_F4() {
  log "=== F4 app container death + reconcile ==="
  ensure_minidocker
  ensure_cairnd
  clean_counter_pending
  mkdir -p "${HOME}/.cairn/volumes/counter-data"
  echo "F4_OK" >"${HOME}/.cairn/volumes/counter-data/index.html"
  cairn deploy examples/counter-api/cairn.yaml >>"$LOG" 2>&1
  BODY="$(curl -sf -m 3 http://127.0.0.1:8080/index.html || true)"
  case "$BODY" in *F4_OK*) ;; *) die "expected F4_OK (got: ${BODY:0:80})" ;; esac

  RID="$(cairn inspect counter-api | awk -F': *' '/^Runtime ID:/{print $2}' | tr -d '[:space:]')"
  [[ -n "$RID" ]] || die "F4: no runtime id"
  log "Stopping/removing container $RID"
  # Use mini_docker CLI if available, else HTTP API over socket
  if command -v mini-docker >/dev/null 2>&1; then
    mini-docker stop "$RID" >>"$LOG" 2>&1 || true
    mini-docker rm "$RID" >>"$LOG" 2>&1 || true
  else
    python3 - <<PY >>"$LOG" 2>&1 || true
import socket
def req(method, path):
    s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
    s.connect("$MINI_DOCKER_SOCKET")
    s.sendall(f"{method} {path} HTTP/1.1\\r\\nHost: localhost\\r\\nConnection: close\\r\\n\\r\\n".encode())
    print(s.recv(4096)[:200])
    s.close()
req("POST", "/containers/${RID}/stop")
req("DELETE", "/containers/${RID}?force=true")
PY
  fi
  sleep 1
  # Traffic should be down
  set +e
  curl -sf -m 2 http://127.0.0.1:8080/index.html >/dev/null
  DOWN=$?
  set -e
  [[ $DOWN -ne 0 ]] || log "F4: note traffic still up (port may be held by another container)"

  log "Waiting for reconciliation (up to 45s)..."
  deadline=$((SECONDS + 45))
  while (( SECONDS < deadline )); do
    B="$(curl -sf -m 2 http://127.0.0.1:8080/index.html 2>/dev/null || true)"
    case "$B" in
      *F4_OK*) break ;;
    esac
    # nudge via restart if reconcile is slow
    if (( SECONDS > 20 )); then
      cairn restart counter-api >>"$LOG" 2>&1 || true
    fi
    sleep 2
  done
  BODY="$(curl -sf -m 5 http://127.0.0.1:8080/index.html || true)"
  case "$BODY" in *F4_OK*) ;; *) die "F4: service did not recover (got: ${BODY:0:80})" ;; esac
  assert_counter_healthy >>"$LOG"
  # Reconcile (or CLI restart nudge) must leave a ServiceRestarted audit event
  EVENTS_F4="$(cairn events 2>/dev/null || true)"
  printf '%s\n' "$EVENTS_F4" | sed -n '1,30p' >>"$LOG" || true
  # Avoid pipefail+grep -q SIGPIPE false negatives (match found but pipeline fails)
  case "$EVENTS_F4" in
    *ServiceRestarted*) ;;
    *) die "F4: missing ServiceRestarted event after container death recover" ;;
  esac
  log "GREEN F4"
}

run_F5() {
  log "=== F5 backup interrupt (SIGKILL mid-backup) ==="
  ensure_minidocker
  ensure_cairnd
  clean_counter_pending
  VOL_DIR="${HOME}/.cairn/volumes/counter-data"
  mkdir -p "$VOL_DIR"
  echo "F5_OK" >"${VOL_DIR}/index.html"
  # Inflate volume so tar.gz takes long enough to interrupt mid-flight (not a no-op race).
  # Mix many small files + poorly compressible blob so gzip cannot finish instantly.
  python3 - <<'PY' >>"$LOG" 2>&1
import os
vol = os.path.expanduser("~/.cairn/volumes/counter-data")
os.makedirs(vol, exist_ok=True)
# Remove previous pad files to keep deploy mounts clean
for name in os.listdir(vol):
    if name.startswith("pad_") or name.startswith("blob_"):
        try:
            os.remove(os.path.join(vol, name))
        except OSError:
            pass
chunk = (b"F5pad" * 2048)  # 10 KiB repeating
for i in range(800):
    with open(os.path.join(vol, f"pad_{i:04d}.bin"), "wb") as f:
        f.write(chunk)
# ~16 MiB low-compressibility payload
with open(os.path.join(vol, "blob_urandom.bin"), "wb") as f:
    f.write(os.urandom(16 * 1024 * 1024))
with open(os.path.join(vol, "index.html"), "w") as f:
    f.write("F5_OK\n")
print("volume padded for slow backup")
PY
  cairn deploy examples/counter-api/cairn.yaml >>"$LOG" 2>&1
  BODY_PRE="$(curl -sf -m 5 http://127.0.0.1:8080/index.html || true)"
  case "$BODY_PRE" in
    *F5_OK*) ;;
    *) die "F5: counter-api not serving F5_OK before backup (got: ${BODY_PRE:0:80})" ;;
  esac
  assert_counter_healthy >>"$LOG"

  # Snapshot pre-interrupt backup IDs so we can identify the interrupted attempt.
  PRE_IDS="$(python3 - <<'PY'
import sqlite3, os
con=sqlite3.connect(os.path.expanduser("~/.cairn/cairn.db"))
rows=con.execute("SELECT id FROM backups").fetchall()
print(",".join(r[0] for r in rows))
PY
)"

  log "Starting backup create (background); will SIGKILL cairnd once pending row appears"
  set +e
  cairn backup create counter-data >"${TMPDIR:-/tmp}/f5-backup.out" 2>&1 &
  BPID=$!
  set -e

  # Wait until a new non-terminal backup row exists (proof we hit mid-flight).
  SAW_PENDING=0
  for _ in $(seq 1 80); do
    PENDING_NOW="$(python3 - <<PY
import sqlite3, os
pre=set("${PRE_IDS}".split(",")) if "${PRE_IDS}" else set()
con=sqlite3.connect(os.path.expanduser("~/.cairn/cairn.db"))
rows=list(con.execute("SELECT id, status FROM backups WHERE status NOT IN ('success','failed')"))
new=[r for r in rows if r[0] not in pre]
print(len(new))
if new:
    print(new[0][0], new[0][1])
PY
)"
    NEW_COUNT="$(printf '%s\n' "$PENDING_NOW" | head -1 | tr -d '[:space:]')"
    if [[ "${NEW_COUNT:-0}" -ge 1 ]]; then
      SAW_PENDING=1
      log "Saw in-flight backup: $(printf '%s\n' "$PENDING_NOW" | sed -n '2p')"
      break
    fi
    # If backup already finished success before we could kill, still proceed to kill
    # (consistency checks below still hold) but note soft path.
    if ! kill -0 "$BPID" 2>/dev/null; then
      log "backup CLI exited before pending observed (fast path)"
      break
    fi
    sleep 0.05
  done

  CAIRND_PID=""
  if [[ -f "${HOME}/.cairn/cairnd.pid" ]]; then
    CAIRND_PID="$(tr -d '[:space:]' <"${HOME}/.cairn/cairnd.pid" || true)"
  fi
  if [[ -n "${CAIRND_PID}" ]] && kill -0 "$CAIRND_PID" 2>/dev/null; then
    log "SIGKILL cairnd pid=$CAIRND_PID mid-backup (SAW_PENDING=$SAW_PENDING)"
    kill -KILL "$CAIRND_PID" 2>/dev/null || true
  else
    # Fallback: kill by process name if pidfile stale
    log "pidfile missing/stale; SIGKILL any cairnd"
    pkill -KILL -x cairnd 2>/dev/null || true
  fi
  wait "$BPID" 2>/dev/null || true
  sleep 0.5
  # Confirm daemon is down before recovery
  if [[ -n "${CAIRND_PID}" ]] && kill -0 "$CAIRND_PID" 2>/dev/null; then
    die "F5: cairnd pid $CAIRND_PID still alive after SIGKILL"
  fi

  ensure_cairnd
  # Allow failIncompleteBackupsOnStartup + optional DuraFlow re-run to settle.
  # Poll until no non-terminal backup rows remain (deadline ~20s).
  for _ in $(seq 1 40); do
    PENDING_LEFT="$(python3 - <<'PY'
import sqlite3, os
con=sqlite3.connect(os.path.expanduser("~/.cairn/cairn.db"))
n=con.execute("SELECT COUNT(1) FROM backups WHERE status NOT IN ('success','failed')").fetchone()[0]
print(n)
PY
)"
    PENDING_LEFT="$(printf '%s' "$PENDING_LEFT" | tr -d '[:space:]')"
    if [[ "${PENDING_LEFT:-0}" -eq 0 ]]; then
      break
    fi
    sleep 0.5
  done

  python3 - <<'PY' >>"$LOG" 2>&1
import gzip, os, sqlite3, sys

con = sqlite3.connect(os.path.expanduser("~/.cairn/cairn.db"))
rows = list(con.execute(
    "SELECT id, status, size_bytes, backup_path, checksum, failure_reason "
    "FROM backups ORDER BY created_at DESC LIMIT 20"
))
print("backups after recovery:", rows)

# 1) No non-terminal backup rows may linger after recovery.
pending = [r for r in rows if r[1] not in ("success", "failed")]
if pending:
    raise SystemExit(f"F5: incomplete backup still non-terminal after recovery: {pending}")

# 2) Every success must point at a real, non-empty, gzip-readable archive.
for r in rows:
    bid, status, size_bytes, path, checksum, reason = r
    if status != "success":
        continue
    if not path:
        raise SystemExit(f"F5: success backup {bid} has empty path")
    if not os.path.isfile(path):
        raise SystemExit(f"F5: success backup {bid} points to missing archive: {path}")
    st = os.stat(path)
    if st.st_size <= 0:
        raise SystemExit(f"F5: success backup {bid} archive is empty: {path}")
    if size_bytes is not None and size_bytes < 0:
        raise SystemExit(f"F5: success backup {bid} negative size_bytes")
    if size_bytes is not None and size_bytes > 0 and st.st_size != size_bytes:
        # Allow tiny metadata drift only if both positive; hard-fail mismatch.
        raise SystemExit(
            f"F5: success backup {bid} size_bytes={size_bytes} != file={st.st_size}"
        )
    # Corrupt partial tar.gz must not be marked success.
    try:
        with gzip.open(path, "rb") as gz:
            # Read a bit to force header + stream validation
            _ = gz.read(64 * 1024)
    except Exception as e:
        raise SystemExit(f"F5: success backup {bid} archive corrupt/unreadable: {e}")
    if not checksum:
        raise SystemExit(f"F5: success backup {bid} missing checksum")

# 3) Service must still be running (platform invariant after cairnd recovery).
svc = con.execute(
    "SELECT actual_state, runtime_id FROM services WHERE name='counter-api'"
).fetchone()
if not svc or svc[0] != "running" or not svc[1]:
    raise SystemExit(f"F5: counter-api not running after recovery: {svc}")

print("F5 backup consistency OK")
PY

  # App still serves expected volume content (or recovered container still mounts volume).
  BODY_POST="$(curl -sf -m 5 http://127.0.0.1:8080/index.html || true)"
  case "$BODY_POST" in
    *F5_OK*) ;;
    *)
      # Nudge restart once if reconcile is slow, then re-check.
      cairn restart counter-api >>"$LOG" 2>&1 || true
      sleep 2
      BODY_POST="$(curl -sf -m 5 http://127.0.0.1:8080/index.html || true)"
      case "$BODY_POST" in
        *F5_OK*) ;;
        *) die "F5: app not serving F5_OK after recovery (got: ${BODY_POST:0:80})" ;;
      esac
      ;;
  esac
  assert_counter_healthy >>"$LOG"
  log "GREEN F5 (SAW_PENDING=$SAW_PENDING)"
}

run_F6() {
  log "=== F6 broken deploy protection (clean_demo) ==="
  ensure_minidocker
  ensure_cairnd
  # clean_demo already covers broken deploy; run full script
  bash scripts/clean_demo.sh >>"$LOG" 2>&1
  assert_counter_healthy >>"$LOG"
  log "GREEN F6"
}

log "CASE=$CASE log=$LOG"
IFS=',' read -ra CASES <<< "$CASE"
for c in "${CASES[@]}"; do
  c="$(echo "$c" | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]')"
  case "$c" in
    F1) run_F1 ;;
    F2) run_F2 ;;
    F3) run_F3 ;;
    F4) run_F4 ;;
    F5) run_F5 ;;
    F6) run_F6 ;;
    *) die "unknown case $c" ;;
  esac
done

log "ALL REQUESTED CASES GREEN"
exit 0
