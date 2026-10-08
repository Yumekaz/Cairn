#!/usr/bin/env bash
# Runs ONLY inside the named disposable VM, never on the user's host.
set -euo pipefail
[[ "$(hostname)" == cairn-acceptance-vm ]] || { echo 'Refusing to run outside the acceptance VM' >&2; exit 2; }
exec > >(tee -a /home/ubuntu/cairn-vm-install.log) 2>&1
sudo cloud-init status --wait
# A interrupted first boot can mark a cloud-init package module as visited
# before every requested package is installed. Verify/install the actual
# runtime prerequisites rather than treating "status: done" as proof.
sudo apt-get install --no-install-recommends -y python3-venv python3-pip iproute2 iptables
sudo mkdir -p /opt/cairn-stack
sudo tar -xf /home/ubuntu/source.tar -C /opt/cairn-stack
sudo tar -xzf /home/ubuntu/go-toolchain.tgz -C /opt
sudo chown -R ubuntu:ubuntu /opt/cairn-stack
export PATH="/opt/go/bin:$PATH"
cd /opt/cairn-stack/SERVER
if [[ "${CAIRN_VM_INSTALL_MODE:-source}" == verified-binaries ]]; then
  python3 - <<'PY'
import hashlib,json
from pathlib import Path
expected=json.loads(Path('bin/binary-hashes.json').read_text())
for name,digest in expected.items():
    assert name in {'cairn','cairnd'}
    assert hashlib.file_digest(Path('bin',name).open('rb'),'sha256').hexdigest()==digest
print('Verified installation binary checksums; clean source compilation is a separate CI gate')
PY
  mkdir -p /home/ubuntu/.local/bin
  install -m 755 bin/cairn bin/cairnd /home/ubuntu/.local/bin/
else
  bash scripts/install.sh
fi
sudo /usr/bin/python3 -m venv /opt/cairn-stack/Mini-Docker/venv
sudo /opt/cairn-stack/Mini-Docker/venv/bin/pip install --no-deps /opt/cairn-stack/Mini-Docker
mkdir -p /opt/cairn-stack/Mini-Docker/rootfs/{bin,dev,proc,sys,tmp,etc}
for command in sh echo cat dd httpd mkdir rmdir sync sleep printf; do
  ln -sf busybox "/opt/cairn-stack/Mini-Docker/rootfs/bin/$command"
done
mkdir -p /home/ubuntu/.cairn
install -m 600 scripts/vm/cairnd-config.yaml /home/ubuntu/.cairn/cairnd-config.yaml
sudo install -m 644 scripts/vm/mini-docker.service /etc/systemd/system/cairn-proof-runtime.service
sudo install -m 644 scripts/vm/cairn.service /etc/systemd/system/cairn-proof-daemon.service
sudo systemctl daemon-reload
sudo systemctl enable --now cairn-proof-runtime.service cairn-proof-daemon.service
for attempt in $(seq 1 60); do
  if /home/ubuntu/.local/bin/cairn doctor; then break; fi
  sleep 1
done
/home/ubuntu/.local/bin/cairn doctor
NOTES_PROOF_PORT=8089 python3 scripts/notes_smoke.py
