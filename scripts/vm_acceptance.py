#!/usr/bin/env python3
"""Fresh Linux installation, guest reboot and abrupt guest shutdown proof.
Uses a verified official image, dedicated 8GiB overlay, TCG, and localhost SSH.
Never reboots the host and never passes host account credentials to the guest.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import tarfile
import time

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--ssh-port", type=int, default=23222)
    parser.add_argument("--boot-timeout", type=int, default=1200)
    parser.add_argument("--install-timeout", type=int, default=3600)
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--install-mode", choices=['source','verified-binaries'], default='source')
    args = parser.parse_args()
    image = args.image.resolve()
    manifest = image.parent / "SHA256SUMS"
    expected = next(line.split()[0] for line in manifest.read_text().splitlines()
                    if line.split()[-1].lstrip("*") == image.name)
    digest = hashlib.file_digest(image.open("rb"), "sha256").hexdigest()
    if digest != expected:
        raise RuntimeError("official cloud-image checksum does not match")
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", args.ssh_port))
    if args.resume:
        previous=json.loads((args.output/'status.json').read_text())
        if previous.get('image_sha256')!=digest or previous.get('checks'):
            raise RuntimeError('resume is limited to this same image before any recovery gate passed')
        (args.output/'previous-status.json').write_text(json.dumps(previous,indent=2))
    else:
        args.output.mkdir(parents=True, exist_ok=False)
    out = args.output.resolve()
    status = {"state": "preparing", "image_sha256": digest, "started_unix": time.time(),
              "limits": {"disk_gib": 8, "ram_mib": 2048, "vcpus": 2}, "checks": {}}
    def save(phase):
        status["state"] = phase
        status["updated_unix"] = time.time()
        (out / "status.json").write_text(json.dumps(status, indent=2))
        print(phase, flush=True)
    def run(argv, timeout=120, log_path=None):
        if log_path:
            with log_path.open('a') as stream:
                result=subprocess.run(argv,stdout=stream,stderr=subprocess.STDOUT,text=True,timeout=timeout)
            if result.returncode:
                raise RuntimeError(f'{argv[0]} failed; retained output: {log_path}')
            return log_path.read_text()
        result = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
        if result.returncode:
            raise RuntimeError(f"{argv[0]} failed: {result.stderr[-3000:]}")
        return result.stdout
    if not args.resume:
        run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(out / "guest-key")])
    public_key = (out / "guest-key.pub").read_text().strip()
    user_data = (ROOT / "scripts/vm/user-data.yaml").read_text().replace("@SSH_KEY@", public_key)
    (out / "user-data").write_text(user_data)
    (out / "meta-data").write_text("instance-id: cairn-acceptance-vm\nlocal-hostname: cairn-acceptance-vm\n")
    if not args.resume:
        run(["cloud-localds", str(out / "seed.img"), str(out / "user-data"), str(out / "meta-data")])
        run(["qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b", str(image), str(out / "disk.qcow2"), "8G"])
    elif (out/'source.tar').exists():
        stamp=str(int(time.time()))
        (out/'source.tar').rename(out/f'source-before-resume-{stamp}.tar')
        (out/'revisions.json').rename(out/f'revisions-before-resume-{stamp}.json')
    revisions = {}
    if args.install_mode=='verified-binaries':
        for binary in ('cairn','cairnd'):
            run(['go','build','-o',str(ROOT/'bin'/binary),'./cmd/'+binary],timeout=180)
        wheel_dir=out/f'runtime-wheel-{int(time.time())}'
        run([str(ROOT.parent/'Mini-Docker/venv/bin/python'),'-m','build','--wheel','--no-isolation','--outdir',str(wheel_dir),str(ROOT.parent/'Mini-Docker')],timeout=180)
    with tarfile.open(out / "source.tar", "w") as archive:
        for repo in ("SERVER", "DURAFLOW", "Mini-Docker"):
            directory = ROOT.parent / repo
            files = subprocess.check_output(["git", "-C", str(directory), "ls-files", "-co", "--exclude-standard", "-z"]).decode().split("\0")
            revisions[repo] = {"commit": run(["git", "-C", str(directory), "rev-parse", "HEAD"]).strip(),
                               "working_tree": run(["git", "-C", str(directory), "status", "--short"])}
            for name in dict.fromkeys(files):
                if not name or name.startswith((".env", ".agents/", ".codex/")):
                    continue
                path = directory / name
                if path.is_file() and not path.is_symlink():
                    archive.add(path, arcname=f"{repo}/{name}", recursive=False)
        busybox = ROOT.parent / "Mini-Docker/rootfs/bin/busybox"
        if not busybox.is_file() or busybox.stat().st_size == 0:
            raise RuntimeError("static BusyBox fixture is missing")
        archive.add(busybox.resolve(), arcname="Mini-Docker/rootfs/bin/busybox", recursive=False)
        if args.install_mode=='verified-binaries':
            hashes={}
            for binary in ('cairn','cairnd'):
                path=ROOT/'bin'/binary
                hashes[binary]=hashlib.file_digest(path.open('rb'),'sha256').hexdigest()
                archive.add(path,arcname='SERVER/bin/'+binary,recursive=False)
            manifest=out/'binary-hashes.json'
            manifest.write_text(json.dumps(hashes,indent=2))
            archive.add(manifest,arcname='SERVER/bin/binary-hashes.json',recursive=False)
            wheels=list(wheel_dir.glob('*.whl'))
            if len(wheels)!=1:raise RuntimeError('expected one locally built runtime wheel')
            wheel=wheels[0]
            wheel_manifest=out/'wheel-hashes.json'
            wheel_manifest.write_text(json.dumps({wheel.name:hashlib.file_digest(wheel.open('rb'),'sha256').hexdigest()},indent=2))
            archive.add(wheel,arcname='SERVER/bin/'+wheel.name,recursive=False)
            archive.add(wheel_manifest,arcname='SERVER/bin/wheel-hashes.json',recursive=False)
            status['installation_mode']='verified-binaries; source build separately tested in GitHub CI'
    (out / "revisions.json").write_text(json.dumps(revisions, indent=2))
    goroot = Path(run(["go", "env", "GOROOT"]).strip())
    if not (args.resume and (out/'go-toolchain.tgz').exists()):
        run(["tar", "--transform", f"s,^{re.escape(goroot.name)},go,", "-czf", str(out / "go-toolchain.tgz"), "-C", str(goroot.parent), goroot.name], timeout=180)
    key_options = ["-i", str(out / "guest-key"), "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=accept-new",
                   "-o", f"UserKnownHostsFile={out / 'known-hosts'}", "-o", "ConnectTimeout=20"]
    def ssh(command, timeout=60, log_path=None):
        return run(["ssh", *key_options, "-p", str(args.ssh_port), "ubuntu@127.0.0.1", command], timeout, log_path)
    process = None
    serial = (out / "serial.log").open("ab")
    def start_vm():
        nonlocal process
        process = subprocess.Popen(["qemu-system-x86_64", "-accel", "tcg,thread=multi", "-cpu", "max", "-smp", "2", "-m", "2048",
            "-drive", f"file={out / 'disk.qcow2'},format=qcow2,if=virtio,cache=none",
            "-drive", f"file={out / 'seed.img'},format=raw,if=virtio,readonly=on",
            "-netdev", f"user,id=net0,hostfwd=tcp:127.0.0.1:{args.ssh_port}-:22", "-device", "virtio-net-pci,netdev=net0",
            "-display", "none", "-serial", "stdio", "-monitor", "none"], stdout=serial, stderr=serial)
        (out / "qemu.pid").write_text(str(process.pid))
    def ready(previous_boot=None):
        deadline = time.monotonic() + args.boot_timeout
        last = ""
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError("owned QEMU process exited; inspect serial.log")
            try:
                boot = ssh("cat /proc/sys/kernel/random/boot_id", 30).strip()
                if boot and boot != previous_boot:
                    return boot
            except Exception as exc:
                last = str(exc)
            time.sleep(5)
        raise RuntimeError(f"guest did not become reachable: {last}")
    try:
        save("booting-fresh-linux")
        start_vm()
        boot = ready()
        for filename in ("source.tar", "go-toolchain.tgz"):
            run(["scp", *key_options, "-P", str(args.ssh_port), str(out / filename), "ubuntu@127.0.0.1:/home/ubuntu/"], timeout=300)
        save("fresh-install-and-notes-proof")
        command="tar -xOf /home/ubuntu/source.tar SERVER/scripts/vm/install_and_prove.sh | "
        if args.install_mode=='verified-binaries': command+="env CAIRN_VM_INSTALL_MODE=verified-binaries "
        output = ssh(command+"bash", timeout=args.install_timeout, log_path=out/'install.log')
        fixture = next(json.loads(line) for line in reversed(output.splitlines()) if line.startswith('{"result": "passed"'))
        status["fixture"] = fixture
        name = fixture["service"]
        if not re.fullmatch(r"notes-proof-[0-9a-f]{8}", name):
            raise RuntimeError("unexpected test-service name")
        ssh(f"/home/ubuntu/.local/bin/cairn start {name}; curl --fail --silent --data 'before-reboot' http://127.0.0.1:8089/cgi-bin/notes")
        expected_data = "first\nbefore-reboot\n"
        def verify_data():
            deadline = time.monotonic() + 60
            last = ""
            while time.monotonic() < deadline:
                try:
                    last = ssh("systemctl is-active cairn-proof-daemon.service cairn-proof-runtime.service && curl --fail --silent http://127.0.0.1:8089/cgi-bin/notes")
                    if last == "active\nactive\n" + expected_data:
                        return
                except Exception as exc:
                    last = str(exc)
                time.sleep(2)
            raise AssertionError(f"VM recovery did not restore serving data: {last}")
        verify_data()
        status["checks"]["fresh_install"] = "passed"
        save("guest-graceful-reboot")
        try:
            ssh("sudo reboot", 10)
        except Exception:
            pass
        new_boot = ready(boot)
        verify_data()
        status["checks"]["guest_reboot"] = "passed"
        save("guest-abrupt-shutdown")
        process.kill(); process.wait(timeout=10)
        start_vm()
        ready(new_boot)
        verify_data()
        status["checks"]["abrupt_vm_shutdown"] = "passed"
        status["limitations"] = "QEMU SIGKILL is an abrupt guest shutdown, not proof against every physical disk or power failure."
        (out / "guest-journal.log").write_text(ssh("sudo journalctl -u cairn-proof-daemon -u cairn-proof-runtime --no-pager -n 250"))
        save("passed")
    except BaseException as exc:
        status["failure"] = str(exc)
        save("failed")
        raise
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill(); process.wait(timeout=10)
        serial.close()


if __name__ == "__main__":
    main()
