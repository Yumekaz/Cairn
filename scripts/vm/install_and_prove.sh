#!/usr/bin/env bash
# Runs ONLY inside the named disposable VM, never on the user's host.
set -euo pipefail
[[ "$(hostname)" == cairn-acceptance-vm ]] || { echo 'Refusing to run outside the acceptance VM' >&2; exit 2; }
exec > >(tee -a /home/ubuntu/cairn-vm-install.log) 2>&1
sudo cloud-init status --wait
# A power/interruption during initial OS provisioning can leave dpkg partially
# configured even after cloud-init reports done. Repair only this named guest;
# never mistake an unfinished package transaction for an application failure.
sudo dpkg --configure -a
# A interrupted first boot can mark a cloud-init package module as visited
# before every requested package is installed. Verify/install the actual
# runtime prerequisites rather than treating "status: done" as proof.
sudo apt-get install --no-install-recommends -y python3-venv python3-pip iproute2 iptables
sudo mkdir -p /opt/cairn-stack
sudo tar -xf /home/ubuntu/source.tar -C /opt/cairn-stack
if [[ "${CAIRN_VM_INSTALL_MODE:-source}" == source ]]; then
  sudo tar -xzf /home/ubuntu/go-toolchain.tgz -C /opt
fi
sudo chown -R ubuntu:ubuntu /opt/cairn-stack
export PATH="/opt/go/bin:$PATH"
cd /opt/cairn-stack/SERVER
if [[ "${CAIRN_VM_INSTALL_MODE:-source}" == verified-binaries ]]; then
  python3 - <<'PY'
import hashlib,json
from pathlib import Path
expected=json.loads(Path('bin/binary-hashes.json').read_text())
for name,digest in expected.items():
    assert name in {'cairn','cairnd','cairn-integration-tests'}
    assert hashlib.file_digest(Path('bin',name).open('rb'),'sha256').hexdigest()==digest
print('Verified installation binary checksums; clean source compilation is a separate CI gate')
PY
  mkdir -p /home/ubuntu/.local/bin
  install -m 755 bin/cairn bin/cairnd /home/ubuntu/.local/bin/
else
  bash scripts/install.sh
fi
mkdir -p /opt/cairn-stack/Mini-Docker/rootfs/{bin,dev,proc,sys,tmp,etc}
for command in sh echo cat dd httpd mkdir rmdir sync sleep printf; do
  ln -sf busybox "/opt/cairn-stack/Mini-Docker/rootfs/bin/$command"
done
mkdir -p /home/ubuntu/.cairn
chmod 700 /home/ubuntu/.cairn
if [[ ! -f /home/ubuntu/.cairn/cairnd-config.yaml ]]; then
  install -m 600 scripts/vm/cairnd-config.yaml /home/ubuntu/.cairn/cairnd-config.yaml
fi
# Replace only the old, explicitly identified disposable-VM fixtures. The
# supported package installer must prove its actual root-owned units at boot,
# not another pair of source-tree acceptance units.
for legacy_unit in cairn-proof-daemon.service cairn-proof-runtime.service; do
  if systemctl cat "$legacy_unit" >/dev/null 2>&1; then
    legacy_description=$(systemctl show "$legacy_unit" -p Description --value)
    [[ "$legacy_description" == "Disposable Cairn acceptance VM "* ]] || { echo "Refusing unknown legacy unit: $legacy_unit" >&2; exit 2; }
    sudo systemctl stop "$legacy_unit"
    sudo systemctl disable "$legacy_unit"
  fi
done
bundle="/home/ubuntu/cairn-bundles/bundle-$(date +%s%N)"
if [[ "${CAIRN_VM_INSTALL_MODE:-source}" == verified-binaries ]]; then
  python3 scripts/system_install.py prepare --output "$bundle" --prebuilt-dir "$PWD/bin" --source-record "$PWD/bin/source-revisions.json"
else
  python3 -m venv /opt/cairn-stack/Mini-Docker/venv
  /opt/cairn-stack/Mini-Docker/venv/bin/pip install build setuptools wheel
  python3 scripts/system_install.py prepare --output "$bundle"
fi
bundle_digest=$(sha256sum "$bundle/manifest.json" | awk '{print $1}')
sudo python3 scripts/system_install.py apply --bundle "$bundle" --manifest-sha256 "$bundle_digest"
systemctl is-enabled cairn.service cairn-runtime.service
export MINI_DOCKER_SOCKET=/run/cairn-runtime/mini-docker.sock
for attempt in $(seq 1 60); do
  # Doctor intentionally allows an absent cairnd before installation. The
  # integration fixture needs its live API, not merely a usable runtime.
  if curl --fail --silent --unix-socket /home/ubuntu/.cairn/cairnd.sock http://localhost/status > /home/ubuntu/cairn-ready.json && /home/ubuntu/.local/bin/cairn doctor; then break; fi
  sleep 1
done
curl --fail --silent --unix-socket /home/ubuntu/.cairn/cairnd.sock http://localhost/status > /home/ubuntu/cairn-ready.json
/home/ubuntu/.local/bin/cairn doctor
python3 scripts/verify_installed_admin.py
if [[ "${CAIRN_VM_INTEGRATION_PROOF:-0}" == 1 ]]; then
  CAIRN_TEST_HOME=/home/ubuntu CAIRN_ROOTFS=/opt/cairn-stack/Mini-Docker/rootfs \
    bin/cairn-integration-tests -test.v -test.timeout 15m | tee /home/ubuntu/cairn-integration.log
  python3 scripts/verify_integration_log.py /home/ubuntu/cairn-integration.log
fi
NOTES_PROOF_PORT=8089 python3 scripts/notes_smoke.py | tee /home/ubuntu/cairn-notes-proof.json
proof_service=$(python3 -c 'import json; print(json.load(open("/home/ubuntu/cairn-notes-proof.json"))["service"])')
python3 scripts/verify_installed_recovery.py --service "$proof_service" --port 8089
