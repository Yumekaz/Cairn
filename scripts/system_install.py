#!/usr/bin/env python3
"""Two-stage, offline systemd installation. Never reboots or migrates app data.

prepare runs unprivileged; apply requires root and an explicitly named bundle.
The controller retains the owner's existing ~/.cairn state. Runtime socket
access is root-equivalent and is granted only to that owner's primary group.
"""
import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import pwd
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent
PREFIX = Path('/opt/cairn')
SOCKET = '/run/cairn-runtime/mini-docker.sock'
MARKER = '# Managed by Cairn system_install.py v1\n'
UNITS = ('cairn-runtime.service', 'cairn.service')


def run(argv, **kwargs):
    return subprocess.run([str(x) for x in argv], check=True, timeout=300, **kwargs)


def sha(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def unix_status(path, endpoint):
    class Connection(http.client.HTTPConnection):
        def connect(self):
            self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            self.sock.settimeout(2)
            self.sock.connect(str(path))
    connection = Connection('localhost', timeout=2)
    try:
        connection.request('GET', endpoint)
        response = connection.getresponse()
        response.read(1024 * 1024)
        return response.status
    finally:
        connection.close()


def atomic(path, data, mode=0o600, owner=None):
    path = Path(path)
    fd, temporary = tempfile.mkstemp(prefix='.' + path.name, dir=path.parent)
    try:
        os.fchmod(fd, mode)
        if owner:
            os.fchown(fd, *owner)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def check_path(path):
    """Reject systemd expansions/injection, broad write paths and symlink parents."""
    path = Path(path)
    if not path.is_absolute() or any(x in str(path) for x in ('%', '$', '\n', '\r', '\\', ' ', '"')):
        raise ValueError('installation paths must be absolute without systemd expansions or whitespace')
    if '..' in path.parts or len(path.parts) < 4:
        raise ValueError('refusing a broad installation/state path')
    for parent in (path, *path.parents):
        if parent.is_symlink():
            raise ValueError('symlink in installation/state path')
    return path


def validate_config(cfg, account):
    base = check_path(Path(account.pw_dir) / '.cairn')
    if cfg.get('data_dir') != str(base):
        raise ValueError('system installer currently supports only the owner ~/.cairn data layout')
    for key in ('socket_path', 'database_path', 'volume_dir', 'backup_dir'):
        value = check_path(cfg[key])
        if not value.is_relative_to(base) or value == base:
            raise ValueError(f'{key} must be a specific path inside ~/.cairn; no data migration is performed')
    if cfg.get('dashboard_addr') not in ('127.0.0.1:2476', 'localhost:2476', '[::1]:2476'):
        raise ValueError('system installation requires the default loopback dashboard; configure external TLS separately')
    cfg['mini_docker_socket'] = SOCKET
    cfg['admin_token_file'] = str(base / 'admin.token')
    return cfg


def unit_texts(release, account, config):
    release = check_path(release)
    home = check_path(Path(account.pw_dir) / '.cairn').parent
    data = check_path(config['data_dir'])
    runtime = MARKER + f'''[Unit]
Description=Cairn privileged Mini-Docker runtime
After=network.target

[Service]
Type=simple
User=root
Group={account.pw_gid}
RuntimeDirectory=cairn-runtime
RuntimeDirectoryMode=0750
Environment=MINI_DOCKER_ROOT=/var/lib/mini-docker
Environment=MINI_DOCKER_RUN=/run/cairn-runtime
WorkingDirectory={release}
ExecStart={release}/venv/bin/python -I -m mini_docker daemon --socket {SOCKET} --socket-mode 660
Restart=on-failure
RestartSec=2
Delegate=yes
KillMode=process
UMask=0027
NoNewPrivileges=yes
LimitNOFILE=65536
TasksMax=8192

[Install]
WantedBy=multi-user.target
'''
    daemon = MARKER + f'''[Unit]
Description=Cairn control plane
After=cairn-runtime.service
Requires=cairn-runtime.service

[Service]
Type=simple
User={account.pw_uid}
Group={account.pw_gid}
Environment=HOME={home}
Environment=CAIRN_ROOTFS={release}/rootfs
WorkingDirectory={data}
ExecStart={release}/cairnd --config {data}/cairnd-config.yaml
Restart=on-failure
RestartSec=2
UMask=0077
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths={data}
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictSUIDSGID=yes
LockPersonality=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
TimeoutStopSec=45

[Install]
WantedBy=multi-user.target
'''
    return dict(zip(UNITS, (runtime, daemon)))


def prepare(args):
    if os.geteuid() == 0:
        raise ValueError('prepare must run as the unprivileged installation owner')
    account = pwd.getpwuid(os.getuid())
    out = check_path(args.output)
    out.mkdir(parents=True, exist_ok=False, mode=0o700)
    runtime = Path(args.runtime_project).resolve()
    python = Path(args.python).absolute()
    for name in ('cairn', 'cairnd'):
        run(['go', 'build', '-trimpath', '-o', out / name, './cmd/' + name], cwd=ROOT)
    cfg = json.loads(subprocess.check_output(
        [str(out / 'cairn'), 'admin', 'config-json'], text=True))
    original_runtime_socket = cfg['mini_docker_socket']
    validate_config(cfg, account)
    run([python, '-m', 'build', '--wheel', '--no-isolation', '--outdir', out, runtime])
    wheels = list(out.glob('*.whl'))
    if len(wheels) != 1:
        raise ValueError('expected exactly one runtime wheel')
    shutil.copyfile(runtime / 'rootfs/bin/busybox', out / 'busybox')
    os.chmod(out / 'busybox', 0o755)
    revisions = {}
    for repo in (ROOT, ROOT.parent / 'DURAFLOW', runtime):
        revisions[repo.name] = {
            'commit': subprocess.check_output(['git', '-C', repo, 'rev-parse', 'HEAD'], text=True).strip(),
            'working_tree': subprocess.check_output(['git', '-C', repo, 'status', '--short'], text=True),
            'diff_sha256': hashlib.sha256(subprocess.check_output(['git', '-C', repo, 'diff', 'HEAD'])).hexdigest(),
            'untracked_sha256': {
                name: sha(repo / name) for name in subprocess.check_output(
                    ['git', '-C', repo, 'ls-files', '--others', '--exclude-standard'], text=True).splitlines()
                if (repo / name).is_file() and not (repo / name).is_symlink()
            },
        }
    artifacts = {p.name: sha(p) for p in (out / 'cairn', out / 'cairnd', out / 'busybox', wheels[0])}
    manifest = {'version': 1, 'uid': account.pw_uid, 'gid': account.pw_gid,
                'config': cfg, 'original_config_sha256': sha(Path(account.pw_dir) / '.cairn/cairnd-config.yaml'),
                'original_runtime_socket': original_runtime_socket,
                'artifacts': artifacts, 'revisions': revisions}
    atomic(out / 'manifest.json', json.dumps(manifest, indent=2).encode())
    print(json.dumps({'bundle': str(out), 'manifest_sha256': sha(out / 'manifest.json'),
                      'next': 'Review manifest, then sudo python3 scripts/system_install.py apply --bundle PATH --manifest-sha256 DIGEST'}, indent=2))


def secure_root_directory(path):
    def validate_existing():
        for current in (path, *path.parents):
            try:
                info = current.lstat()
            except FileNotFoundError:
                continue
            if stat.S_ISLNK(info.st_mode):
                raise ValueError('refusing symlinked system installation directory: ' + str(current))
            if info.st_uid != 0 or info.st_mode & 0o022:
                raise ValueError('system installation directory is not root-controlled: ' + str(current))
    # Do not even create a privileged leaf below an unsafe ancestor. Checking
    # only the new root-owned leaf would not protect the release from rename.
    validate_existing()
    path.mkdir(mode=0o755, parents=True, exist_ok=True)
    validate_existing()


def copy_verified(source, destination, expected, mode):
    fd = os.open(source, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise ValueError('bundle artifact must be a regular file')
        digest = hashlib.sha256()
        with os.fdopen(fd, 'rb', closefd=False) as stream, destination.open('xb') as output:
            while block := stream.read(1024 * 1024):
                digest.update(block)
                output.write(block)
            output.flush()
            os.fsync(output.fileno())
        if digest.hexdigest() != expected:
            raise ValueError('bundle artifact checksum mismatch')
        os.chmod(destination, mode)
    finally:
        os.close(fd)


def apply(args):
    if os.geteuid() != 0:
        raise ValueError('apply requires root')
    # Detect this before creating a partial root-owned release. The required
    # OS package is a deliberate prerequisite, not an implicit network install.
    run(['/usr/bin/python3', '-I', '-c', 'import ensurepip, venv'])
    bundle = check_path(args.bundle)
    data = (bundle / 'manifest.json').read_bytes()
    digest = hashlib.sha256(data).hexdigest()
    if digest != args.manifest_sha256:
        raise ValueError('manifest checksum changed since review')
    manifest = json.loads(data)
    if manifest['version'] != 1 or manifest['uid'] <= 0:
        raise ValueError('unsupported manifest or root controller owner')
    account = pwd.getpwuid(manifest['uid'])
    if account.pw_gid != manifest['gid']:
        raise ValueError('owner group changed since prepare')
    cfg = validate_config(dict(manifest['config']), account)
    state = Path(cfg['data_dir'])
    config_file = state / 'cairnd-config.yaml'
    check_path(config_file)
    if state.stat().st_uid != account.pw_uid or state.stat().st_mode & 0o077:
        raise ValueError('existing ~/.cairn must be private and owned by the controller user')
    if not config_file.is_file() or config_file.stat().st_uid != account.pw_uid:
        raise ValueError('existing config must be an owner-controlled regular file')
    if sha(config_file) != manifest['original_config_sha256']:
        raise ValueError('existing config changed; prepare a new bundle instead of overwriting it')
    for name in UNITS:
        path = Path('/etc/systemd/system') / name
        if path.is_symlink() or (path.exists() and not path.read_text().startswith(MARKER)):
            raise ValueError('refusing to overwrite an unmanaged unit: ' + name)
    # Do not silently replace a manually running controller/runtime.
    for path, unit in ((Path(cfg['socket_path']), 'cairn.service'), (Path(SOCKET), 'cairn-runtime.service')):
        if path.exists() and subprocess.run(['systemctl', 'is-active', '--quiet', unit]).returncode != 0:
            raise ValueError('unmanaged socket exists; inspect it before installation: ' + str(path))
    old_socket = manifest['original_runtime_socket']
    if old_socket != SOCKET:
        try:
            unix_status(old_socket, '/containers/json')
        except OSError:
            pass
        else:
            raise ValueError('another runtime answers at the old configured socket; no duplicate will be started')
    prefix = check_path(args.prefix)
    secure_root_directory(prefix)
    secure_root_directory(prefix / 'releases')
    release = prefix / 'releases' / digest[:20]
    release.mkdir(mode=0o755, exist_ok=False)
    files = manifest['artifacts']
    wheels = [name for name in files if name.endswith('.whl')]
    if set(files) != {'cairn', 'cairnd', 'busybox', *wheels} or len(wheels) != 1:
        raise ValueError('unexpected bundle artifacts')
    for name, expected in files.items():
        if Path(name).name != name:
            raise ValueError('invalid bundle artifact name')
        copy_verified(bundle / name, release / name, expected, 0o644 if name.endswith('.whl') else 0o755)
    atomic(release / 'manifest.json', data, 0o644)
    run(['/usr/bin/python3', '-m', 'venv', release / 'venv'])
    run([release / 'venv/bin/python', '-I', '-m', 'pip', 'install', '--no-index', '--no-deps', release / wheels[0]])
    rootfs = release / 'rootfs'
    for name in ('bin', 'dev', 'proc', 'sys', 'tmp', 'etc'):
        (rootfs / name).mkdir(parents=True)
    shutil.copyfile(release / 'busybox', rootfs / 'bin/busybox')
    os.chmod(rootfs / 'bin/busybox', 0o755)
    os.chmod(rootfs / 'tmp', 0o1777)
    for name in ('sh', 'echo', 'cat', 'dd', 'httpd', 'mkdir', 'rmdir', 'sync', 'sleep', 'printf'):
        (rootfs / 'bin' / name).symlink_to('busybox')
    # Refuse automatic adoption of any legacy, live numeric PID metadata.
    inventory = subprocess.check_output([release / 'venv/bin/python', '-I', '-c',
        'import json; from mini_docker.container import Container; '
        'print(json.dumps([{"id": c.id,"status": c.status} for c in Container().list(True)]))'], text=True,
        env={**os.environ, 'MINI_DOCKER_ROOT': '/var/lib/mini-docker'})
    if any(c['status'] == 'unknown' for c in json.loads(inventory)):
        raise ValueError('unverified legacy runtime processes need explicit inspection; no services were changed')
    owner = (account.pw_uid, account.pw_gid)
    token = Path(cfg['admin_token_file'])
    if not token.exists():
        run(['runuser', '-u', account.pw_name, '--', release / 'cairn', 'admin', 'init', '--token-file', token])
    elif token.is_symlink() or not token.is_file() or token.stat().st_uid != account.pw_uid or token.stat().st_mode & 0o077:
        raise ValueError('existing admin token is not a private owner-controlled regular file')
    value = token.read_bytes()
    text = value.strip()
    if len(value) > 4096 or len(text) < 32 or any(char in text for char in b' \t\r\n:'):
        raise ValueError('existing admin token is malformed; no services were changed')
    units = unit_texts(release, account, cfg)
    for name, text in units.items():
        atomic(release / name, text.encode(), 0o644)
    run(['systemd-analyze', 'verify', *[release / name for name in UNITS]])
    backup = state / ('cairnd-config.pre-system-' + str(time.time_ns()) + '.yaml')
    atomic(backup, config_file.read_bytes(), owner=owner)
    # JSON is valid YAML; avoids depending on a privileged third-party YAML parser.
    atomic(config_file, json.dumps(cfg, indent=2).encode(), owner=owner)
    for name, text in units.items():
        atomic(Path('/etc/systemd/system') / name, text.encode(), 0o644)
    run(['systemctl', 'daemon-reload'])
    run(['systemctl', 'enable', *UNITS])
    run(['systemctl', 'restart', 'cairn-runtime.service'])
    run(['systemctl', 'restart', 'cairn.service'])
    run(['systemctl', 'is-active', *UNITS])
    deadline = time.monotonic() + 30
    while True:
        try:
            if unix_status(SOCKET, '/containers/json') == 200 and unix_status(cfg['socket_path'], '/status') == 200:
                break
        except (OSError, http.client.HTTPException):
            pass
        if time.monotonic() >= deadline:
            raise ValueError('installed services did not become API-ready; retain the release/config backup and inspect the journal')
        time.sleep(.2)
    receipt = {'release': str(release), 'manifest_sha256': digest, 'owner': account.pw_name,
               'config_backup': str(backup), 'units': list(UNITS), 'applied_unix': time.time(),
               'warning': 'runtime socket group is root-equivalent; no host reboot was performed'}
    atomic(release / 'installation.json', json.dumps(receipt, indent=2).encode(), 0o644)
    print(json.dumps(receipt, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    build = commands.add_parser('prepare')
    build.add_argument('--output', type=Path, required=True)
    build.add_argument('--runtime-project', type=Path, default=ROOT.parent / 'Mini-Docker')
    build.add_argument('--python', type=Path, default=ROOT.parent / 'Mini-Docker/venv/bin/python')
    install = commands.add_parser('apply')
    install.add_argument('--bundle', type=Path, required=True)
    install.add_argument('--manifest-sha256', required=True)
    install.add_argument('--prefix', type=Path, default=PREFIX,
                         help='Root-controlled install directory (default /opt/cairn); every existing ancestor must be root-owned and not group/other-writable')
    args = parser.parse_args()
    try:
        (prepare if args.command == 'prepare' else apply)(args)
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        print('Installation stopped: ' + str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
