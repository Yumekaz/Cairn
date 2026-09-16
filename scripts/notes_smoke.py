#!/usr/bin/env python3
"""Exercise a real notes journal through deploy, restart, redeploy and restore.
Creates uniquely named services/volumes and retains data; stops the test service.
"""
import http.client
import json
import os
from pathlib import Path
import shutil
import socket
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parent.parent
SOCKET = os.environ.get("CAIRN_SOCKET", str(Path.home() / ".cairn/cairnd.sock"))
PORT = int(os.environ.get("NOTES_PROOF_PORT", "8088"))

class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(SOCKET)

def api(method, path, body=None):
    conn = UnixHTTP("localhost", timeout=90)
    try:
        conn.request(method, path, json.dumps(body) if body is not None else None,
                     {"Content-Type": "application/json"})
        response = conn.getresponse()
        payload = response.read()
        if response.status >= 400:
            raise RuntimeError(f"{method} {path}: {response.status} {payload!r}")
        return json.loads(payload) if payload else None
    finally:
        conn.close()

def journal(method="GET", body=None):
    conn = http.client.HTTPConnection("127.0.0.1", PORT, timeout=5)
    try:
        conn.request(method, "/cgi-bin/notes", body)
        response = conn.getresponse()
        value = response.read().decode()
        if response.status >= 400:
            raise RuntimeError(f"journal: {response.status}: {value}")
        return value
    finally:
        conn.close()

def expect(value):
    deadline = time.monotonic() + 20
    last = None
    while time.monotonic() < deadline:
        try:
            last = journal()
            if last == value:
                return
        except (OSError, RuntimeError) as exc:
            last = str(exc)
        time.sleep(.25)
    raise AssertionError(f"expected {value!r}, got {last!r}")

def main():
    # Fail before creating resources when this host port is already occupied.
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", PORT))
    name = "notes-proof-" + uuid.uuid4().hex[:8]
    code_name, data_name = name + "-code", name + "-data"
    code = api("POST", "/volumes", {"name": code_name, "mount_path": "/www"})
    api("POST", "/volumes", {"name": data_name, "mount_path": "/data"})
    target = Path(code["host_path"]) / "cgi-bin"
    target.mkdir()
    shutil.copy2(ROOT / "examples/notes/cgi-bin/notes", target / "notes")
    (target / "notes").chmod(0o755)
    cfg = {"name": name, "kind": "web",
           "image": os.environ.get("CAIRN_ROOTFS", str(ROOT.parent / "Mini-Docker/rootfs")),
           "command": ["/bin/busybox", "httpd", "-f", "-p", "80", "-h", "/www"],
           "ports": [{"host": PORT, "container": 80}],
           "volumes": [{"name": code_name, "mount_path": "/www"}, {"name": data_name, "mount_path": "/data"}],
           # The HTTP API encodes Go durations as nanoseconds (YAML accepts strings).
           "healthcheck": {"http_path": "/cgi-bin/notes", "interval": 2000000000, "timeout": 1000000000, "retries": 3, "startup_grace": 1000000000}}
    started = time.monotonic()
    try:
        api("POST", "/services", cfg)
        expect("")
        journal("POST", "first")
        expect("first\n")
        backup = api("POST", f"/volumes/{data_name}/backups")
        journal("POST", "second")
        api("POST", f"/services/{name}/restart")
        expect("first\nsecond\n")
        api("POST", "/services", cfg)
        expect("first\nsecond\n")
        api("POST", f"/volumes/{data_name}/restore", {"backup_id": backup["id"]})
        expect("first\n")
        print(json.dumps({"result": "passed", "service": name, "data_volume": data_name,
                          "backup": backup["id"], "seconds": round(time.monotonic()-started, 2),
                          "verified": ["write", "restart", "redeploy", "backup", "restore"]}))
    finally:
        already_failed = sys.exc_info()[0] is not None
        try:
            api("POST", f"/services/{name}/stop")
        except Exception as exc:
            if not already_failed:
                raise
            print(f"Cleanup also failed for {name}: {exc}", file=sys.stderr)

if __name__ == "__main__":
    main()
