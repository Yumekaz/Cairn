# Administration and system installation

Cairn's Unix API is protected by filesystem permissions. Access to the
Mini-Docker runtime socket is **root-equivalent**: grant it only to trusted
administrators, never to an application or an untrusted user.

## Authenticated TCP administration

Create a credential without displaying it:

```sh
cairn admin init
```

Set `admin_token_file` in `~/.cairn/cairnd-config.yaml` to the resulting private
file and restart cairnd. The browser uses HTTP Basic authentication with
username `cairn` and that file's token as its password. Do not put credentials
in a URL, command-line argument, Git, screenshots, or shared logs. Read them
locally in a private context. Token changes take effect after daemon restart;
rotate the token to revoke previously issued credentials. There is no
multi-user account, role, MFA or per-user audit system.

Loopback HTTP remains available for compatibility. It is not encrypted and
is not a public deployment configuration. Every non-loopback dashboard bind
requires both authentication and native TLS; invalid settings fail startup
before API listeners or background workers start.

For a separately configured public deployment, supply a certificate trusted
by your clients and matching your hostname:

```yaml
dashboard_addr: "0.0.0.0:2476"
admin_token_file: /home/operator/.cairn/admin.token
dashboard_tls_cert: /home/operator/.cairn/tls/fullchain.pem
dashboard_tls_key: /home/operator/.cairn/tls/private-key.pem
dashboard_hosts: ["admin.example.com"]
```

This is a configuration example, not a provided certificate or DNS setup.
Private keys and tokens must be regular files with no group/other access
(typically `0600`), inside operator-controlled directories. TLS 1.2 or newer
is required. Expired/not-yet-valid certificates and broken key pairs fail
startup. Renew certificates and restart before expiry. Host and Origin
checks still apply; forwarded headers do not bypass them. Configure proxy
origin/Host preservation deliberately rather than disabling these guards.

Application `*.localhost` routes remain separate from the administrative
host. Admin authentication does not supply application authentication.

## Boot-enabled Linux installation

The two-stage installer builds as the ordinary owner and installs a reviewed,
checksummed, offline bundle into root-owned `/opt/cairn/releases/`. It never
executes the privileged runtime from a writable source checkout or adds users
to privileged groups. Python isolated mode excludes user site packages and
`PYTHONPATH` from the installed runtime.

Prerequisites: systemd Linux, Python 3.11+ with `venv`, Go matching `go.mod`,
the sibling repositories/build tools, an initialized private `~/.cairn`, and
the bundled BusyBox rootfs. The current installer intentionally supports the
owner's `~/.cairn` data/volume/backup layout and loopback port 2476 only. It
refuses automatic external data migration or public exposure.

```sh
python3 scripts/system_install.py prepare --output /absolute/path/to/new-bundle
# Review manifest.json and the printed SHA-256. Then, from this checkout:
sudo python3 scripts/system_install.py apply \
  --bundle /absolute/path/to/new-bundle --manifest-sha256 PRINTED_DIGEST
systemctl status cairn.service cairn-runtime.service
```

Every existing installation-path ancestor must be root-owned and not writable
by a group or other users. A root-owned leaf under a writable `/opt` is not safe.
If your environment intentionally gives ordinary users control of `/opt` (as
some CI images do), select a separately root-controlled prefix with
`apply --prefix /usr/local/lib/cairn`. This retains the same ownership/symlink
checks; do not change shared directory permissions merely to make a test pass.

The installer preserves existing data paths, backs up the previous config,
enables authentication, and waits for both APIs to be ready. It refuses
unmanaged service/socket replacement and uncertain legacy runtime identities.
Interrupted/failed bundles and installation backups are retained, not erased
or silently retried. Re-prepare after source or configuration changes.

The controller runs as its ordinary owner with a read-only system/home view
except for its specific data directory. The runtime remains privileged for
namespaces, mounts, networking and cgroups. `Delegate=yes` and
`KillMode=process` preserve supervised workloads during runtime-daemon
restart; deliberately stop applications through Cairn before decommissioning
the runtime. This is not proof against hostile container escapes or a
replacement for an independent isolation audit.

Security changes to workload mount namespaces require an explicit workload
restart; restarting the two daemons alone intentionally preserves running
containers. The runtime now requires read-only procfs and private inert masks
for sensitive proc interfaces. Exec refuses a legacy unprotected namespace,
but that refusal does not secure or stop an already-running old workload.
Plan a controlled service restart after installing this runtime update. Procfs
tuning writes (including per-process proc-file writes) are unsupported; readable
process/status information and private writable shared memory remain available.

Inspect failures with `journalctl -u cairn -u cairn-runtime`; never publish
unredacted application secrets. Installation does not reboot the host. A
disposable VM must separately demonstrate guest boot/reboot/crash recovery.
