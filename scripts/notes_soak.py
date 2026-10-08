#!/usr/bin/env python3
"""Bounded real-app soak; creates and changes only its own notes-proof fixture."""
import argparse
import contextlib
import http.client
import io
import json
import os
from pathlib import Path
import signal
import socket
import statistics
import subprocess
import time

import notes_smoke as notes


def process_resources(pid):
    if not pid or int(pid) <= 0:
        raise RuntimeError("resource sample has no live process PID")
    fields = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
    rss = next(int(line.split()[1]) for line in Path(f"/proc/{pid}/status").read_text().splitlines()
               if line.startswith("VmRSS:"))
    return {"pid": int(pid), "start_ticks": int(fields[19]), "rss_kib": rss,
            "cpu_seconds": (int(fields[11])+int(fields[12])) / os.sysconf("SC_CLK_TCK")}


def runtime_resources(socket_path, runtime_id):
    class RuntimeHTTP(http.client.HTTPConnection):
        def connect(self):
            self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            self.sock.settimeout(self.timeout)
            self.sock.connect(socket_path)
    connection = RuntimeHTTP("localhost", timeout=5)
    try:
        connection.request("GET", f"/containers/{runtime_id}/json")
        response = connection.getresponse()
        if response.status != 200:
            raise RuntimeError(f"runtime resource inspection: {response.status}")
        container = json.loads(response.read())
        if container.get("status") != "running":
            raise RuntimeError("runtime is not running during resource sampling")
        return process_resources(container.get("pid"))
    finally:
        connection.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--port", type=int, default=8099)
    parser.add_argument("--duration-seconds", type=int, default=7200)
    parser.add_argument("--interval-seconds", type=float, default=15)
    parser.add_argument("--runtime-socket", default=os.environ.get("MINI_DOCKER_SOCKET", f"/run/user/{os.getuid()}/mini-docker/mini-docker.sock"))
    parser.add_argument("--daemon-unit", default="cairn-acceptance-live.service")
    parser.add_argument("--reads-per-sample", type=int, default=3)
    args = parser.parse_args()
    if not 1 <= args.duration_seconds <= 14400 or args.interval_seconds < 1:
        parser.error("duration must be 1..14400 seconds and interval >=1")
    if args.duration_seconds / args.interval_seconds > 20000:
        parser.error("at most 20000 samples are allowed; increase the interval")
    if not 1 <= args.reads_per_sample <= 16:
        parser.error("reads per sample must be 1..16")
    args.output.mkdir(parents=True, exist_ok=False)
    notes.PORT = args.port
    status = {"state": "starting", "target_seconds": args.duration_seconds,
              "purpose": "acceptance-session" if args.duration_seconds >= 7200 else "runner-smoke",
              "started_unix": time.time(), "requests": 0, "errors": 0,
              "max_data_bytes": 4 * 1024 * 1024}
    latencies = []
    fixture = None
    def save():
        status["updated_unix"] = time.time()
        encoded = json.dumps(status, indent=2)
        target = args.output / "status.json"
        temporary = target.with_suffix(".pending")
        with temporary.open("w") as stream:
            stream.write(encoded); stream.flush(); os.fsync(stream.fileno())
        temporary.replace(target)
        directory_fd=os.open(args.output,os.O_RDONLY|os.O_DIRECTORY)
        try: os.fsync(directory_fd)
        finally: os.close(directory_fd)
    def interrupted(signum, frame):
        raise InterruptedError(f"soak interrupted by signal {signum}")
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    save()
    try:
        revisions = {}
        for repo in ("SERVER", "Mini-Docker", "DURAFLOW", "FAILFORGE", "Mini-Redis-Cassandra", "Coordination-service"):
            directory = notes.ROOT.parent / repo
            revisions[repo] = {"commit": subprocess.check_output(["git", "-C", str(directory), "rev-parse", "HEAD"], text=True).strip(),
                               "working_tree": subprocess.check_output(["git", "-C", str(directory), "status", "--short"], text=True)}
        (args.output / "revisions.json").write_text(json.dumps(revisions, indent=2))
        # The existing smoke proof creates unique volumes and tests restoration
        # before returning a stopped, fully identified disposable service.
        captured = io.StringIO()
        def declared_fixture(value):
            nonlocal fixture
            fixture=value
            status["fixture"]=value
            save()
        with contextlib.redirect_stdout(captured):
            notes.main(on_fixture=declared_fixture)
        fixture = json.loads(captured.getvalue())
        status["fixture"] = fixture
        (args.output / "fixture.json").write_text(json.dumps(fixture, indent=2))
        notes.api("POST", f'/services/{fixture["service"]}/start')
        notes.expect("first\n")
        volume = notes.api("GET", f'/volumes/{fixture["data_volume"]}')
        data_path = Path(volume["host_path"])
        started = time.monotonic()
        previous_wall = time.time()
        expected = "first\n"
        sequence = 0
        previous_daemon = previous_runtime = None
        daemon_restarts = runtime_restarts = 0
        status["state"] = "running"
        with (args.output / "samples.jsonl").open("a", buffering=1) as samples:
            while time.monotonic() - started < args.duration_seconds:
                now = time.time()
                if now - previous_wall > max(90, args.interval_seconds * 3):
                    raise InterruptedError("host suspension or sampling gap; continuity is not proven")
                previous_wall = now
                sequence += 1
                marker = f"soak-{sequence:08d}"
                before = time.monotonic()
                status["requests"] += 1
                notes.journal("POST", marker)
                latencies.append((time.monotonic() - before) * 1000)
                expected += marker + "\n"
                for read in range(args.reads_per_sample):
                    before = time.monotonic()
                    status["requests"] += 1
                    actual = notes.journal()
                    latencies.append((time.monotonic() - before) * 1000)
                    if actual != expected:
                        raise AssertionError(f"journal mismatch at sample {sequence}, read {read}")
                data_bytes = sum(path.stat().st_size for path in data_path.rglob("*") if path.is_file())
                if data_bytes > status["max_data_bytes"]:
                    raise RuntimeError("fixture reached its bounded data budget")
                stat = os.statvfs(data_path)
                if stat.f_bavail * stat.f_frsize < 1024 * 1024 * 1024:
                    raise RuntimeError("less than 1GiB free; refusing further writes")
                daemon = notes.api("GET", "/status")
                service = notes.api("GET", f'/services/{fixture["service"]}')
                daemon_pid = subprocess.check_output(["systemctl", "--user", "show", args.daemon_unit, "-p", "MainPID", "--value"], text=True).strip()
                daemon_metrics = process_resources(daemon_pid)
                runtime_metrics = runtime_resources(args.runtime_socket, service["runtime_id"])
                daemon_identity = (daemon_metrics["pid"], daemon_metrics["start_ticks"])
                runtime_identity = (runtime_metrics["pid"], runtime_metrics["start_ticks"])
                if previous_daemon is not None and daemon_identity != previous_daemon:
                    daemon_restarts += 1
                if previous_runtime is not None and runtime_identity != previous_runtime:
                    runtime_restarts += 1
                previous_daemon, previous_runtime = daemon_identity, runtime_identity
                status.update(elapsed_seconds=round(time.monotonic()-started, 2), samples=sequence,
                              data_bytes=data_bytes, runtime_id=service["runtime_id"], daemon=daemon,
                              daemon_resources=daemon_metrics, runtime_resources=runtime_metrics,
                              daemon_restarts=daemon_restarts, runtime_restarts=runtime_restarts,
                              latency_p50_ms=round(statistics.median(latencies), 3),
                              latency_p95_ms=round(sorted(latencies)[int((len(latencies)-1)*.95)],3),
                              latency_max_ms=round(max(latencies),3))
                samples.write(json.dumps(status) + "\n")
                os.fsync(samples.fileno())
                save()
                time.sleep(min(args.interval_seconds, max(0, args.duration_seconds-(time.monotonic()-started))))
        notes.api("POST", f'/volumes/{fixture["data_volume"]}/restore', {"backup_id": fixture["backup"]})
        notes.expect("first\n")
        status.update(state="passed", restore_verified=True, elapsed_seconds=round(time.monotonic()-started,2))
        save()
    except BaseException as exc:
        status.update(state="interrupted" if isinstance(exc,InterruptedError) else "failed",
                      errors=status["errors"]+1, failure=str(exc))
        save()
        raise
    finally:
        if fixture:
            try:
                notes.api("POST", f'/services/{fixture["service"]}/stop')
            except Exception as exc:
                status["cleanup_error"] = str(exc)
                if status["state"] == "passed":
                    status["state"] = "failed"
                    status["errors"] += 1
                save()
        print(json.dumps(status), flush=True)
        if status.get("cleanup_error") and status["state"]=="failed":
            raise RuntimeError("soak fixture cleanup failed; inspect the retained status")


if __name__ == "__main__":
    main()
