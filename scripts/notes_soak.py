#!/usr/bin/env python3
"""Bounded real-app soak; creates and changes only its own notes-proof fixture."""
import argparse
import contextlib
import errno
import http.client
import io
import json
import math
import os
from pathlib import Path
import signal
import socket
import statistics
import subprocess
import time

import notes_smoke as notes


MAX_SAMPLES = 20000


class SoakClock:
    """Monotonic sampling time, with independent gap/suspend evidence."""

    def __init__(self, interval_seconds):
        self.max_gap_seconds = max(90, interval_seconds * 3)
        self.boot_clock = (getattr(time, "CLOCK_BOOTTIME", None)
                           if hasattr(time, "clock_gettime") else None)
        before, now, wall, boot = self._read(initial=True)
        self.started = self.previous_monotonic = now
        self.previous_sample = now
        self.previous_wall = wall
        self.previous_boot = boot
        # Bracket the boot/monotonic offset so scheduler delay between clock
        # reads cannot masquerade as suspension. Tighten this bound as we go.
        self.boot_offset_upper = None if boot is None else boot - before

    def _read(self, initial=False):
        before = time.monotonic()
        wall = time.time()
        boot = None
        if self.boot_clock is not None:
            try:
                boot = time.clock_gettime(self.boot_clock)
            except (OSError, ValueError) as exc:
                unsupported = (isinstance(exc, ValueError) or
                               exc.errno in (errno.EINVAL, errno.ENOSYS, errno.ENOTSUP))
                if not initial or not unsupported:
                    raise InterruptedError("lost CLOCK_BOOTTIME evidence; continuity is not proven") from exc
                self.boot_clock = None
        now = time.monotonic()
        if not all(math.isfinite(value) for value in (before, now, wall)
                   + (() if boot is None else (boot,))):
            raise InterruptedError("invalid clock evidence; continuity is not proven")
        return before, now, wall, boot

    def check(self):
        before, now, wall, boot = self._read()
        if (now < self.previous_monotonic or before > now
                or now - self.previous_monotonic > self.max_gap_seconds
                or now - self.previous_sample > self.max_gap_seconds
                or wall - self.previous_wall > self.max_gap_seconds):
            raise InterruptedError("host suspension or sampling gap; continuity is not proven")
        if boot is not None:
            # One microsecond accommodates floating-point clock precision, not
            # a sampling interval: even short observed suspends invalidate proof.
            if boot < self.previous_boot or boot - now > self.boot_offset_upper + 0.000001:
                raise InterruptedError("host suspension observed by CLOCK_BOOTTIME vs CLOCK_MONOTONIC; continuity is not proven")
            self.boot_offset_upper = min(self.boot_offset_upper, boot - before)
        self.previous_monotonic, self.previous_wall, self.previous_boot = now, wall, boot
        return now - self.started

    def sample_completed(self):
        elapsed = self.check()
        self.previous_sample = self.started + elapsed
        return elapsed

    def elapsed(self):
        return time.monotonic() - self.started


def daemon_main_pid(unit, scope):
    return subprocess.check_output(
        ["systemctl", "--" + scope, "show", unit, "-p", "MainPID", "--value"],
        text=True, timeout=5).strip()


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
    parser.add_argument("--daemon-scope", choices=("user", "system"), default="user",
                        help="systemd manager owning the observed controller")
    parser.add_argument("--reads-per-sample", type=int, default=3)
    args = parser.parse_args()
    if (not 1 <= args.duration_seconds <= 14400
            or not math.isfinite(args.interval_seconds) or args.interval_seconds < 1):
        parser.error("duration must be 1..14400 seconds and interval finite and >=1")
    if math.ceil(args.duration_seconds / args.interval_seconds) + 1 > MAX_SAMPLES:
        parser.error("at most 20000 samples, including the terminal sample, are allowed; increase the interval")
    if not 1 <= args.reads_per_sample <= 16:
        parser.error("reads per sample must be 1..16")
    args.output.mkdir(parents=True, exist_ok=False)
    notes.PORT = args.port
    status = {"state": "starting", "target_seconds": args.duration_seconds,
              "purpose": "acceptance-session" if args.duration_seconds >= 7200 else "runner-smoke",
              "started_unix": time.time(), "requests": 0, "errors": 0,
              "sampled_elapsed_seconds": 0.0, "elapsed_seconds": 0.0, "total_elapsed_seconds": 0.0,
              "samples": 0, "terminal_sample_verified": False,
              "max_data_bytes": 4 * 1024 * 1024}
    latencies = []
    fixture = None
    soak_clock = None
    def save():
        status["updated_unix"] = time.time()
        if soak_clock is not None:
            # Total time since sampling began includes restore and cleanup;
            # sampled_elapsed_seconds is advanced only by successful samples.
            status["total_elapsed_seconds"] = soak_clock.elapsed()
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
        # Sleeps are capped at the remaining target duration. Use that same
        # finite bound for continuity, even for an enormous finite CLI interval.
        soak_clock = SoakClock(min(args.interval_seconds, args.duration_seconds))
        status["suspend_detection_clock"] = "CLOCK_BOOTTIME" if soak_clock.boot_clock is not None else None
        if args.duration_seconds >= 7200 and soak_clock.boot_clock is None:
            raise InterruptedError("acceptance session requires CLOCK_BOOTTIME suspension evidence")
        expected = "first\n"
        sequence = 0
        previous_daemon = previous_runtime = None
        daemon_restarts = runtime_restarts = 0
        status["state"] = "running"
        with (args.output / "samples.jsonl").open("a", buffering=1) as samples:
            while True:
                sample_started = soak_clock.check()
                if sequence >= MAX_SAMPLES:
                    raise InterruptedError("sample bound exhausted before terminal evidence; continuity is not proven")
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
                daemon_pid = daemon_main_pid(args.daemon_unit, args.daemon_scope)
                daemon_metrics = process_resources(daemon_pid)
                runtime_metrics = runtime_resources(args.runtime_socket, service["runtime_id"])
                daemon_identity = (daemon_metrics["pid"], daemon_metrics["start_ticks"])
                runtime_identity = (runtime_metrics["pid"], runtime_metrics["start_ticks"])
                if previous_daemon is not None and daemon_identity != previous_daemon:
                    daemon_restarts += 1
                if previous_runtime is not None and runtime_identity != previous_runtime:
                    runtime_restarts += 1
                previous_daemon, previous_runtime = daemon_identity, runtime_identity
                sampled_elapsed = soak_clock.sample_completed()
                terminal_sample = sample_started >= args.duration_seconds
                # Only completed, successful samples advance sampled operation.
                # Never round this duration up to the requested target.
                status.update(sampled_elapsed_seconds=sampled_elapsed, elapsed_seconds=sampled_elapsed,
                              sample_started_elapsed_seconds=sample_started,
                              total_elapsed_seconds=sampled_elapsed, samples=sequence,
                              terminal_sample_verified=terminal_sample,
                              data_bytes=data_bytes, runtime_id=service["runtime_id"], daemon=daemon,
                              daemon_resources=daemon_metrics, runtime_resources=runtime_metrics,
                              daemon_restarts=daemon_restarts, runtime_restarts=runtime_restarts,
                              latency_p50_ms=round(statistics.median(latencies), 3),
                              latency_p95_ms=round(sorted(latencies)[int((len(latencies)-1)*.95)],3),
                              latency_max_ms=round(max(latencies),3))
                samples.write(json.dumps(status) + "\n")
                os.fsync(samples.fileno())
                save()
                if terminal_sample:
                    break
                time.sleep(min(args.interval_seconds, max(0, args.duration_seconds-soak_clock.elapsed())))
        if not status["terminal_sample_verified"] or status["sampled_elapsed_seconds"] < args.duration_seconds:
            raise InterruptedError("missing successful terminal sample; continuity is not proven")
        soak_clock.check()
        notes.api("POST", f'/volumes/{fixture["data_volume"]}/restore', {"backup_id": fixture["backup"]})
        notes.expect("first\n")
        soak_clock.check()
        status.update(state="passed", restore_verified=True)
        save()
    except BaseException as exc:
        status.update(state="interrupted" if isinstance(exc,InterruptedError) else "failed",
                      errors=status["errors"]+1, failure=str(exc))
        save()
        raise
    finally:
        completion_interruption = None
        if fixture:
            try:
                notes.api("POST", f'/services/{fixture["service"]}/stop')
            except Exception as exc:
                status["cleanup_error"] = str(exc)
                if status["state"] == "passed":
                    status["state"] = "failed"
                    status["errors"] += 1
                save()
        if soak_clock is not None:
            if status["state"] == "passed":
                try:
                    soak_clock.check()
                except InterruptedError as exc:
                    status.update(state="interrupted", errors=status["errors"]+1, failure=str(exc))
                    completion_interruption = exc
            save()
        print(json.dumps(status), flush=True)
        if completion_interruption is not None:
            raise completion_interruption
        if status.get("cleanup_error") and status["state"]=="failed":
            raise RuntimeError("soak fixture cleanup failed; inspect the retained status")


if __name__ == "__main__":
    main()
