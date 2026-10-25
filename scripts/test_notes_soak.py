import contextlib
import errno
import io
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import notes_soak


class FakeClock:
    def __init__(self):
        self.monotonic_seconds = 100.0
        self.boot_seconds = 1000.0
        self.wall_seconds = 1700000000.0
        self.sleeps = []
        self.on_sleep = None
        self.boot_available = True

    def advance(self, seconds=0, suspend_seconds=0, wall_seconds=None):
        self.monotonic_seconds += seconds
        self.boot_seconds += seconds + suspend_seconds
        self.wall_seconds += seconds + suspend_seconds if wall_seconds is None else wall_seconds

    def monotonic(self):
        return self.monotonic_seconds

    def time(self):
        return self.wall_seconds

    def clock_gettime(self, clock):
        if not self.boot_available:
            raise OSError(errno.EINVAL, 'boot clock unavailable')
        return self.boot_seconds

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.advance(seconds)
        if self.on_sleep:
            self.on_sleep()

    @contextlib.contextmanager
    def patched(self):
        with contextlib.ExitStack() as stack:
            for name in ('monotonic', 'time', 'clock_gettime', 'sleep'):
                stack.enter_context(patch.object(notes_soak.time, name, getattr(self, name), create=True))
            stack.enter_context(patch.object(notes_soak.time, 'CLOCK_BOOTTIME', 7, create=True))
            yield


class SoakClockTests(unittest.TestCase):
    def test_suspend_is_detected_even_if_civil_clock_does_not_move(self):
        clock = FakeClock()
        with clock.patched():
            observed = notes_soak.SoakClock(15)
            clock.advance(15, suspend_seconds=0.25, wall_seconds=0)
            with self.assertRaisesRegex(InterruptedError, 'CLOCK_BOOTTIME vs CLOCK_MONOTONIC'):
                observed.check()

    def test_slow_clock_read_is_not_mistaken_for_suspend(self):
        clock = FakeClock()

        def delayed_boot_read(clock_id):
            clock.advance(2)
            return clock.clock_gettime(clock_id)

        with clock.patched(), patch.object(notes_soak.time, 'clock_gettime', delayed_boot_read):
            observed = notes_soak.SoakClock(15)
            clock.advance(15)
            self.assertEqual(observed.sample_completed(), 17)

    def test_short_suspends_accumulate_against_initial_offset(self):
        clock = FakeClock()
        with clock.patched():
            observed = notes_soak.SoakClock(15)
            clock.advance(15, suspend_seconds=0.0000006)
            observed.sample_completed()
            clock.advance(15, suspend_seconds=0.0000006)
            with self.assertRaises(InterruptedError):
                observed.check()

    def test_unavailable_boot_clock_retains_monotonic_gap_detection(self):
        clock = FakeClock()
        clock.boot_available = False
        with clock.patched():
            observed = notes_soak.SoakClock(15)
            self.assertIsNone(observed.boot_clock)
            clock.advance(91, wall_seconds=0)
            with self.assertRaisesRegex(InterruptedError, 'sampling gap'):
                observed.check()

    def test_lost_boot_clock_evidence_is_an_interruption(self):
        clock = FakeClock()
        with clock.patched():
            observed = notes_soak.SoakClock(15)
            clock.boot_available = False
            with self.assertRaisesRegex(InterruptedError, 'lost CLOCK_BOOTTIME evidence'):
                observed.check()

    def test_initial_clock_read_failure_is_not_treated_as_unsupported(self):
        clock = FakeClock()
        with clock.patched(), patch.object(notes_soak.time, 'clock_gettime',
                                           side_effect=OSError(errno.EIO, 'clock read failed')):
            with self.assertRaisesRegex(InterruptedError, 'CLOCK_BOOTTIME evidence'):
                notes_soak.SoakClock(15)

    def test_absent_boot_clock_still_checks_sampling_gaps(self):
        clock = FakeClock()
        with clock.patched(), patch.object(notes_soak.time, 'CLOCK_BOOTTIME', None):
            observed = notes_soak.SoakClock(15)
            self.assertIsNone(observed.boot_clock)
            clock.advance(91, wall_seconds=0)
            with self.assertRaisesRegex(InterruptedError, 'sampling gap'):
                observed.check()

    def test_intermediate_checks_cannot_hide_gap_between_successful_samples(self):
        clock = FakeClock()
        with clock.patched():
            observed = notes_soak.SoakClock(15)
            clock.advance(50)
            observed.check()
            clock.advance(41, wall_seconds=0)
            with self.assertRaisesRegex(InterruptedError, 'sampling gap'):
                observed.sample_completed()


class SoakEvidenceTests(unittest.TestCase):
    """Exercise main's durable evidence using only temporary files and fakes."""

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.data_path = self.root / 'data'
        self.data_path.mkdir()
        self.output = self.root / 'evidence'
        self.clock = FakeClock()
        self.fixture = {'service': 'fixture-service', 'data_volume': 'fixture-volume', 'backup': 'fixture-backup'}
        self.journal_text = 'first\n'
        self.markers = []
        self.api_calls = []
        self.restore_delay = 0
        self.on_post = None
        self.on_restore = None
        self.on_stop = None
        self.restore_matches = True
        self.stop_error = False
        self.free_bytes = 2 * 1024 * 1024 * 1024

    def smoke(self, on_fixture):
        on_fixture(self.fixture)
        print(json.dumps(self.fixture))

    def api(self, method, route, payload=None):
        self.api_calls.append((method, route, payload))
        if route.endswith('/restore'):
            self.clock.advance(self.restore_delay)
            if self.on_restore:
                self.on_restore()
            self.journal_text = 'first\n' if self.restore_matches else 'wrong restored data\n'
        elif route.endswith('/stop'):
            if self.on_stop:
                self.on_stop()
            if self.stop_error:
                raise RuntimeError('stop failed')
        elif method == 'GET' and route == '/volumes/fixture-volume':
            return {'host_path': str(self.data_path)}
        elif method == 'GET' and route == '/services/fixture-service':
            return {'runtime_id': 'fixture-runtime'}
        return {}

    def journal(self, method='GET', marker=None):
        if method == 'POST':
            self.markers.append((marker, self.clock.monotonic_seconds - 100))
            self.journal_text += marker + '\n'
            if self.on_post:
                self.on_post()
        return self.journal_text

    def expect(self, expected):
        if self.journal_text != expected:
            raise AssertionError('restored journal mismatch')

    def run_soak(self, *extra):
        argv = ['notes_soak.py', '--output', str(self.output),
                '--duration-seconds', '10', '--interval-seconds', '6',
                '--reads-per-sample', '2', *extra]
        with self.clock.patched(), contextlib.ExitStack() as stack:
            stack.enter_context(patch.object(sys, 'argv', argv))
            stack.enter_context(patch.object(notes_soak.signal, 'signal'))
            stack.enter_context(patch.object(notes_soak.os, 'fsync'))
            stack.enter_context(patch.object(notes_soak.os, 'statvfs',
                                            side_effect=lambda _: SimpleNamespace(f_bavail=self.free_bytes, f_frsize=1)))
            stack.enter_context(patch.object(notes_soak.subprocess, 'check_output', return_value='fake-revision\n'))
            stack.enter_context(patch.object(notes_soak.notes, 'PORT', 8099))
            for name in ('main', 'api', 'journal', 'expect'):
                replacement = self.smoke if name == 'main' else getattr(self, name)
                stack.enter_context(patch.object(notes_soak.notes, name, side_effect=replacement))
            daemon_pid = stack.enter_context(patch.object(notes_soak, 'daemon_main_pid', return_value='123'))
            for name, pid in (('process_resources', 123), ('runtime_resources', 456)):
                stack.enter_context(patch.object(notes_soak, name, return_value={
                    'pid': pid, 'start_ticks': 1, 'rss_kib': 100, 'cpu_seconds': 0.5}))
            with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                notes_soak.main()
        return daemon_pid

    def status(self):
        return json.loads((self.output / 'status.json').read_text())

    def samples(self):
        return [json.loads(line) for line in (self.output / 'samples.jsonl').read_text().splitlines()]

    def assert_stopped(self):
        self.assertEqual(self.api_calls[-1], ('POST', '/services/fixture-service/stop', None))

    def test_success_requires_terminal_sample_and_restore_cannot_inflate_sampled_time(self):
        self.restore_delay = 40
        pid = self.run_soak('--daemon-scope', 'system')
        status = self.status()
        samples = self.samples()
        self.assertEqual(status['state'], 'passed')
        self.assertTrue(status['restore_verified'])
        self.assertTrue(status['terminal_sample_verified'])
        self.assertEqual([when for _, when in self.markers], [0, 6, 10])
        self.assertEqual(self.clock.sleeps, [6, 4])
        self.assertEqual([sample['sampled_elapsed_seconds'] for sample in samples], [0, 6, 10])
        self.assertEqual(status['sampled_elapsed_seconds'], 10)
        self.assertEqual(status['total_elapsed_seconds'], 50)
        # Preserve the existing queue/monitor API, but bind its legacy field
        # strictly to sampled operation, never the restore-inclusive total.
        self.assertEqual(status['elapsed_seconds'], status['sampled_elapsed_seconds'])
        self.assertLess(status['elapsed_seconds'], status['total_elapsed_seconds'])
        self.assertEqual(status['samples'], 3)
        self.assertEqual(status['requests'], 9)
        self.assertEqual(status['errors'], 0)
        self.assertEqual(status['daemon_restarts'], 0)
        self.assertEqual(status['runtime_restarts'], 0)
        self.assertEqual(status['suspend_detection_clock'], 'CLOCK_BOOTTIME')
        self.assertFalse(samples[-2]['terminal_sample_verified'])
        self.assertTrue(samples[-1]['terminal_sample_verified'])
        self.assertEqual(samples[-1]['sample_started_elapsed_seconds'], 10)
        for call in pid.call_args_list:
            self.assertEqual(call.args, ('cairn-acceptance-live.service', 'system'))
        self.assertIn(('POST', '/volumes/fixture-volume/restore', {'backup_id': 'fixture-backup'}), self.api_calls)
        self.assert_stopped()

    def test_interval_larger_than_duration_still_takes_terminal_sample(self):
        self.run_soak('--interval-seconds', '30')
        self.assertEqual([when for _, when in self.markers], [0, 10])
        self.assertEqual(self.clock.sleeps, [10])
        self.assertTrue(self.status()['terminal_sample_verified'])

    def test_default_scope_still_observes_user_manager(self):
        pid = self.run_soak()
        self.assertEqual(pid.call_count, 3)
        for call in pid.call_args_list:
            self.assertEqual(call.args, ('cairn-acceptance-live.service', 'user'))

    def test_two_hour_target_is_proven_without_real_waits(self):
        self.run_soak('--duration-seconds', '7200', '--interval-seconds', '15')
        status = self.status()
        self.assertEqual(status['state'], 'passed')
        self.assertEqual(status['purpose'], 'acceptance-session')
        self.assertEqual(status['sampled_elapsed_seconds'], 7200)
        self.assertEqual(status['samples'], 481)
        self.assertEqual(len(self.samples()), 481)
        self.assertEqual(self.markers[-1][1], 7200)
        self.assertEqual(sum(self.clock.sleeps), 7200)

    def test_maximum_duration_and_reads_remain_bounded(self):
        self.run_soak('--duration-seconds', '14400', '--interval-seconds', '3600', '--reads-per-sample', '16')
        status = self.status()
        self.assertEqual(status['state'], 'passed')
        self.assertEqual(status['sampled_elapsed_seconds'], 14400)
        self.assertEqual(status['samples'], 5)
        self.assertEqual(status['requests'], 5 * 17)
        self.assertEqual(status['max_data_bytes'], 4 * 1024 * 1024)

    def test_completed_pre_target_sample_is_not_rounded_up(self):
        self.on_post = lambda: self.clock.advance(9.999)
        self.run_soak()
        samples = self.samples()
        self.assertLess(samples[0]['sampled_elapsed_seconds'], 10)
        self.assertFalse(samples[0]['terminal_sample_verified'])
        self.assertGreaterEqual(samples[-1]['sample_started_elapsed_seconds'], 10)
        self.assertEqual(len(samples), 2)

    def test_work_that_crosses_target_still_needs_sample_started_at_target(self):
        self.on_post = lambda: self.clock.advance(11)
        self.run_soak()
        self.assertEqual([when for _, when in self.markers], [0, 11])
        self.assertFalse(self.samples()[0]['terminal_sample_verified'])
        self.assertTrue(self.samples()[-1]['terminal_sample_verified'])

    def test_failed_terminal_read_cannot_use_old_sample_or_restore_as_evidence(self):
        def corrupt_terminal_read():
            if self.clock.monotonic_seconds >= 110:
                self.journal_text = 'corrupt\n'

        self.on_post = corrupt_terminal_read
        self.restore_delay = 80
        with self.assertRaisesRegex(AssertionError, 'journal mismatch at sample 3'):
            self.run_soak()
        status = self.status()
        self.assertEqual(status['state'], 'failed')
        self.assertEqual(status['sampled_elapsed_seconds'], 6)
        self.assertEqual(status['total_elapsed_seconds'], 10)
        self.assertFalse(status['terminal_sample_verified'])
        self.assertEqual(len(self.samples()), 2)
        self.assertFalse(any(route.endswith('/restore') for _, route, _ in self.api_calls))
        self.assert_stopped()

    def test_suspend_with_frozen_civil_clock_is_interrupted(self):
        self.clock.on_sleep = lambda: self.clock.advance(suspend_seconds=5, wall_seconds=0)
        with self.assertRaisesRegex(InterruptedError, 'CLOCK_BOOTTIME vs CLOCK_MONOTONIC'):
            self.run_soak('--duration-seconds', '6')
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertFalse(self.status()['terminal_sample_verified'])
        self.assertEqual(len(self.samples()), 1)
        self.assert_stopped()

    def test_monotonic_sampling_gap_is_interrupted_with_frozen_civil_clock(self):
        self.clock.on_sleep = lambda: self.clock.advance(91, wall_seconds=-6)
        with self.assertRaisesRegex(InterruptedError, 'sampling gap'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertFalse(self.status()['terminal_sample_verified'])
        self.assertEqual(self.status()['sampled_elapsed_seconds'], 0)
        self.assert_stopped()

    def test_extreme_finite_interval_cannot_disable_sampling_gap_detection(self):
        self.clock.on_sleep = lambda: self.clock.advance(91, wall_seconds=-10)
        with self.assertRaisesRegex(InterruptedError, 'sampling gap'):
            self.run_soak('--interval-seconds', '1e308')
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertFalse(self.status()['terminal_sample_verified'])
        self.assert_stopped()

    def test_gap_during_terminal_sample_is_not_persisted_as_success(self):
        def slow_terminal_write():
            if self.clock.monotonic_seconds >= 110:
                self.clock.advance(91, wall_seconds=0)

        self.on_post = slow_terminal_write
        with self.assertRaisesRegex(InterruptedError, 'sampling gap'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertEqual(self.status()['sampled_elapsed_seconds'], 6)
        self.assertFalse(self.status()['terminal_sample_verified'])
        self.assertEqual(len(self.samples()), 2)
        self.assert_stopped()

    def test_suspend_during_terminal_sample_cannot_pass(self):
        def suspend_terminal_write():
            if self.clock.monotonic_seconds >= 110:
                self.clock.advance(suspend_seconds=5, wall_seconds=0)

        self.on_post = suspend_terminal_write
        with self.assertRaisesRegex(InterruptedError, 'CLOCK_BOOTTIME'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertEqual(self.status()['sampled_elapsed_seconds'], 6)
        self.assertFalse(self.status()['terminal_sample_verified'])
        self.assertEqual(len(self.samples()), 2)
        self.assert_stopped()

    def test_suspend_during_restore_cannot_pass(self):
        self.on_restore = lambda: self.clock.advance(suspend_seconds=5, wall_seconds=0)
        with self.assertRaisesRegex(InterruptedError, 'CLOCK_BOOTTIME'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertTrue(self.status()['terminal_sample_verified'])
        self.assertNotIn('restore_verified', self.status())
        self.assertEqual(self.status()['sampled_elapsed_seconds'], 10)
        self.assert_stopped()

    def test_unavailable_boot_clock_is_explicit_and_can_complete_without_gaps(self):
        self.clock.boot_available = False
        self.run_soak()
        self.assertEqual(self.status()['state'], 'passed')
        self.assertIsNone(self.status()['suspend_detection_clock'])

    def test_acceptance_session_cannot_pass_without_suspend_evidence(self):
        self.clock.boot_available = False
        with self.assertRaisesRegex(InterruptedError, 'CLOCK_BOOTTIME suspension evidence'):
            self.run_soak('--duration-seconds', '7200')
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertFalse(self.status()['terminal_sample_verified'])
        self.assertEqual(self.status()['sampled_elapsed_seconds'], 0)
        self.assert_stopped()

    def test_unavailable_boot_clock_retains_civil_gap_fallback(self):
        self.clock.boot_available = False
        self.clock.on_sleep = lambda: self.clock.advance(suspend_seconds=100)
        with self.assertRaisesRegex(InterruptedError, 'sampling gap'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertEqual(self.status()['sampled_elapsed_seconds'], 0)
        self.assert_stopped()

    def test_restore_gap_cannot_pass_or_inflate_sampled_elapsed(self):
        self.restore_delay = 91
        with self.assertRaisesRegex(InterruptedError, 'sampling gap'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertEqual(self.status()['sampled_elapsed_seconds'], 10)
        self.assertEqual(self.status()['total_elapsed_seconds'], 101)
        self.assertNotIn('restore_verified', self.status())
        self.assert_stopped()

    def test_lost_clock_evidence_cannot_pass(self):
        self.clock.on_sleep = lambda: setattr(self.clock, 'boot_available', False)
        with self.assertRaisesRegex(InterruptedError, 'lost CLOCK_BOOTTIME evidence'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertEqual(self.status()['sampled_elapsed_seconds'], 0)
        self.assert_stopped()

    def test_sample_limit_cannot_fall_through_to_restore(self):
        self.clock.on_sleep = lambda: self.clock.advance(-self.clock.sleeps[-1])
        # A stuck clock must exhaust a finite sample budget rather than loop
        # forever or fall through to a restore-backed false pass.
        with patch.object(notes_soak, 'MAX_SAMPLES', 3), self.assertRaisesRegex(InterruptedError, 'sample bound exhausted'):
            self.run_soak('--duration-seconds', '2', '--interval-seconds', '1')
        self.assertEqual(len(self.samples()), 3)
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertFalse(self.status()['terminal_sample_verified'])
        self.assert_stopped()

    def test_planned_sample_budget_includes_terminal_sample(self):
        with patch.object(notes_soak, 'MAX_SAMPLES', 2), self.assertRaises(SystemExit) as error:
            self.run_soak('--duration-seconds', '2', '--interval-seconds', '1')
        self.assertEqual(error.exception.code, 2)
        self.assertFalse(self.output.exists())
        self.assertEqual(self.api_calls, [])

    def test_restore_data_assertion_is_preserved(self):
        self.restore_matches = False
        with self.assertRaisesRegex(AssertionError, 'restored journal mismatch'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'failed')
        self.assertNotIn('restore_verified', self.status())
        self.assert_stopped()

    def test_data_budget_assertion_is_preserved(self):
        with patch.object(notes_soak.Path, 'rglob', return_value=[SimpleNamespace(
                is_file=lambda: True, stat=lambda: SimpleNamespace(st_size=4 * 1024 * 1024 + 1))]):
            with self.assertRaisesRegex(RuntimeError, 'bounded data budget'):
                self.run_soak()
        self.assertEqual(self.status()['state'], 'failed')
        self.assertFalse(self.status()['terminal_sample_verified'])
        self.assert_stopped()

    def test_free_space_assertion_is_preserved(self):
        self.free_bytes = 1024
        with self.assertRaisesRegex(RuntimeError, 'less than 1GiB free'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'failed')
        self.assert_stopped()

    def test_cleanup_failure_cannot_pass(self):
        self.stop_error = True
        with self.assertRaisesRegex(RuntimeError, 'soak fixture cleanup failed'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'failed')
        self.assertEqual(self.status()['errors'], 1)

    def test_suspend_during_cleanup_cannot_leave_passed_status(self):
        self.on_stop = lambda: self.clock.advance(suspend_seconds=5, wall_seconds=0)
        with self.assertRaisesRegex(InterruptedError, 'CLOCK_BOOTTIME'):
            self.run_soak()
        self.assertEqual(self.status()['state'], 'interrupted')
        self.assertEqual(self.status()['errors'], 1)
        self.assertEqual(self.status()['sampled_elapsed_seconds'], 10)
        self.assertTrue(self.status()['restore_verified'])
        self.assert_stopped()

    def test_invalid_or_unbounded_arguments_fail_before_creating_fixture(self):
        for option, value in (('--interval-seconds', 'nan'), ('--interval-seconds', 'inf'),
                              ('--interval-seconds', '-inf'), ('--interval-seconds', '0'),
                              ('--duration-seconds', '0'), ('--duration-seconds', '14401'),
                              ('--reads-per-sample', '0'), ('--reads-per-sample', '17')):
            with self.subTest(option=option, value=value), self.assertRaises(SystemExit) as error:
                self.run_soak(option + '=' + value)
            self.assertEqual(error.exception.code, 2)
            self.assertFalse(self.output.exists())
            self.assertEqual(self.api_calls, [])


class DaemonScopeTests(unittest.TestCase):
    def test_explicit_manager_is_observed_without_changing_services(self):
        for scope in ('user', 'system'):
            with self.subTest(scope=scope), patch.object(notes_soak.subprocess, 'check_output', return_value='123\n') as read:
                self.assertEqual(notes_soak.daemon_main_pid('cairn.service', scope), '123')
                read.assert_called_once_with(
                    ['systemctl', '--' + scope, 'show', 'cairn.service', '-p', 'MainPID', '--value'],
                    text=True, timeout=5)


if __name__ == '__main__':
    unittest.main()
