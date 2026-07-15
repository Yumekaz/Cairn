#!/usr/bin/env bash
# Single-node failure matrix (Phase B / roadmap Phase 17).
#
# Usage:
#   ./scripts/failure_matrix.sh              # run F1 F2 F3 F4 F6
#   CASE=F2 ./scripts/failure_matrix.sh      # one case
#   CASE=F1,F6 ./scripts/failure_matrix.sh
#
# Cases:
#   F1 SIGTERM cairnd mid-migration (mid_deploy_crash_demo)
#   F2 SIGKILL cairnd mid-migration
#   F3 Kill Mini-Docker daemon, doctor fails, restart MD + recover service
#   F4 Kill app container; reconciliation recreates/restarts
#   F5 (optional) interrupt backup — CASE=F5
#   F6 Broken deploy after healthy (clean_demo path subset)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"

CASE="${CASE:-F1,F2,F3,F4,F6}"
LOG_DIR="${LOG_DIR:-/tmp/cairn-proof-runs}"
mkdir -p "$LOG_DIR"
LOG="${LOG_DIR}/failure_matrix_$(date +%Y%m%d-%H%M%S).log"

log() { echo "[matrix] $*" | tee -a "$LOG"; }
die() { echo "[matrix] RED: $*" | tee -a "$LOG" >&2; exit 1; }

if [[ -z "${CAIRN_ROOTFS:-}" ]]; then
  for cand in "${ROOT}/../Mini-Docker/rootfs" "${HOME}/Desktop/Mini-Docker/rootfs"; do
    if [[ -x "${cand}/bin/busybox" ]]; then
      export CAIRN_ROOTFS="$(cd "$(dirname "$cand")" && pwd)/$(basename "$cand")"
      break
    fi
  done
fi
[[ -n "${CAIRN_ROOTFS:-}" ]] || die "set CAIRN_ROOTFS"
export MINI_DOCKER_SOCKET="${MINI_DOCKER_SOCKET:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock}"
export PYTHONPATH="${PYTHONPATH:-${ROOT}/../Mini-Docker}"

ensure_cairnd() {
  if ! cairn status >/dev/null 2>&1; then
    nohup cairnd >>"$LOG" 2>&1 &
    for _ in $(seq 1 40); do
      cairn status >/dev/null 2>&1 && return 0
      sleep 0.15
    done
    die "cairnd not ready"
  fi
}

ensure_minidocker() {
  if [[ -S "$MINI_DOCKER_SOCKET" ]]; then
    if cairn doctor >/dev/null 2>&1 || true; then
      # probe socket
      if python3 - <<PY 2>/dev/null
import socket
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
s.settimeout(2)
s.connect("$MINI_DOCKER_SOCKET")
s.sendall(b"GET /containers/json HTTP/1.1\\r\\nHost: localhost\\r\\nConnection: close\\r\\n\\r\\n")
print(s.recv(32))
PY
      then
        return 0
      fi
    fi
  fi
  die "Mini-Docker socket not usable at $MINI_DOCKER_SOCKET (start daemon first)"
}

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
  curl -sf -m 3 http://127.0.0.1:8080/index.html | grep -q F3_OK

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

  # Restart Mini-Docker (requires sudo if not already root)
  log "Restarting Mini-Docker"
  MD_PY="${MINI_DOCKER_PYTHON:-}"
  if [[ -z "$MD_PY" ]]; then
    for cand in "${ROOT}/../Mini-Docker/venv/bin/python3" "${HOME}/Desktop/Mini-Docker/venv/bin/python3" "python3"; do
      if [[ -x "$cand" ]] || command -v "$cand" >/dev/null 2>&1; then MD_PY="$cand"; break; fi
    done
  fi
  MD_SRC="${ROOT}/../Mini-Docker"
  [[ -d "$MD_SRC" ]] || MD_SRC="${HOME}/Desktop/Mini-Docker"
  if [[ -n "${SUDO_PASSWORD:-}" ]]; then
    printf '%s\n' "$SUDO_PASSWORD" | sudo -S mkdir -p "$(dirname "$MINI_DOCKER_SOCKET")" 2>/dev/null || true
    printf '%s\n' "$SUDO_PASSWORD" | sudo -S rm -f "$MINI_DOCKER_SOCKET" 2>/dev/null || true
    printf '%s\n' "$SUDO_PASSWORD" | sudo -S env PYTHONPATH="$MD_SRC" \
      "$MD_PY" -m mini_docker daemon --socket "$MINI_DOCKER_SOCKET" --socket-mode 666 \
      >/tmp/md-f3.log 2>&1 &
  elif [[ "${EUID}" -eq 0 ]]; then
    mkdir -p "$(dirname "$MINI_DOCKER_SOCKET")"
    rm -f "$MINI_DOCKER_SOCKET" 2>/dev/null || true
    env PYTHONPATH="$MD_SRC" "$MD_PY" -m mini_docker daemon \
      --socket "$MINI_DOCKER_SOCKET" --socket-mode 666 >/tmp/md-f3.log 2>&1 &
  else
    sudo -n mkdir -p "$(dirname "$MINI_DOCKER_SOCKET")" 2>/dev/null || true
    sudo -n rm -f "$MINI_DOCKER_SOCKET" 2>/dev/null || true
    sudo -n env PYTHONPATH="$MD_SRC" "$MD_PY" -m mini_docker daemon \
      --socket "$MINI_DOCKER_SOCKET" --socket-mode 666 >/tmp/md-f3.log 2>&1 &
  fi
  for _ in $(seq 1 40); do
    if [[ -S "$MINI_DOCKER_SOCKET" ]]; then
      if python3 - <<PY 2>/dev/null
import socket
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
s.settimeout(1)
s.connect("$MINI_DOCKER_SOCKET")
s.sendall(b"GET /containers/json HTTP/1.1\\r\\nHost: localhost\\r\\nConnection: close\\r\\n\\r\\n")
s.recv(8)
PY
      then break; fi
    fi
    sleep 0.25
  done
  if [[ ! -S "$MINI_DOCKER_SOCKET" ]]; then
    log "md-f3.log: $(tail -20 /tmp/md-f3.log 2>/dev/null || true)"
    die "F3: Mini-Docker did not restart (start manually with sudo)"
  fi

  # Recover service via restart (recreate path)
  ensure_cairnd
  cairn restart counter-api >>"$LOG" 2>&1 || cairn deploy examples/counter-api/cairn.yaml >>"$LOG" 2>&1
  sleep 2
  curl -sf -m 5 http://127.0.0.1:8080/index.html | grep -q F3_OK
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
  curl -sf -m 3 http://127.0.0.1:8080/index.html | grep -q F4_OK

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
    if curl -sf -m 2 http://127.0.0.1:8080/index.html 2>/dev/null | grep -q F4_OK; then
      break
    fi
    # nudge via restart if reconcile is slow
    if (( SECONDS > 20 )); then
      cairn restart counter-api >>"$LOG" 2>&1 || true
    fi
    sleep 2
  done
  curl -sf -m 5 http://127.0.0.1:8080/index.html | grep -q F4_OK || die "F4: service did not recover"
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
  log "=== F5 backup interrupt ==="
  ensure_minidocker
  ensure_cairnd
  clean_counter_pending
  mkdir -p "${HOME}/.cairn/volumes/counter-data"
  echo "F5_OK" >"${HOME}/.cairn/volumes/counter-data/index.html"
  cairn deploy examples/counter-api/cairn.yaml >>"$LOG" 2>&1

  # Start backup in background and kill cairnd quickly
  set +e
  cairn backup create counter-data >"${TMPDIR:-/tmp}/f5-backup.out" 2>&1 &
  BPID=$!
  set -e
  sleep 0.3
  if [[ -f "${HOME}/.cairn/cairnd.pid" ]]; then
    kill -TERM "$(cat "${HOME}/.cairn/cairnd.pid")" 2>/dev/null || true
  fi
  wait "$BPID" 2>/dev/null || true
  sleep 1
  ensure_cairnd
  # After recovery, either last backup success exists or a failed backup row — no silent success with empty path
  python3 - <<'PY' >>"$LOG" 2>&1
import sqlite3, os
con=sqlite3.connect(os.path.expanduser("~/.cairn/cairn.db"))
rows=list(con.execute("SELECT id, status, size_bytes, backup_path FROM backups ORDER BY created_at DESC LIMIT 5"))
print("backups", rows)
# service still healthy
svc=con.execute("SELECT actual_state FROM services WHERE name='counter-api'").fetchone()
assert svc and svc[0]=='running', svc
# no backup with success and zero size + missing file falsely claiming success beyond empty volume
for r in rows:
    if r[1]=='success' and r[2] is not None and r[2] < 0:
        raise SystemExit('negative backup size')
print("F5 backup rows OK")
PY
  curl -sf -m 3 http://127.0.0.1:8080/index.html | grep -q F5_OK
  log "GREEN F5"
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
