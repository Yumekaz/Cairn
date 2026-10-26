"""Hermetic queue/evidence tests: synthetic files, fake clocks, no real child."""
import copy
import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import soak_session_queue as queue


class Clock:
    def __init__(self):
        self.wall = 1800000000.0
        self.mono = 100.0
        self.sleeps = []
        self.on_sleep = None

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.wall += seconds
        self.mono += seconds
        if self.on_sleep:
            self.on_sleep()


class QueueTests(unittest.TestCase):
    def setUp(self):
        # TemporaryDirectory is synthetic evidence only, never an app fixture.
        temporary = tempfile.TemporaryDirectory(prefix="queue-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.validation = self.root / "validation-runs"
        self.first = self.validation / "explicit-first" / "status.json"
        self.first.parent.mkdir(parents=True)
        self.second = self.validation / "explicit-second"
        self.clock = Clock()
        for target, replacement in (("ROOT", self.root), ("VALIDATION_ROOT", self.validation)):
            self.enterContext(patch.object(queue, target, replacement))
        self.enterContext(patch.object(queue.time, "time", lambda: self.clock.wall))
        self.enterContext(patch.object(queue.time, "monotonic", lambda: self.clock.mono))
        self.enterContext(patch.object(queue.time, "sleep", self.clock.sleep))
        # There is no path in this suite which executes notes_soak or systemctl.
        self.child = self.enterContext(patch.object(queue.subprocess, "run", return_value=SimpleNamespace(returncode=0)))
        self.original_writer_probe = queue.first_writer_running
        self.writer_probe = self.enterContext(patch.object(queue, "first_writer_running", return_value=False))
        self.status, self.samples = self.evidence()
        self.save()

    def evidence(self, target=7200, restart=False):
        records = []
        started = self.clock.wall - target - 120
        elapsed_points = [0.1, target / 2, target + 0.1]
        for i, elapsed in enumerate(elapsed_points, 1):
            start = max(0, elapsed - 0.1)
            changed = restart and i >= 2
            metrics = {"pid": 123 + int(changed), "start_ticks": 100 + int(changed),
                       "rss_kib": 4096, "cpu_seconds": (i - 1) * 0.5 if changed else i * 0.5}
            records.append({
                "state": "running", "purpose": "acceptance-session", "target_seconds": target,
                "started_unix": started, "updated_unix": started + elapsed,
                "sampled_elapsed_seconds": elapsed, "elapsed_seconds": elapsed,
                "total_elapsed_seconds": elapsed, "sample_started_elapsed_seconds": start,
                "samples": i, "requests": i * 4, "errors": 0,
                "terminal_sample_verified": i == 3, "suspend_detection_clock": "CLOCK_BOOTTIME",
                "max_data_bytes": queue.DATA_BUDGET, "data_bytes": i * 32,
                "fixture": {"service": "own-notes", "data_volume": "own-data", "backup": "own-backup"},
                "runtime_id": "own-runtime", "daemon_resources": metrics,
                "runtime_resources": {"pid": 456, "start_ticks": 50, "rss_kib": 8192, "cpu_seconds": i},
                "daemon_restarts": int(changed), "runtime_restarts": 0})
        status = copy.deepcopy(records[-1])
        status.update(state="passed", restore_verified=True, total_elapsed_seconds=target + 90,
                      updated_unix=started + target + 90)
        return status, records

    def save(self):
        self.first.write_text(json.dumps(self.status))
        self.sample_path.write_text("".join(json.dumps(sample) + "\n" for sample in self.samples))

    @property
    def sample_path(self):
        return self.first.parent / "samples.jsonl"

    def argv(self, *extra):
        return ["--first-status", str(self.first), "--second-output", str(self.second),
                "--port", "18099", "--interval-seconds", "5", "--reads-per-sample", "9",
                "--runtime-socket", "/explicit/runtime.sock", "--daemon-unit", "explicit-daemon.service",
                "--daemon-scope", "system", "--wait-timeout", "120", *extra]

    def assert_refused(self):
        self.save()
        with self.assertRaises((ValueError, OSError, TimeoutError)):
            queue.main(self.argv())
        self.child.assert_not_called()
        self.assertFalse(self.second.exists())
        self.assertFalse(queue.claim_path(self.second).exists())

    def test_completed_explicit_run_resumes_even_when_old_and_forwards_all_flags(self):
        self.clock.wall += 86400
        before = (self.first.read_bytes(), self.sample_path.read_bytes())
        self.assertEqual(queue.main(self.argv()), 0)
        expected = [queue.sys.executable, str(self.root / "scripts/notes_soak.py"),
                    "--output", str(self.second), "--port", "18099", "--duration-seconds", "7200",
                    "--interval-seconds", "5.0", "--reads-per-sample", "9",
                    "--runtime-socket", "/explicit/runtime.sock", "--daemon-unit", "explicit-daemon.service",
                    "--daemon-scope", "system"]
        self.child.assert_called_once_with(expected, cwd=self.root, timeout=7800)
        self.assertFalse(self.second.exists(), "queue must let notes_soak create output exclusively")
        self.assertEqual(before, (self.first.read_bytes(), self.sample_path.read_bytes()))
        claim = json.loads(queue.claim_path(self.second).read_text())
        self.assertEqual(claim["command"], expected)
        self.assertEqual(self.clock.sleeps, [])

    def test_four_hour_first_target_and_explicit_second_duration_are_independent(self):
        self.status, self.samples = self.evidence(14400)
        self.save()
        self.assertEqual(queue.main(self.argv("--duration-seconds", "10800", "--daemon-scope", "user")), 0)
        args = self.child.call_args.args[0]
        self.assertEqual(args[args.index("--duration-seconds") + 1], "10800")
        self.assertEqual(args[args.index("--daemon-scope") + 1], "user")
        self.assertEqual(self.child.call_args.kwargs["timeout"], 11400)

    def test_actual_notes_soak_evidence_is_accepted_with_all_external_operations_mocked(self):
        # Reuse the existing writer's hermetic harness: execute its actual save/
        # sample/restore logic with fake APIs, CPU metrics and clocks, not services.
        import test_notes_soak
        writer = test_notes_soak.SoakEvidenceTests("test_two_hour_target_is_proven_without_real_waits")
        writer.setUp()
        try:
            writer.run_soak("--duration-seconds", "7200", "--interval-seconds", "15")
            self.first.write_bytes((writer.output / "status.json").read_bytes())
            self.sample_path.write_bytes((writer.output / "samples.jsonl").read_bytes())
            self.assertEqual(queue.main(self.argv()), 0)
            self.child.assert_called_once()
        finally:
            writer.doCleanups()

    def test_realistic_restart_counter_matches_pid_and_start_ticks(self):
        self.status, self.samples = self.evidence(restart=True)
        self.save()
        self.assertEqual(queue.main(self.argv()), 0)

    def test_child_nonzero_propagates_and_claim_prevents_retry_even_without_output(self):
        self.child.return_value.returncode = 23
        self.assertEqual(queue.main(self.argv()), 23)
        with self.assertRaisesRegex(ValueError, "launch claim"):
            queue.main(self.argv())
        self.assertEqual(self.child.call_count, 1)

    def test_child_timeout_or_spawn_failure_never_retries_or_removes_claim(self):
        for error in (subprocess.TimeoutExpired("notes_soak", 7800), OSError("spawn failed")):
            with self.subTest(error=error):
                self.child.reset_mock()
                self.child.side_effect = error
                with self.assertRaises(type(error)):
                    queue.main(self.argv())
                self.child.assert_called_once()
                self.assertTrue(queue.claim_path(self.second).exists())
                self.second = self.second.with_name(self.second.name + "-next-explicit")

    def test_existing_second_output_is_rejected_before_wait(self):
        self.second.mkdir()
        with patch.object(queue, "wait_for_first") as wait, self.assertRaisesRegex(ValueError, "existing"):
            queue.main(self.argv())
        wait.assert_not_called()
        self.child.assert_not_called()

    def test_output_created_while_waiting_is_not_started_again(self):
        with patch.object(queue, "wait_for_first", side_effect=lambda *args: self.second.mkdir()):
            with self.assertRaisesRegex(ValueError, "existing"):
                queue.main(self.argv())
        self.child.assert_not_called()

    def test_exclusive_claim_race_loser_cannot_spawn(self):
        original = queue.validate_paths
        calls = 0
        def validate(*args):
            nonlocal calls
            result = original(*args)
            calls += 1
            if calls == 3:
                queue.claim_path(self.second).write_text("other queue won")
            return result
        with patch.object(queue, "validate_paths", side_effect=validate), self.assertRaises(FileExistsError):
            queue.main(self.argv())
        self.child.assert_not_called()

    def test_paths_must_be_named_disjoint_and_contained_even_through_symlinks(self):
        outside = self.root / "outside"
        outside.mkdir()
        for bad in (self.root / "escape", self.validation, self.first.parent / "nested-second"):
            with self.subTest(path=bad), self.assertRaises(ValueError):
                queue.validate_paths(self.first, bad)
        link = self.validation / "outside-link"
        link.symlink_to(outside, target_is_directory=True)
        with self.assertRaises(ValueError):
            queue.validate_paths(self.first, link / "new-output")
        dangling = self.validation / "dangling"
        dangling.symlink_to(outside / "missing")
        with self.assertRaises(ValueError):
            queue.validate_paths(self.first, dangling)
        wrong = self.first.with_name("not-status.json")
        wrong.write_text(self.first.read_text())
        with self.assertRaises(ValueError):
            queue.validate_paths(wrong, self.second)
        self.child.assert_not_called()

    def test_status_failures_short_runs_and_boolean_numbers_do_not_count(self):
        baseline = copy.deepcopy(self.status)
        failures = {"state": ["failed", "interrupted", "unknown"], "target_seconds": [7199, 14401, True],
                    "errors": [1, False], "restore_verified": [False, 1],
                    "terminal_sample_verified": [False, 1], "samples": [1, 4, True],
                    "purpose": ["runner-smoke"], "sampled_elapsed_seconds": [7199, float("nan")],
                    "elapsed_seconds": [14400], "cleanup_error": ["", "stop failed"],
                    "failure": ["suspended", ""], "interrupted": [True],
                    "suspend_detection_clock": [None], "data_bytes": [-1, queue.DATA_BUDGET + 1],
                    "daemon_restarts": [-1, 1], "runtime_restarts": [1],
                    "sample_started_elapsed_seconds": [7199]}
        for key, values in failures.items():
            for value in values:
                with self.subTest(key=key, value=value):
                    self.status = copy.deepcopy(baseline)
                    self.status[key] = value
                    self.assert_refused()

    def test_restore_total_cannot_replace_sampled_duration_or_sum_short_runs(self):
        for sample in self.samples:
            sample["sampled_elapsed_seconds"] /= 2
            sample["elapsed_seconds"] /= 2
            sample["sample_started_elapsed_seconds"] /= 2
        self.status.update(sampled_elapsed_seconds=3600, elapsed_seconds=3600, total_elapsed_seconds=15000)
        self.assert_refused()

    def test_missing_cpu_memory_disk_and_restart_fields_are_not_truthy_proof(self):
        baseline = copy.deepcopy(self.status)
        for role in ("daemon", "runtime"):
            for key in ("pid", "start_ticks", "rss_kib", "cpu_seconds"):
                with self.subTest(role=role, key=key):
                    self.status = copy.deepcopy(baseline)
                    del self.status[role + "_resources"][key]
                    self.assert_refused()
            for field in (role + "_resources", role + "_restarts"):
                self.status = copy.deepcopy(baseline)
                del self.status[field]
                self.assert_refused()
        for key in ("data_bytes", "max_data_bytes"):
            self.status = copy.deepcopy(baseline)
            del self.status[key]
            self.assert_refused()

    def test_sample_stream_is_required_owned_complete_and_hashlessly_cross_checked(self):
        self.sample_path.unlink()
        with self.assertRaises(OSError):
            queue.main(self.argv())
        self.save()
        original = self.sample_path.read_bytes()
        for data in (b"", original.rsplit(b"\n", 2)[0] + b"\n", original[:-1], b"{}\n"):
            with self.subTest(data=data[:30]):
                self.sample_path.write_bytes(data)
                with self.assertRaises(ValueError):
                    queue.main(self.argv())
        self.sample_path.unlink()
        elsewhere = self.validation / "different-samples.jsonl"
        elsewhere.write_bytes(original)
        self.sample_path.symlink_to(elsewhere)
        with self.assertRaisesRegex(ValueError, "symlink"):
            queue.main(self.argv())
        self.child.assert_not_called()

    def test_bad_or_foreign_intermediate_sample_rejects_otherwise_good_final_status(self):
        baseline = copy.deepcopy(self.samples)
        changes = [dict(state="failed"), dict(errors=1), dict(target_seconds=14400),
                   dict(started_unix=1), dict(daemon_resources={}), dict(data_bytes=queue.DATA_BUDGET + 1),
                   dict(daemon_restarts=1), dict(samples=1), dict(requests=7),
                   dict(terminal_sample_verified=True), dict(failure="suspension"),
                   dict(fixture={"service": "foreign", "data_volume": "other", "backup": "other"})]
        for change in changes:
            with self.subTest(change=change):
                self.samples = copy.deepcopy(baseline)
                self.samples[1].update(change)
                self.assert_refused()

    def test_cpu_cannot_go_backwards_without_restart(self):
        self.samples[1]["daemon_resources"]["cpu_seconds"] = 0
        self.assert_refused()

    def test_passed_before_final_cleanup_is_not_completion(self):
        self.status["updated_unix"] = self.clock.wall
        self.save()
        self.writer_probe.side_effect = [True, False]
        self.assertEqual(queue.main(self.argv()), 0)
        self.assertEqual(self.clock.sleeps, [5])
        self.child.assert_called_once()

    def test_cleanup_failure_after_transient_passed_does_not_launch(self):
        self.status["updated_unix"] = self.clock.wall
        self.save()
        self.writer_probe.return_value = True
        def cleanup_failure():
            self.status.update(state="failed", errors=1, cleanup_error="stop failed",
                               updated_unix=self.clock.wall)
            self.save()
        self.clock.on_sleep = cleanup_failure
        with self.assertRaises(ValueError):
            queue.main(self.argv())
        self.child.assert_not_called()

    def test_writer_exit_between_status_read_and_probe_rechecks_final_cleanup(self):
        failed = dict(self.status, state="failed", errors=1, cleanup_error="late stop failure")
        with patch.object(queue, "read_status", side_effect=[self.status, failed, failed]):
            with self.assertRaises(ValueError):
                queue.main(self.argv())
        self.child.assert_not_called()

    def test_changing_completed_snapshot_cannot_bypass_wait_deadline(self):
        def changing_status(_):
            self.clock.wall += 1
            self.clock.mono += 1
            return dict(self.status, updated_unix=self.clock.wall)
        with patch.object(queue, "read_status", side_effect=changing_status) as read:
            with self.assertRaises(TimeoutError):
                queue.main(self.argv("--wait-timeout", "2"))
        self.assertLessEqual(read.call_count, 4)
        self.child.assert_not_called()

    def test_process_probe_matches_only_real_writer_and_its_own_output(self):
        proc = self.root / "fake-proc"
        proc.mkdir()
        pid = proc / "123"
        pid.mkdir()
        (pid / "cwd").symlink_to(self.root, target_is_directory=True)
        with patch.object(queue, "PROC_ROOT", proc):
            for args, expected in [
                    (["python3", "scripts/notes_soak.py", "--output", str(self.first.parent)], True),
                    (["python3", str(self.root / "scripts/notes_soak.py"), "--output=" + str(self.first.parent)], True),
                    (["python3", "-m", "scripts.notes_soak", "--output", str(self.first.parent)], True),
                    (["python3", "scripts/notes_soak.py", "--output", str(self.second)], False),
                    (["python3", "other/notes_soak.py", "--output", str(self.first.parent)], False),
                    (["bash", "-c", "python notes_soak.py --output elsewhere"], False)]:
                with self.subTest(args=args):
                    (pid / "cmdline").write_bytes(b"\0".join(os.fsencode(arg) for arg in args) + b"\0")
                    self.assertEqual(self.original_writer_probe(self.first), expected)

    def test_process_inspection_failure_is_not_assumed_complete(self):
        self.writer_probe.side_effect = PermissionError("cannot inspect writer")
        with self.assertRaises(PermissionError):
            queue.main(self.argv())
        self.child.assert_not_called()

    def test_missing_or_invalid_json_never_retries(self):
        for raw in (b"{", b"[]", b'{"state":"failed","state":"passed"}', b'{"errors":NaN}'):
            self.first.write_bytes(raw)
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                queue.main(self.argv())
        self.child.assert_not_called()

    def test_evidence_size_and_non_regular_file_bounds(self):
        with patch.object(queue, "MAX_RECORD_BYTES", 3), self.assertRaises(ValueError):
            queue.main(self.argv())
        with patch.object(queue, "MAX_EVIDENCE_BYTES", 3), self.assertRaises(ValueError):
            queue.main(self.argv())
        self.sample_path.unlink()
        os.mkfifo(self.sample_path)
        with self.assertRaises(ValueError):
            queue.main(self.argv())
        self.child.assert_not_called()

    def running(self):
        status = copy.deepcopy(self.samples[0])
        status["updated_unix"] = self.clock.wall
        return status

    def test_fresh_advancing_first_is_waited_for_then_validated_before_launch(self):
        passed = copy.deepcopy(self.status)
        self.status = self.running()
        self.save()
        def advance():
            self.status = copy.deepcopy(passed)
            self.status["updated_unix"] = self.clock.wall
            self.save()
        self.clock.on_sleep = advance
        self.assertEqual(queue.main(self.argv()), 0)
        self.assertEqual(self.clock.sleeps, [5])

    def test_stale_running_status_is_not_waited_on_or_counted(self):
        self.status = self.running()
        self.status["updated_unix"] -= 91
        self.assert_refused()
        self.assertEqual(self.clock.sleeps, [])

    def test_timestamp_only_heartbeat_does_not_hide_no_sample_progress(self):
        self.status = self.running()
        self.save()
        def heartbeat():
            self.status["updated_unix"] = self.clock.wall
            self.save()
        self.clock.on_sleep = heartbeat
        with self.assertRaisesRegex(ValueError, "not advancing"):
            queue.main(self.argv())
        self.assertEqual(sum(self.clock.sleeps), 90)
        self.child.assert_not_called()

    def test_wait_timeout_is_finite_caps_sleep_and_allows_later_explicit_resume(self):
        passed = copy.deepcopy(self.status)
        self.status = self.running()
        self.save()
        with self.assertRaises(TimeoutError):
            queue.main(self.argv("--wait-timeout", "2"))
        self.assertEqual(self.clock.sleeps, [2])
        self.assertFalse(queue.claim_path(self.second).exists())
        self.status = passed
        self.save()
        self.assertEqual(queue.main(self.argv()), 0)

    def test_failed_or_replaced_run_during_wait_does_not_restart(self):
        for change in (dict(state="failed"), dict(state="interrupted"), dict(started_unix=1),
                       dict(sampled_elapsed_seconds=0, elapsed_seconds=0),
                       dict(fixture={"service": "replacement"})):
            with self.subTest(change=change):
                self.status = self.running()
                self.save()
                def replace_run():
                    self.status.update(change, updated_unix=self.clock.wall)
                    self.save()
                self.clock.on_sleep = replace_run
                with self.assertRaises(ValueError):
                    queue.main(self.argv())
        self.child.assert_not_called()

    def test_required_cli_choices_and_finite_bounds(self):
        with contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                queue.parse_args([])
            for option, values in {"--port": ["0", "65536"], "--duration-seconds": ["1", "14401"],
                                   "--interval-seconds": ["0", "nan", "inf"],
                                   "--reads-per-sample": ["0", "17"], "--wait-timeout": ["0", "-1", "nan", "inf", "18001"],
                                   "--daemon-scope": ["host"], "--runtime-socket": ["relative.sock"],
                                   "--daemon-unit": ["wrong", "bad unit.service"]}.items():
                for value in values:
                    with self.subTest(option=option, value=value), self.assertRaises(SystemExit):
                        queue.parse_args(self.argv(option, value))
        self.child.assert_not_called()


if __name__ == "__main__":
    unittest.main()
