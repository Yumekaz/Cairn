#!/usr/bin/env python3
"""Queue one explicitly named acceptance soak; never aggregate or retry runs.

First-session evidence is read-only. An exclusive .<output>.queue-claim is kept
after launch, including child failure/timeout, to prevent silent duplicate starts.
Completed first runs may be old; unfinished ones must be fresh and advancing.
"""
import argparse
import json
import math
import os
from pathlib import Path
import stat
import subprocess
import sys
import time


ROOT = Path(__file__).resolve().parent.parent
VALIDATION_ROOT = ROOT / "validation-runs"
MAX_WAIT_SECONDS = 18000
STALE_SECONDS = 90
POLL_SECONDS = 5
MAX_SAMPLES = 20000  # notes_soak.py's actual bound
MAX_RECORD_BYTES = 1024 * 1024
MAX_EVIDENCE_BYTES = 128 * 1024 * 1024
DATA_BUDGET = 4 * 1024 * 1024
PROC_ROOT = Path("/proc")


def first_writer_running(first):
    """Exclude the writer's transient passed-before-cleanup status.

    notes_soak saves passed before its finally/stop path, and saves again after
    cleanup. It has no finalization/PID field. Inspect only processes owned by
    the evidence owner and match the actual script AND that session's output.
    Permission/inspection failures are not permission to launch session two.
    """
    owner = first.stat().st_uid
    script = (ROOT / "scripts/notes_soak.py").resolve()
    count = 0
    for entry in PROC_ROOT.iterdir():
        if not entry.name.isdecimal():
            continue
        count += 1
        if count > 131072:
            raise ValueError("process inspection exceeded its bound")
        try:
            if entry.stat().st_uid != owner:
                continue
            with (entry / "cmdline").open("rb") as stream:
                raw = stream.read(65537)
            if len(raw) > 65536:
                raise ValueError("process command line exceeded its bound")
            argv = [os.fsdecode(arg) for arg in raw.split(b"\0") if arg]
            candidates = [(i, False) for i, arg in enumerate(argv) if Path(arg).name == "notes_soak.py"]
            candidates += [(i, True) for i, arg in enumerate(argv)
                           if i > 0 and argv[i - 1] == "-m" and arg in ("notes_soak", "scripts.notes_soak")]
            if not candidates:
                continue
            cwd = (entry / "cwd").resolve(strict=True)
            for index, module in candidates:
                candidate = Path(argv[index])
                if not module and (cwd / candidate).resolve() != script:
                    continue
                for i in range(index + 1, len(argv)):
                    if argv[i] == "--output" and i + 1 < len(argv):
                        output = argv[i + 1]
                    elif argv[i].startswith("--output="):
                        output = argv[i].split("=", 1)[1]
                    else:
                        continue
                    if (cwd / output).resolve() == first.parent:
                        return True
        except (FileNotFoundError, ProcessLookupError):
            continue  # normal process exit race, including an already-exited writer
    return False


def number(record, key, minimum=0, maximum=None, integer=False):
    value = record.get(key)
    if (isinstance(value, bool) or not isinstance(value, (int, float))
            or not math.isfinite(value) or value < minimum
            or (integer and not isinstance(value, int))
            or (maximum is not None and value > maximum)):
        raise ValueError(f"invalid/missing {key}")
    return value


def _object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON field: {key}")
        result[key] = value
    return result


def decode(raw):
    def invalid_constant(value):
        raise ValueError(f"non-finite JSON constant: {value}")
    value = json.loads(raw, object_pairs_hook=_object, parse_constant=invalid_constant)
    if not isinstance(value, dict):
        raise ValueError("evidence must be a JSON object")
    return value


def regular_file(path, limit):
    info = path.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
        raise ValueError(f"evidence is not a bounded regular file: {path}")


def read_status(path):
    regular_file(path, MAX_RECORD_BYTES)
    with path.open("rb") as stream:
        raw = stream.read(MAX_RECORD_BYTES + 1)
    if len(raw) > MAX_RECORD_BYTES:
        raise ValueError("status exceeded the bounded record size")
    return decode(raw)


def claim_path(output):
    return output.with_name(f".{output.name}.queue-claim")


def validate_paths(first, second):
    root = VALIDATION_ROOT.resolve(strict=True)
    first = first.resolve(strict=True)
    if second.exists() or second.is_symlink():
        raise ValueError("refusing an existing/duplicate second output")
    second = second.resolve()
    if (first.name != "status.json" or first.parent == root or second == root
            or not first.is_relative_to(root) or not second.is_relative_to(root)
            or second.is_relative_to(first.parent) or first.parent.is_relative_to(second)):
        raise ValueError("require disjoint named session paths under SERVER/validation-runs")
    regular_file(first, MAX_RECORD_BYTES)
    if claim_path(second).exists() or claim_path(second).is_symlink():
        raise ValueError("second output already has a launch claim; inspect it, do not retry")
    return first, second


def common_evidence(record):
    if any(key in record for key in ("failure", "cleanup_error", "interruption")) or record.get("interrupted"):
        raise ValueError("failed cleanup or interrupted/failed evidence cannot count")
    if record.get("purpose") != "acceptance-session":
        raise ValueError("first run is not an acceptance session")
    target = number(record, "target_seconds", 7200, 14400, integer=True)
    started = number(record, "started_unix", 1)
    updated = number(record, "updated_unix", started)
    if updated > time.time() + 5:
        raise ValueError("first status timestamp is in the future")
    if number(record, "errors", integer=True) != 0:
        raise ValueError("first run has errors; no retry or duration aggregation")
    samples = number(record, "samples", maximum=MAX_SAMPLES, integer=True)
    requests = number(record, "requests", maximum=MAX_SAMPLES * 17, integer=True)
    sampled = number(record, "sampled_elapsed_seconds")
    if number(record, "elapsed_seconds") != sampled:
        raise ValueError("elapsed_seconds must represent sampled operation, not total/restore time")
    number(record, "total_elapsed_seconds", sampled)
    if not isinstance(record.get("terminal_sample_verified"), bool):
        raise ValueError("missing terminal sample flag")
    if number(record, "max_data_bytes", integer=True) != DATA_BUDGET:
        raise ValueError("unexpected disk data budget")
    return (started, target), updated, (samples, sampled, requests)


def resource_evidence(record):
    if record.get("suspend_detection_clock") != "CLOCK_BOOTTIME":
        raise ValueError("missing acceptance suspension evidence")
    if not isinstance(record.get("runtime_id"), str) or not record["runtime_id"]:
        raise ValueError("missing runtime identity")
    number(record, "data_bytes", maximum=DATA_BUDGET, integer=True)
    for role in ("daemon", "runtime"):
        resource = record.get(role + "_resources")
        if not isinstance(resource, dict):
            raise ValueError(f"missing {role} resource evidence")
        number(resource, "pid", 1, integer=True)
        number(resource, "start_ticks", 1, integer=True)
        number(resource, "rss_kib", 1, integer=True)
        number(resource, "cpu_seconds")
        number(record, role + "_restarts", maximum=MAX_SAMPLES, integer=True)


def fixture_identity(record):
    fixture = record.get("fixture")
    if not isinstance(fixture, dict) or any(not fixture.get(key) for key in ("service", "data_volume", "backup")):
        raise ValueError("missing first run's own fixture identity")
    return fixture


def validate_passed(first, status):
    """Verify each sample against the status, including recorded resource data.

    notes_soak records data_bytes/max_data_bytes, not a numeric free-disk sample;
    that free-space assertion cannot be independently reconstructed here.
    """
    identity, updated, progress = common_evidence(status)
    if status.get("state") != "passed" or status.get("restore_verified") is not True:
        raise ValueError("first run has not passed verified restoration")
    target = identity[1]
    if status["terminal_sample_verified"] is not True or progress[1] < target:
        raise ValueError("missing full sampled duration or successful terminal sample")
    resource_evidence(status)
    fixture = fixture_identity(status)
    samples_path = first.parent / "samples.jsonl"
    if samples_path.is_symlink():
        raise ValueError("sample resource file must belong to the first session, not a symlink")
    regular_file(samples_path, MAX_EVIDENCE_BYTES)
    previous = None
    request_rate = None
    count = 0
    bytes_read = 0
    with samples_path.open("rb") as stream:
        while True:
            raw = stream.readline(MAX_RECORD_BYTES + 1)
            if not raw:
                break
            count += 1
            bytes_read += len(raw)
            if (count > MAX_SAMPLES or len(raw) > MAX_RECORD_BYTES
                    or bytes_read > MAX_EVIDENCE_BYTES or not raw.endswith(b"\n")):
                raise ValueError("unbounded or torn samples resource file")
            sample = decode(raw)
            sample_identity, sample_updated, sample_progress = common_evidence(sample)
            resource_evidence(sample)
            if (sample.get("state") != "running" or sample_identity != identity
                    or fixture_identity(sample) != fixture or sample_progress[0] != count
                    or sample_updated > updated):
                raise ValueError("sample does not belong to the first run's contiguous evidence")
            rate, remainder = divmod(sample_progress[2], count)
            if remainder or not 2 <= rate <= 17 or (request_rate is not None and rate != request_rate):
                raise ValueError("sample request counts do not prove completed bounded reads")
            request_rate = rate
            sample_start = number(sample, "sample_started_elapsed_seconds", maximum=sample_progress[1])
            if sample["terminal_sample_verified"] != (sample_start >= target):
                raise ValueError("terminal flag is not backed by a post-target sample")
            for role in ("daemon", "runtime"):
                metrics = sample[role + "_resources"]
                if previous is None:
                    expected_restarts = 0
                else:
                    old = previous[role + "_resources"]
                    changed = (metrics["pid"], metrics["start_ticks"]) != (old["pid"], old["start_ticks"])
                    expected_restarts = previous[role + "_restarts"] + int(changed)
                    if not changed and metrics["cpu_seconds"] < old["cpu_seconds"]:
                        raise ValueError("CPU evidence regressed without a process restart")
                if sample[role + "_restarts"] != expected_restarts:
                    raise ValueError("process restart counts do not match resource identities")
            if previous is not None:
                if (previous["terminal_sample_verified"] or sample_progress[1] <= previous["sampled_elapsed_seconds"]
                        or sample_start < previous["sampled_elapsed_seconds"]
                        or sample_updated < previous["updated_unix"]):
                    raise ValueError("samples regressed, restarted, or continued after a terminal sample")
            previous = sample
    if count < 2 or count != status["samples"] or previous is None or not previous["terminal_sample_verified"]:
        raise ValueError("missing/extra samples or no terminal resource evidence")
    for key in ("samples", "requests", "sampled_elapsed_seconds", "elapsed_seconds",
                "sample_started_elapsed_seconds", "terminal_sample_verified", "data_bytes",
                "max_data_bytes", "runtime_id", "daemon_resources", "runtime_resources",
                "daemon_restarts", "runtime_restarts", "suspend_detection_clock"):
        if status.get(key) != previous.get(key):
            raise ValueError(f"final status is not backed by the last sample: {key}")


def wait_for_first(first, wait_timeout):
    deadline = time.monotonic() + wait_timeout
    previous = None
    seen_fixture = {}
    last_advance = time.monotonic()
    while True:
        if time.monotonic() > deadline:
            raise TimeoutError("first session exceeded --wait-timeout")
        status = read_status(first)
        state = status.get("state")
        if state in ("failed", "interrupted"):
            raise ValueError("first run failed/interrupted; no retry or duration aggregation")
        identity, updated, progress = common_evidence(status)
        fixture = status.get("fixture", {})
        if not isinstance(fixture, dict):
            raise ValueError("invalid first fixture identity")
        for key in ("service", "data_volume", "backup"):
            if key in seen_fixture and fixture.get(key) != seen_fixture[key]:
                raise ValueError("first status switched fixture identity")
            if key in fixture:
                seen_fixture[key] = fixture[key]
        if previous is not None:
            old_identity, old_updated, old_progress, old_state = previous
            if (identity != old_identity or updated < old_updated
                    or any(new < old for new, old in zip(progress, old_progress))
                    or (old_state in ("running", "passed") and state == "starting")
                    or (old_state == "passed" and state == "running")):
                raise ValueError("first status regressed or switched to a different/retried run")
            if progress != old_progress or state != old_state:
                if updated <= old_updated:
                    raise ValueError("first progress has no advancing timestamp")
                last_advance = time.monotonic()
        if state == "passed" and not first_writer_running(first):
            # The writer may have exited (and changed passed to cleanup-failed)
            # between our status read and process inspection. Validate its final
            # snapshot, never the pre-exit cached record.
            if read_status(first) != status:
                continue
            if previous is not None and time.time() - updated > STALE_SECONDS:
                raise ValueError("watched first status stopped advancing before completion")
            validate_passed(first, status)  # explicitly selected completed runs may be old
            if time.monotonic() > deadline:
                raise TimeoutError("first evidence validation exceeded --wait-timeout")
            return status
        if state not in ("starting", "running", "passed"):
            raise ValueError("unknown first-session state")
        if time.time() - updated > STALE_SECONDS or time.monotonic() - last_advance >= STALE_SECONDS:
            raise ValueError("unfinished first status is stale/not advancing; continuity is not proven")
        if state == "running" and progress[0]:
            resource_evidence(status)
        previous = identity, updated, progress, state
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("first session did not pass within --wait-timeout")
        time.sleep(min(POLL_SECONDS, remaining))


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--first-status", type=Path, required=True)
    parser.add_argument("--second-output", type=Path, required=True)
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--duration-seconds", type=int, default=7200)
    parser.add_argument("--interval-seconds", type=float, required=True)
    parser.add_argument("--reads-per-sample", type=int, required=True)
    parser.add_argument("--runtime-socket", required=True)
    parser.add_argument("--daemon-unit", required=True)
    parser.add_argument("--daemon-scope", choices=("user", "system"), required=True)
    parser.add_argument("--wait-timeout", type=float, default=10800,
                        help="finite queue wait seconds, >0 and <=18000 (default: 10800)")
    args = parser.parse_args(argv)
    if (not 1 <= args.port <= 65535 or not 7200 <= args.duration_seconds <= 14400
            or not math.isfinite(args.interval_seconds) or args.interval_seconds < 1
            or not 1 <= args.reads_per_sample <= 16
            or not math.isfinite(args.wait_timeout) or not 0 < args.wait_timeout <= MAX_WAIT_SECONDS):
        parser.error("require port 1..65535, duration 7200..14400, finite interval >=1, reads 1..16, finite wait (0,18000]")
    if (not Path(args.runtime_socket).is_absolute() or not args.daemon_unit.endswith(".service")
            or args.daemon_unit.startswith("-") or any(c.isspace() for c in args.daemon_unit)):
        parser.error("require an explicit absolute runtime socket and a service unit name")
    return args


def main(argv=None):
    args = parse_args(argv)
    first, second = validate_paths(args.first_status, args.second_output)
    wait_for_first(first, args.wait_timeout)
    command = [sys.executable, str(ROOT / "scripts/notes_soak.py"), "--output", str(second),
               "--port", str(args.port), "--duration-seconds", str(args.duration_seconds),
               "--interval-seconds", str(args.interval_seconds), "--reads-per-sample", str(args.reads_per_sample),
               "--runtime-socket", args.runtime_socket, "--daemon-unit", args.daemon_unit,
               "--daemon-scope", args.daemon_scope]
    first, second = validate_paths(first, second)
    second.parent.mkdir(parents=True, exist_ok=True)
    first, second = validate_paths(first, second)
    with claim_path(second).open("x", encoding="utf-8") as claim:
        json.dump({"first_status": str(first), "second_output": str(second),
                   "command": command, "claimed_unix": time.time(), "queue_pid": os.getpid()}, claim, indent=2)
        claim.flush()
        os.fsync(claim.fileno())
    directory_fd = os.open(second.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory_fd)
    finally:
        os.close(directory_fd)
    # notes_soak atomically creates the output directory (exist_ok=False).
    # No retry after a failed, timed-out, interrupted or ambiguous launch.
    result = subprocess.run(command, cwd=ROOT, timeout=args.duration_seconds + 600)
    return result.returncode


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, TimeoutError, subprocess.SubprocessError) as exc:
        print(f"soak queue refused: {exc}", file=sys.stderr)
        raise SystemExit(1)
