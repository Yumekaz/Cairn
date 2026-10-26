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
SOURCE_REPOS = ('SERVER', 'DURAFLOW', 'Mini-Docker')
SOURCE_PATHS = ('.', ':(exclude)validation-runs/**', ':(exclude)verification/**')
STATIC_NETWORK = {
    'id': 'private-static-v1', 'net': '10.0.2.0/24', 'host': '10.0.2.2',
    'dns': '10.0.2.3', 'guest': '10.0.2.15', 'prefix': 24,
    'mac': '52:54:00:ca:17:01', 'dhcp4': False, 'dhcp6': False,
    'accept_ra': False, 'ipv6': False,
}
LEGACY_NETWORK = {'id': 'legacy-default-usernet-v0',
                  'addressing': 'original seed and QEMU defaults retained; not a static-network proof'}


def select_network_profile(previous=None):
    """Never retrofit the fresh static NIC/MAC onto an old DHCP seed."""
    if previous is None:
        return dict(STATIC_NETWORK)
    recorded = previous.get('network_profile')
    if recorded is None:
        return dict(LEGACY_NETWORK)
    if not isinstance(recorded, dict):
        raise RuntimeError('invalid recorded VM network profile')
    profiles = {item['id']: item for item in (STATIC_NETWORK, LEGACY_NETWORK)}
    expected = profiles.get(recorded.get('id'))
    if expected is None or any(recorded.get(key) != value for key, value in expected.items()):
        raise RuntimeError('unknown or changed VM network profile; use a fresh fixture')
    return dict(recorded)


def network_arguments(profile, ssh_port):
    if type(ssh_port) is not int or not 1 <= ssh_port <= 65535:
        raise ValueError('SSH forwarding port must be between 1 and 65535')
    profile = select_network_profile({'network_profile': profile})
    if profile['id'] == LEGACY_NETWORK['id']:
        # Byte-for-byte original networking: even adding a MAC would change
        # the old guest's NIC identity and could break historical resumes.
        backend = f'user,id=net0,hostfwd=tcp:127.0.0.1:{ssh_port}-:22'
        device = 'virtio-net-pci,netdev=net0'
    else:
        backend = (f"user,id=net0,ipv4=on,net={profile['net']},host={profile['host']},"
                   f"dns={profile['dns']},ipv6=off,"
                   f"hostfwd=tcp:127.0.0.1:{ssh_port}-{profile['guest']}:22")
        device = f"virtio-net-pci,netdev=net0,mac={profile['mac']}"
    return ['-netdev', backend, '-device', device]


def render_user_data(public_key, install_mode):
    if install_mode not in ('source', 'verified-binaries'):
        raise ValueError('unknown guest installation mode')
    if not re.fullmatch(r'ssh-ed25519 [A-Za-z0-9+/]+={0,2}(?: [^\r\n]*)?', public_key):
        raise ValueError('expected one generated ed25519 guest public key')
    compiler_packages = '  - git\n  - build-essential' if install_mode == 'source' else ''
    return ((ROOT / 'scripts/vm/user-data.yaml').read_text()
            .replace('@SSH_KEY@', json.dumps(public_key))
            .replace('@SOURCE_PACKAGES@', compiler_packages))


def render_network_config():
    text = (ROOT / 'scripts/vm/network-config.yaml').read_text()
    for key in ('mac', 'guest', 'prefix', 'host', 'dns'):
        text = text.replace('@' + key.upper() + '@', str(STATIC_NETWORK[key]))
    if re.search(r'@[A-Z_]+@', text):
        raise RuntimeError('unresolved VM network seed setting')
    return text


def file_sha256(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def prepare_seed(out, public_key, install_mode, run):
    """Fresh fixtures only. Resume must never call this or rewrite seed files."""
    (out / 'user-data').write_text(render_user_data(public_key, install_mode))
    (out / 'meta-data').write_text('instance-id: cairn-acceptance-vm\nlocal-hostname: cairn-acceptance-vm\n')
    (out / 'network-config').write_text(render_network_config())
    run(['cloud-localds', '-N', str(out / 'network-config'), str(out / 'seed.img'),
         str(out / 'user-data'), str(out / 'meta-data')])
    profile = select_network_profile()
    profile['seed_files_sha256'] = {
        name: file_sha256(out / name)
        for name in ('user-data', 'meta-data', 'network-config', 'seed.img')
    }
    return profile


def resume_network_profile(out, previous):
    profile = select_network_profile(previous)
    if profile['id'] == STATIC_NETWORK['id']:
        expected = profile.get('seed_files_sha256', {})
        if set(expected) != {'user-data', 'meta-data', 'network-config', 'seed.img'}:
            raise RuntimeError('static-network resume requires its original recorded seed files')
        for name, digest in expected.items():
            if file_sha256(out / name) != digest:
                raise RuntimeError('VM seed changed; refusing network retrofit on resume: ' + name)
    else:
        # Sidecar user-data from historical retries may not match the ISO.
        # Pin only the actual old seed, not today's templates or assumptions.
        digest = file_sha256(out / 'seed.img')
        expected = profile.get('seed_sha256', digest)
        if digest != expected:
            raise RuntimeError('legacy VM seed changed since previous resume')
        profile['seed_sha256'] = digest
    return profile


def prelaunch_inventory():
    """Read-only host inventory immediately before EACH owned QEMU launch."""
    pid1 = Path('/proc/1/comm').read_text().strip()
    if pid1 not in ('systemd', 'init'):
        raise RuntimeError('cannot verify host VM inventory from this PID namespace; run on the host')
    output = subprocess.check_output(['ps', '-eo', 'pid=,comm=,args='], text=True, timeout=15)
    active = []
    for line in output.splitlines():
        fields = line.split(None, 2)
        if len(fields) >= 2 and (fields[1].startswith('qemu-system') or fields[1] == 'qemu-kvm'):
            active.append(line.strip())
    if active:
        raise RuntimeError('existing QEMU process found; inspect/stop its owner before launching another VM: ' + '; '.join(active))
    return {'checked_unix': time.time(), 'pid1': pid1, 'active_qemu': active}


def source_revision(directory):
    def git(*arguments):
        return subprocess.check_output(['git', '-C', str(directory), *arguments], timeout=30)
    return {'commit': git('rev-parse', 'HEAD').decode().strip(),
            'working_tree': git('status', '--short', '--untracked-files=all', '--', *SOURCE_PATHS).decode(),
            'diff_sha256': hashlib.sha256(git('diff', '--binary', 'HEAD', '--', *SOURCE_PATHS)).hexdigest()}


def capture_source_inputs(allow_dirty=False):
    inputs = {repo: source_revision(ROOT.parent / repo) for repo in SOURCE_REPOS}
    dirty = [repo for repo, item in inputs.items() if item['working_tree']]
    if dirty and not allow_dirty:
        raise RuntimeError('final-revision VM proof requires clean source: ' + ', '.join(dirty))
    return inputs


def verify_source_inputs(inputs):
    for repo, expected in inputs.items():
        if source_revision(ROOT.parent / repo) != expected:
            raise RuntimeError('source changed while building/capturing VM inputs: ' + repo)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--ssh-port", type=int, default=23222)
    parser.add_argument("--boot-timeout", type=int, default=1200)
    parser.add_argument("--install-timeout", type=int, default=3600)
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--install-mode", choices=['source','verified-binaries'], default='source')
    parser.add_argument("--resume-installed", action="store_true")
    parser.add_argument("--reverify-installed", action="store_true", help="explicitly install final code on a previously passed guest and repeat its recovery proof")
    parser.add_argument("--integration-proof", action="store_true", help="run the complete integration suite inside the disposable guest")
    parser.add_argument('--allow-dirty', action='store_true', help='Explicit development snapshot only; never final-revision proof')
    args = parser.parse_args()
    if args.integration_proof and args.install_mode != 'verified-binaries':
        parser.error('--integration-proof requires verified-binaries mode')
    if not 1 <= args.ssh_port <= 65535:
        parser.error('--ssh-port must be between 1 and 65535')
    source_inputs = capture_source_inputs(args.allow_dirty)
    image = args.image.resolve()
    manifest = image.parent / "SHA256SUMS"
    expected = next(line.split()[0] for line in manifest.read_text().splitlines()
                    if line.split()[-1].lstrip("*") == image.name)
    digest = file_sha256(image)
    if digest != expected:
        raise RuntimeError("official cloud-image checksum does not match")
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", args.ssh_port))
    if args.resume:
        previous=json.loads((args.output/'status.json').read_text())
        if previous.get('image_sha256')!=digest:
            raise RuntimeError('resume requires this same verified image')
        if previous.get('checks') and not (args.reverify_installed and previous.get('state') == 'passed'):
            raise RuntimeError('completed gates require explicit --reverify-installed; never discard partial recovery evidence')
        (args.output/f'previous-status-{time.time_ns()}.json').write_text(json.dumps(previous,indent=2))
        if args.resume_installed and not previous.get('fixture'):
            raise RuntimeError('no completed disposable installation fixture to resume')
    else:
        args.output.mkdir(parents=True, exist_ok=False)
    out = args.output.resolve()
    network_profile = resume_network_profile(out, previous) if args.resume else select_network_profile()
    if (args.resume_installed or args.reverify_installed) and not args.resume:
        parser.error('installed-state options require --resume')
    if args.resume_installed and args.reverify_installed:
        parser.error('choose resume without reinstalling OR explicit final-code reinstallation')
    status = {"state": "preparing", "image_sha256": digest, "started_unix": time.time(),
              "limits": {"disk_gib": 8, "ram_mib": 2048, "vcpus": 2}, "checks": {},
              "release_source_clean": not args.allow_dirty, 'source_inputs': source_inputs,
              'network_profile': network_profile, 'ssh_forward': {
                  'host': '127.0.0.1', 'port': args.ssh_port, 'guest_port': 22}}
    def save(phase):
        status["state"] = phase
        status["updated_unix"] = time.time()
        temporary = out / 'status.pending'
        with temporary.open('w') as stream:
            stream.write(json.dumps(status, indent=2)); stream.flush(); os.fsync(stream.fileno())
        temporary.replace(out / 'status.json')
        directory_fd=os.open(out,os.O_RDONLY|os.O_DIRECTORY)
        try: os.fsync(directory_fd)
        finally: os.close(directory_fd)
        print(phase, flush=True)
    def run(argv, timeout=120, log_path=None):
        started=time.time()
        if log_path:
            with log_path.open('a') as stream:
                stream.seek(0,os.SEEK_END)
                offset=stream.tell()
                result=subprocess.run(argv,stdout=stream,stderr=subprocess.STDOUT,text=True,timeout=timeout)
            with (out/'commands.jsonl').open('a') as evidence:
                evidence.write(json.dumps({'argv':argv,'started_unix':started,'exit_code':result.returncode,'log':str(log_path),'offset':offset})+'\n')
            if result.returncode:
                raise RuntimeError(f'{argv[0]} failed; retained output: {log_path}')
            # Earlier successful fixture output must not satisfy a resumed
            # invocation that produced no new proof of its own.
            with log_path.open() as stream:
                stream.seek(offset)
                return stream.read()
        result = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
        with (out/'commands.jsonl').open('a') as evidence:
            evidence.write(json.dumps({'argv':argv,'started_unix':started,'exit_code':result.returncode})+'\n')
        if result.returncode:
            raise RuntimeError(f"{argv[0]} failed: {result.stderr[-3000:]}")
        return result.stdout
    if not args.resume:
        run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(out / "guest-key")])
    if not args.resume:
        public_key = (out / 'guest-key.pub').read_text().strip()
        status['network_profile'] = prepare_seed(out, public_key, args.install_mode, run)
        run(["qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b", str(image), str(out / "disk.qcow2"), "8G"])
    elif (out/'source.tar').exists():
        stamp=str(int(time.time()))
        (out/'source.tar').rename(out/f'source-before-resume-{stamp}.tar')
        (out/'revisions.json').rename(out/f'revisions-before-resume-{stamp}.json')
    # Preserve the seed/profile provenance even if later source capture fails.
    save('preparing')
    revisions = {}
    if args.install_mode=='verified-binaries':
        for binary in ('cairn','cairnd'):
            run(['go','build','-o',str(ROOT/'bin'/binary),'./cmd/'+binary],timeout=180)
        if args.integration_proof:
            run(['go','test','-c','-o',str(ROOT/'bin/cairn-integration-tests'),'./tests/integration'],timeout=180)
        wheel_dir=out/f'runtime-wheel-{int(time.time())}'
        run([str(ROOT.parent/'Mini-Docker/venv/bin/python'),'-m','build','--wheel','--no-isolation','--outdir',str(wheel_dir),str(ROOT.parent/'Mini-Docker')],timeout=180)
    with tarfile.open(out / "source.tar", "w") as archive:
        for repo in SOURCE_REPOS:
            directory = ROOT.parent / repo
            files = subprocess.check_output(["git", "-C", str(directory), "ls-files", "-co", "--exclude-standard", "-z"]).decode().split("\0")
            revisions[repo] = dict(source_inputs[repo])
            revisions[repo]['source_files_sha256'] = {}
            for name in dict.fromkeys(files):
                if not name or name.startswith((".env", ".agents/", ".codex/", 'validation-runs/', 'verification/')):
                    continue
                path = directory / name
                if path.is_file() and not path.is_symlink():
                    archive.add(path, arcname=f"{repo}/{name}", recursive=False)
                    revisions[repo]['source_files_sha256'][name] = hashlib.file_digest(path.open('rb'), 'sha256').hexdigest()
        source_record = out / 'source-revisions.json'
        source_record.write_text(json.dumps(revisions, indent=2))
        archive.add(source_record, arcname='SERVER/bin/source-revisions.json', recursive=False)
        busybox = ROOT.parent / "Mini-Docker/rootfs/bin/busybox"
        if not busybox.is_file() or busybox.stat().st_size == 0:
            raise RuntimeError("static BusyBox fixture is missing")
        archive.add(busybox.resolve(), arcname="Mini-Docker/rootfs/bin/busybox", recursive=False)
        if args.install_mode=='verified-binaries':
            hashes={}
            for binary in ('cairn','cairnd') + (('cairn-integration-tests',) if args.integration_proof else ()):
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
    verify_source_inputs(source_inputs)
    (out / "revisions.json").write_text(json.dumps(revisions, indent=2))
    if args.install_mode == 'source':
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
        status.setdefault('prelaunch_inventory', []).append(prelaunch_inventory())
        process = subprocess.Popen(["qemu-system-x86_64", "-accel", "tcg,thread=multi", "-cpu", "max", "-smp", "2", "-m", "2048",
            "-drive", f"file={out / 'disk.qcow2'},format=qcow2,if=virtio,cache=none",
            "-drive", f"file={out / 'seed.img'},format=raw,if=virtio,readonly=on",
            *network_arguments(status['network_profile'], args.ssh_port),
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
    installed_units = ['cairn.service', 'cairn-runtime.service']
    if args.resume_installed:
        installed_units = previous.get('installed_units', ['cairn-proof-daemon.service', 'cairn-proof-runtime.service'])
    if installed_units not in (['cairn.service', 'cairn-runtime.service'], ['cairn-proof-daemon.service', 'cairn-proof-runtime.service']):
        raise RuntimeError('unexpected installed unit identities')
    status['installed_units'] = installed_units
    try:
        save("booting-fresh-linux")
        start_vm()
        boot = ready()
        for filename in ("source.tar",) + (("go-toolchain.tgz",) if args.install_mode == 'source' else ()):
            run(["scp", *key_options, "-P", str(args.ssh_port), str(out / filename), "ubuntu@127.0.0.1:/home/ubuntu/"], timeout=300)
        save("fresh-install-and-notes-proof")
        if args.reverify_installed:
            old_name = previous['fixture']['service']
            if not re.fullmatch(r"notes-proof-[0-9a-f]{8}", old_name):
                raise RuntimeError('unexpected previous disposable fixture name')
            # Reinstall final code, but keep the previous pass and its data.
            old_service=json.loads(ssh(f"curl --fail --silent --unix-socket /home/ubuntu/.cairn/cairnd.sock http://localhost/services/{old_name}"))
            if old_service.get('desired_state') == 'running':
                ssh(f"/home/ubuntu/.local/bin/cairn stop {old_name}")
        command="tar -xOf /home/ubuntu/source.tar SERVER/scripts/vm/install_and_prove.sh | "
        command += "env "
        if args.install_mode=='verified-binaries': command+="CAIRN_VM_INSTALL_MODE=verified-binaries "
        if args.integration_proof: command+="CAIRN_VM_INTEGRATION_PROOF=1 "
        if args.resume_installed:
            fixture=previous['fixture']
        else:
            output = ssh(command+"bash", timeout=args.install_timeout, log_path=out/'install.log')
            fixture = next(json.loads(line) for line in reversed(output.splitlines()) if line.startswith('{"result": "passed"'))
        if args.integration_proof:
            status['checks']['full_integration_suite']='passed'
        status["fixture"] = fixture
        name = fixture["service"]
        if not re.fullmatch(r"notes-proof-[0-9a-f]{8}", name):
            raise RuntimeError("unexpected test-service name")
        # Reboot/resume may already have reconciled the installed service.
        # Read its desired state rather than issuing a duplicate Start.
        service = json.loads(ssh(f"curl --fail --silent --unix-socket /home/ubuntu/.cairn/cairnd.sock http://localhost/services/{name}"))
        if service.get('desired_state') != 'running':
            ssh(f"/home/ubuntu/.local/bin/cairn start {name}")
        expected_data = "first\n"
        def verify_data():
            deadline = time.monotonic() + 60
            last = ""
            while time.monotonic() < deadline:
                try:
                    last = ssh("systemctl is-active " + ' '.join(installed_units) + " && curl --fail --silent http://127.0.0.1:8089/cgi-bin/notes")
                    if last == "active\nactive\n" + expected_data:
                        return
                except Exception as exc:
                    last = str(exc)
                time.sleep(2)
            raise AssertionError(f"VM recovery did not restore serving data: {last}")
        verify_data()
        ssh("curl --fail --silent --show-error --data 'before-reboot' http://127.0.0.1:8089/cgi-bin/notes")
        expected_data = "first\nbefore-reboot\n"
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
        (out / "guest-journal.log").write_text(ssh("sudo journalctl -u " + ' -u '.join(installed_units) + " --no-pager -n 250"))
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
