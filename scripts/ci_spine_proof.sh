#!/usr/bin/env bash
# Only disposable runtime/state paths; does not use the runner's default Cairn home.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PARENT="$(cd "$ROOT/.." && pwd)"
mkdir -p "$ROOT/verification"
PROOF_STATE="$(mktemp -d "$ROOT/verification/state.XXXXXX")"
PROOF_SOCKETS="$(mktemp -d /tmp/ca-ci.XXXXXX)"
PROOF_RUNTIME_UNIT="cairn-ci-runtime-${GITHUB_RUN_ID:-$$}"
PROOF_DAEMON_UNIT="cairn-ci-daemon-${GITHUB_RUN_ID:-$$}"
PROOF_PYTHON="$(command -v python3)"
export CAIRN_ROOTFS="$PARENT/Mini-Docker/rootfs"
export CAIRN_SOCKET="$PROOF_SOCKETS/cairnd.sock"
PROOF_RUNTIME_SOCKET="$PROOF_SOCKETS/mini-docker.sock"
cleanup() {
  sudo systemctl stop "$PROOF_DAEMON_UNIT.service" "$PROOF_RUNTIME_UNIT.service" || true
  sudo journalctl -u "$PROOF_DAEMON_UNIT.service" -u "$PROOF_RUNTIME_UNIT.service" --no-pager > "$ROOT/verification/daemon-journal.log" || true
}
trap cleanup EXIT
mkdir -p "$PROOF_STATE/data" "$PROOF_STATE/volumes" "$PROOF_STATE/backups"
python3 - "$PROOF_STATE" "$CAIRN_SOCKET" "$PROOF_RUNTIME_SOCKET" <<'PY'
import json,sys
from pathlib import Path
root,socket,runtime=map(str,sys.argv[1:])
values={'socket_path':socket,'database_path':root+'/cairn.db','data_dir':root+'/data','volume_dir':root+'/volumes','backup_dir':root+'/backups','mini_docker_socket':runtime,'dashboard_addr':''}
Path(root+'/config.yaml').write_text('\n'.join(key+': '+json.dumps(value) for key,value in values.items())+'\n')
PY
sudo systemd-run --unit="$PROOF_RUNTIME_UNIT" --collect --property="Group=$(id -gn)" \
  --working-directory="$PARENT/Mini-Docker" --setenv="PYTHONPATH=$PARENT/Mini-Docker" \
  --setenv="MINI_DOCKER_ROOT=$PROOF_STATE/runtime-root" --setenv="MINI_DOCKER_RUN=$PROOF_STATE/runtime-run" \
  "$PROOF_PYTHON" -m mini_docker daemon --socket "$PROOF_RUNTIME_SOCKET" --socket-mode 660
for attempt in $(seq 1 30); do [[ -S "$PROOF_RUNTIME_SOCKET" ]] && break; sleep 1; done
test -S "$PROOF_RUNTIME_SOCKET"
sudo chown "$(id -u):$(id -g)" "$PROOF_RUNTIME_SOCKET"
sudo systemd-run --unit="$PROOF_DAEMON_UNIT" --collect --uid="$(id -u)" --gid="$(id -g)" \
  --working-directory="$ROOT" --setenv="CAIRN_ROOTFS=$CAIRN_ROOTFS" \
  "$ROOT/bin/cairnd" --config "$PROOF_STATE/config.yaml"
for attempt in $(seq 1 30); do
  if curl --fail --silent --unix-socket "$CAIRN_SOCKET" http://localhost/status > "$ROOT/verification/status.json"; then break; fi
  sleep 1
done
curl --fail --silent --unix-socket "$CAIRN_SOCKET" http://localhost/status > "$ROOT/verification/status.json"
NOTES_PROOF_PORT=18089 python3 "$ROOT/scripts/notes_smoke.py" > "$ROOT/verification/notes-proof.json"
python3 - "$ROOT/verification/notes-proof.json" <<'PY'
import json,sys
proof=json.load(open(sys.argv[1]))
assert proof['result']=='passed'
assert {'write','restart','redeploy','backup','restore'} <= set(proof['verified'])
PY
