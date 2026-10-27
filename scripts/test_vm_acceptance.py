import ast
import hashlib
import json
import os
from pathlib import Path
import signal
import stat
import sys
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace
import vm_acceptance as vm


class GuestDataDeadlineTests(unittest.TestCase):
    class Clock:
        now = 0

        def clock(self):
            return self.now

        def sleep(self, duration):
            if not 0 <= duration <= 2:
                raise AssertionError('sleep exceeds remaining bounded probe')
            self.now += duration

    def test_success_inside_whole_probe_deadline(self):
        clock = self.Clock()
        calls = []
        def probe(command, timeout):
            calls.append(timeout)
            clock.now += 59.5
            return 'active\nactive\nfirst\n'
        vm.verify_guest_data(probe, ['cairn.service', 'cairn-runtime.service'], 'first\n', clock.clock, clock.sleep)
        self.assertEqual(calls, [60])

    def test_late_exact_success_is_rejected(self):
        clock = self.Clock()
        def probe(command, timeout):
            self.assertEqual(timeout, 60)
            clock.now += 61
            return 'active\nactive\nfirst\n'
        with self.assertRaisesRegex(AssertionError, '60-second'):
            vm.verify_guest_data(probe, ['cairn.service', 'cairn-runtime.service'], 'first\n', clock.clock, clock.sleep)

    def test_retry_receives_only_remaining_budget(self):
        clock = self.Clock()
        calls = []
        def probe(command, timeout):
            calls.append(timeout)
            if len(calls) == 1:
                clock.now += 57
                raise OSError('not ready')
            clock.now += .5
            return 'active\nactive\nfirst\n'
        vm.verify_guest_data(probe, ['cairn.service', 'cairn-runtime.service'], 'first\n', clock.clock, clock.sleep)
        self.assertEqual(calls, [60, 1])

    def test_wrong_data_exhausts_original_budget_without_new_full_timeout(self):
        clock = self.Clock()
        calls = []
        def probe(command, timeout):
            calls.append(timeout)
            clock.now += 59
            return 'active\nactive\nwrong\n'
        with self.assertRaisesRegex(AssertionError, '60-second'):
            vm.verify_guest_data(probe, ['cairn.service', 'cairn-runtime.service'], 'first\n', clock.clock, clock.sleep)
        self.assertEqual(calls, [60])
        self.assertEqual(clock.now, 60)

    def test_crash_pass_requires_new_acknowledged_post_and_exact_readback(self):
        source = Path(vm.__file__).read_text()
        kill = source.index('process.kill(); process.wait(timeout=10)')
        post = source.index("--data 'after-crash'", kill)
        readback = source.index('verify_data()', post)
        passed = source.index('status["checks"]["abrupt_vm_shutdown"] = "passed"', readback)
        self.assertLess(kill, post)
        self.assertLess(post, readback)
        self.assertLess(readback, passed)


class AccelerationTests(unittest.TestCase):
    def test_tcg_default_is_unchanged_and_does_not_probe_devices(self):
        self.assertEqual(vm.acceleration_arguments('tcg'), ['-accel', 'tcg,thread=multi'])
        with patch.object(vm.os, 'open', side_effect=AssertionError('unexpected device access')):
            self.assertEqual(vm.verify_acceleration('tcg'), {'mode': 'tcg', 'fallback': False})

    def test_kvm_only_uses_existing_device_and_records_kernel_api(self):
        with patch.object(vm.os, 'open', return_value=71) as opened, \
             patch.object(vm.os, 'fstat', return_value=SimpleNamespace(st_mode=stat.S_IFCHR)), \
             patch('fcntl.ioctl', return_value=12) as ioctl, patch.object(vm.os, 'close') as closed:
            self.assertEqual(vm.verify_acceleration('kvm'), {'mode':'kvm','api_version':12,'fallback':False})
            opened.assert_called_once_with('/dev/kvm', vm.os.O_RDWR|vm.os.O_CLOEXEC|vm.os.O_NOFOLLOW)
            ioctl.assert_called_once_with(71,0xAE00)
            closed.assert_called_once_with(71)
        self.assertEqual(vm.acceleration_arguments('kvm'), ['-accel','kvm'])

    def test_missing_device_or_denial_never_silently_falls_back(self):
        for error in (PermissionError(), FileNotFoundError()):
            with self.subTest(error=error), patch.object(vm.os,'open',side_effect=error):
                with self.assertRaisesRegex(RuntimeError,'existing /dev/kvm access'):
                    vm.verify_acceleration('kvm')

    def test_wrong_device_or_api_refuses_and_closes_descriptor(self):
        for mode,version in ((stat.S_IFREG,12),(stat.S_IFCHR,11)):
            with self.subTest(mode=mode,version=version), \
                 patch.object(vm.os,'open',return_value=71), \
                 patch.object(vm.os,'fstat',return_value=SimpleNamespace(st_mode=mode)), \
                 patch('fcntl.ioctl',return_value=version), patch.object(vm.os,'close') as closed:
                with self.assertRaises(RuntimeError): vm.verify_acceleration('kvm')
                closed.assert_called_once_with(71)

    def test_unknown_backend_cannot_inject_options(self):
        for mode in ('auto','kvm:tcg','kvm,thread=single',True,None):
            with self.subTest(mode=mode), self.assertRaises(ValueError): vm.acceleration_arguments(mode)

    def test_kernel_query_error_still_closes_device_without_fallback(self):
        with patch.object(vm.os,'open',return_value=71), \
             patch.object(vm.os,'fstat',return_value=SimpleNamespace(st_mode=stat.S_IFCHR)), \
             patch('fcntl.ioctl',side_effect=OSError('kernel query refused')), \
             patch.object(vm.os,'close') as closed:
            with self.assertRaises(OSError): vm.verify_acceleration('kvm')
            closed.assert_called_once_with(71)

    def test_hardware_option_does_not_change_resource_or_assertion_budgets(self):
        source=(vm.ROOT/'scripts/vm_acceptance.py').read_text()
        guest=(vm.ROOT/'scripts/vm/install_and_prove.sh').read_text()
        self.assertIn('"-cpu", "max", "-smp", "2", "-m", "2048"',source)
        self.assertIn('def ssh(command, timeout=60',source)
        self.assertIn('bin/cairn-integration-tests -test.v -test.timeout 15m',guest)
        self.assertIn('verify_integration_log.py',guest)
        self.assertNotIn('-icount',source)


class SourceFreezeTests(unittest.TestCase):
    def identity(self, path):
        return {'commit': path.name * 3, 'working_tree': '', 'diff_sha256': 'unchanged'}

    def test_clean_sources_are_accepted_and_rechecked(self):
        with patch.object(vm, 'source_revision', side_effect=self.identity):
            inputs = vm.capture_source_inputs()
            self.assertEqual(set(inputs), {'SERVER', 'DURAFLOW', 'Mini-Docker'})
            vm.verify_source_inputs(inputs)

    def test_dirty_runtime_cannot_be_final_revision_proof(self):
        def dirty(path):
            item = self.identity(path)
            if path.name == 'Mini-Docker':
                item['working_tree'] = ' M mini_docker/filesystem.py\n'
            return item
        with patch.object(vm, 'source_revision', side_effect=dirty):
            with self.assertRaisesRegex(RuntimeError, 'requires clean source: Mini-Docker'):
                vm.capture_source_inputs()
            self.assertTrue(vm.capture_source_inputs(allow_dirty=True)['Mini-Docker']['working_tree'])

    def test_mid_build_commit_or_same_status_content_change_is_rejected(self):
        for field in ('commit', 'diff_sha256', 'working_tree'):
            with self.subTest(field=field), patch.object(vm, 'source_revision', side_effect=self.identity):
                inputs = vm.capture_source_inputs()
                inputs['SERVER'][field] = 'different before capture'
                with self.assertRaisesRegex(RuntimeError, 'source changed.*SERVER'):
                    vm.verify_source_inputs(inputs)


class NetworkSeedTests(unittest.TestCase):
    public_key = 'ssh-ed25519 AAAA fixture-key'

    def temporary(self):
        evidence = vm.ROOT / 'validation-runs/vm-network-repair-20261026'
        evidence.mkdir(parents=True, exist_ok=True)
        directory = tempfile.TemporaryDirectory(prefix='fixture-', dir=evidence)
        self.addCleanup(directory.cleanup)
        return Path(directory.name)

    def seed(self, directory):
        commands = []
        def run(argv):
            commands.append(argv)
            self.assertEqual(argv[0], 'cloud-localds')
            (directory / 'seed.img').write_bytes(b'hermetic ISO placeholder, not booted')
        profile = vm.prepare_seed(directory, self.public_key, 'verified-binaries', run)
        return profile, commands

    def test_fresh_seed_passes_network_config_and_pins_seed_artifacts(self):
        directory = self.temporary()
        profile, commands = self.seed(directory)
        self.assertEqual(commands, [[
            'cloud-localds', '-N', str(directory / 'network-config'),
            str(directory / 'seed.img'), str(directory / 'user-data'), str(directory / 'meta-data')]])
        self.assertEqual(profile['id'], 'private-static-v1')
        self.assertEqual(set(profile['seed_files_sha256']), {'network-config', 'user-data', 'meta-data', 'seed.img'})
        for name, digest in profile['seed_files_sha256'].items():
            self.assertEqual(vm.file_sha256(directory / name), digest)

    def test_guest_static_network_disables_dhcp_ra_and_link_local(self):
        rendered = vm.render_network_config()
        for setting in ('version: 2', 'renderer: networkd', 'set-name: cairn0',
                        'macaddress: "52:54:00:ca:17:01"', 'dhcp4: false', 'dhcp6: false',
                        'accept-ra: false', 'link-local: []', '- 10.0.2.15/24',
                        'to: 0.0.0.0/0', 'via: 10.0.2.2', '- 10.0.2.3'):
            self.assertIn(setting, rendered)
        self.assertNotRegex(rendered, r'@[A-Z_]+@')
        self.assertNotIn('gateway6', rendered)

    def test_fresh_qemu_network_is_explicit_with_fixed_mac_and_loopback_forward(self):
        self.assertEqual(vm.network_arguments(vm.select_network_profile(), 23222), [
            '-netdev', 'user,id=net0,ipv4=on,net=10.0.2.0/24,host=10.0.2.2,dns=10.0.2.3,'
            'ipv6=off,hostfwd=tcp:127.0.0.1:23222-10.0.2.15:22',
            '-device', 'virtio-net-pci,netdev=net0,mac=52:54:00:ca:17:01'])
        for port in (0, -1, 65536, True, '23222,hostfwd=tcp::22-:22'):
            with self.subTest(port=port), self.assertRaises(ValueError):
                vm.network_arguments(vm.select_network_profile(), port)
        args = vm.network_arguments(vm.select_network_profile(), 23456)
        self.assertIn('hostfwd=tcp:127.0.0.1:23456-10.0.2.15:22', args[1])

    def test_verified_binary_packages_omit_only_compiler_prerequisites(self):
        source = vm.render_user_data(self.public_key, 'source')
        binaries = vm.render_user_data(self.public_key, 'verified-binaries')
        def packages(text):
            return {line.strip()[2:] for line in text.split('packages:', 1)[1].splitlines()
                    if line.strip().startswith('- ')}
        required = {'python3', 'python3-venv', 'python3-pip', 'curl', 'iproute2', 'iptables', 'procps'}
        self.assertEqual(packages(binaries), required)
        self.assertEqual(packages(source), required | {'git', 'build-essential'})
        self.assertIn('package_update: true', binaries)
        self.assertNotIn('@SOURCE_PACKAGES@', source + binaries)

    def test_public_key_is_quoted_and_newline_injection_rejected(self):
        key = 'ssh-ed25519 AAAA comment: "quoted" # text'
        self.assertIn('  - ' + json.dumps(key), vm.render_user_data(key, 'source'))
        with self.assertRaises(ValueError):
            vm.render_user_data(key + '\nbootcmd: [unsafe]', 'source')

    def test_legacy_resume_keeps_actual_seed_sidecars_and_original_nic_arguments(self):
        directory = self.temporary()
        originals = {'seed.img': b'old DHCP ISO', 'user-data': b'old user data',
                     'meta-data': b'old metadata'}
        for name, data in originals.items():
            (directory / name).write_bytes(data)
        with patch.object(vm, 'prepare_seed', side_effect=AssertionError('seed retrofit')):
            profile = vm.resume_network_profile(directory, {'state': 'failed', 'checks': {}})
        self.assertEqual(profile['id'], 'legacy-default-usernet-v0')
        self.assertEqual(vm.network_arguments(profile, 23222), [
            '-netdev', 'user,id=net0,hostfwd=tcp:127.0.0.1:23222-:22',
            '-device', 'virtio-net-pci,netdev=net0'])
        for name, data in originals.items():
            self.assertEqual((directory / name).read_bytes(), data)
        self.assertFalse((directory / 'network-config').exists())
        self.assertEqual(vm.resume_network_profile(directory, {'network_profile': profile}), profile)

    def test_static_resume_uses_original_files_not_current_template(self):
        directory = self.temporary()
        profile, _ = self.seed(directory)
        before = {name: (directory / name).read_bytes() for name in profile['seed_files_sha256']}
        with patch.object(vm, 'render_network_config', side_effect=AssertionError('regenerated network')):
            resumed = vm.resume_network_profile(directory, {'network_profile': profile})
        self.assertEqual(resumed, profile)
        self.assertEqual(vm.network_arguments(resumed, 23222), vm.network_arguments(profile, 23222))
        for name, data in before.items():
            self.assertEqual((directory / name).read_bytes(), data)

    def test_resume_detects_seed_or_profile_drift_instead_of_retrofitting(self):
        for artifact in ('network-config', 'user-data', 'meta-data', 'seed.img'):
            with self.subTest(artifact=artifact):
                directory = self.temporary()
                profile, _ = self.seed(directory)
                (directory / artifact).write_bytes(b'changed')
                with self.assertRaisesRegex(RuntimeError, 'seed changed'):
                    vm.resume_network_profile(directory, {'network_profile': profile})
        for change in ({'id': 'unknown'}, {'mac': '52:54:00:00:00:02'}, {'host': '0.0.0.0'}):
            with self.subTest(change=change), self.assertRaisesRegex(RuntimeError, 'network profile'):
                vm.select_network_profile({'network_profile': {**vm.STATIC_NETWORK, **change}})

    def test_main_historical_failed_resume_does_not_rewrite_seed(self):
        directory = self.temporary()
        image = directory / 'base.qcow2'
        image.write_bytes(b'fake image: no VM launch')
        digest = hashlib.sha256(image.read_bytes()).hexdigest()
        (directory / 'SHA256SUMS').write_text(digest + ' *base.qcow2\n')
        old = directory / 'old-fixture'
        old.mkdir()
        (old / 'status.json').write_text(json.dumps({'state': 'failed', 'checks': {}, 'image_sha256': digest}))
        artifacts = {'seed.img': b'original ISO', 'user-data': b'original user data',
                     'meta-data': b'original metadata', 'guest-key.pub': b'original key'}
        for name, contents in artifacts.items():
            (old / name).write_bytes(contents)
        class CaptureStopped(BaseException):
            pass
        with patch.object(sys, 'argv', ['vm_acceptance', '--image', str(image), '--output', str(old), '--resume']), \
             patch.object(vm, 'capture_source_inputs', return_value={}), \
             patch.object(vm.socket, 'socket'), \
             patch.object(vm.tarfile, 'open', side_effect=CaptureStopped), \
             patch.object(vm.subprocess, 'run', side_effect=AssertionError('unexpected subprocess')), \
             patch.object(vm.subprocess, 'Popen', side_effect=AssertionError('VM launched')):
            with self.assertRaises(CaptureStopped):
                vm.main()
        for name, contents in artifacts.items():
            self.assertEqual((old / name).read_bytes(), contents)
        self.assertFalse((old / 'network-config').exists())
        self.assertEqual(json.loads((old / 'status.json').read_text())['checks'], {})
        self.assertEqual(json.loads((old / 'status.json').read_text())['network_profile']['id'], 'legacy-default-usernet-v0')
        self.assertEqual(len(list(old.glob('previous-status-*.json'))), 1)

    def test_main_fresh_records_static_seed_profile_before_capture_or_launch(self):
        directory = self.temporary()
        image, out = directory / 'base.qcow2', directory / 'new-fixture'
        image.write_bytes(b'fake checksum-pinned image, never booted')
        (directory / 'SHA256SUMS').write_text(vm.file_sha256(image) + ' *base.qcow2\n')
        class CaptureStopped(BaseException):
            pass
        commands = []
        def run(argv, **kwargs):
            commands.append(argv)
            if argv[0] == 'ssh-keygen':
                Path(argv[-1] + '.pub').write_text(self.public_key)
            elif argv[0] == 'cloud-localds':
                Path(argv[3]).write_bytes(b'hermetic seed placeholder')
            elif argv[:2] == ['qemu-img', 'create']:
                Path(argv[-2]).write_bytes(b'hermetic overlay placeholder')
            else:
                raise AssertionError('unexpected command: ' + repr(argv))
            return subprocess.CompletedProcess(argv, 0, stdout='', stderr='')
        with patch.object(sys, 'argv', ['vm_acceptance', '--image', str(image), '--output', str(out)]), \
             patch.object(vm, 'capture_source_inputs', return_value={}), \
             patch.object(vm.socket, 'socket'), \
             patch.object(vm.tarfile, 'open', side_effect=CaptureStopped), \
             patch.object(vm.subprocess, 'run', side_effect=run), \
             patch.object(vm.subprocess, 'Popen', side_effect=AssertionError('VM launched')):
            with self.assertRaises(CaptureStopped):
                vm.main()
        status = json.loads((out / 'status.json').read_text())
        self.assertEqual(status['state'], 'failed')
        self.assertEqual(status['checks'], {})
        self.assertEqual(status['network_profile']['id'], 'private-static-v1')
        self.assertEqual(status['ssh_forward'], {'host': '127.0.0.1', 'port': 23222, 'guest_port': 22})
        for name, digest in status['network_profile']['seed_files_sha256'].items():
            self.assertEqual(vm.file_sha256(out / name), digest)
        self.assertEqual(next(argv for argv in commands if argv[0] == 'cloud-localds')[1:3],
                         ['-N', str(out / 'network-config')])

    def test_inventory_rejects_live_qemu_not_shell_mentions_or_unrelated_services(self):
        clean = '123 bash sh -c echo qemu-system-x86_64\n124 cairnd cairnd --config /fixture\n'
        with patch.object(Path, 'read_text', return_value='systemd\n'), \
             patch.object(vm.subprocess, 'check_output', return_value=clean):
            self.assertEqual(vm.prelaunch_inventory()['active_qemu'], [])
        with patch.object(Path, 'read_text', return_value='systemd\n'), \
             patch.object(vm.subprocess, 'check_output', return_value=clean + '125 qemu-system-x86 qemu-system-x86_64 -drive old.qcow2\n'):
            with self.assertRaisesRegex(RuntimeError, 'existing QEMU process'):
                vm.prelaunch_inventory()
        with patch.object(Path, 'read_text', return_value='codex\n'), \
             patch.object(vm.subprocess, 'check_output') as inventory:
            with self.assertRaisesRegex(RuntimeError, 'PID namespace'):
                vm.prelaunch_inventory()
            inventory.assert_not_called()

    def test_launch_inventory_and_network_profile_are_used_without_removing_recovery_gates(self):
        tree = ast.parse(Path(vm.__file__).read_text())
        main = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'main')
        start = next(node for node in ast.walk(main) if isinstance(node, ast.FunctionDef) and node.name == 'start_vm')
        calls = list(node for node in ast.walk(start) if isinstance(node, ast.Call))
        inventory = next(node for node in calls if isinstance(node.func, ast.Name) and node.func.id == 'prelaunch_inventory')
        launch = next(node for node in calls if isinstance(node.func, ast.Attribute) and node.func.attr == 'start_process')
        self.assertLess(inventory.lineno, launch.lineno)
        self.assertTrue(any(isinstance(node.func, ast.Name) and node.func.id == 'network_arguments' for node in calls))
        text = ast.get_source_segment(Path(vm.__file__).read_text(), main)
        for retained in ('ready(boot)', 'ready(new_boot)', 'process.kill(); process.wait(timeout=10)',
                         'status["checks"]["fresh_install"] = "passed"',
                         'status["checks"]["guest_reboot"] = "passed"',
                         'status["checks"]["abrupt_vm_shutdown"] = "passed"',
                         'verify_guest_data(ssh, installed_units, expected_data)',
                         'completed gates require explicit --reverify-installed',
                         'source_inputs = capture_source_inputs(args.allow_dirty)',
                         'verify_source_inputs(source_inputs)'):
            self.assertIn(retained, text)


class VMInterruptionTests(unittest.TestCase):
    def lifecycle(self, process=None, save=None):
        status = {'state': 'guest-graceful-reboot', 'checks': {'fresh_install': 'passed'},
                  'source_inputs': {'SERVER': {'commit': 'frozen'}}, 'fixture': {'service': 'owned'}}
        saved = []
        def persist(phase):
            status['state'] = phase
            saved.append(json.loads(json.dumps(status)))
        serial = unittest.mock.Mock()
        return vm.VMRunLifecycle(status, save or persist, lambda: process, lambda: serial), status, saved, serial

    def previous_handlers(self):
        return {sig: signal.getsignal(sig) for sig in (signal.SIGTERM, signal.SIGINT)}

    def assert_handlers(self, previous):
        self.assertEqual(self.previous_handlers(), previous)

    def test_stop_signals_preserve_partial_gates_sources_and_owned_cleanup(self):
        for signum in (signal.SIGTERM, signal.SIGINT):
            with self.subTest(signum=signum):
                process = unittest.mock.Mock()
                process.poll.return_value = None
                lifecycle, status, saved, serial = self.lifecycle(process)
                previous = self.previous_handlers()
                with self.assertRaises(vm.VMInterrupted) as caught:
                    with lifecycle:
                        signal.getsignal(signum)(signum, None)
                self.assertEqual(caught.exception.signum, signum)
                self.assertEqual(status['state'], 'interrupted')
                self.assertEqual(status['checks'], {'fresh_install': 'passed'})
                self.assertEqual(status['source_inputs'], {'SERVER': {'commit': 'frozen'}})
                self.assertEqual(status['interruption']['phase'], 'guest-graceful-reboot')
                self.assertTrue(all(item['state'] != 'passed' for item in saved))
                process.terminate.assert_called_once_with()
                process.wait.assert_called_once_with(timeout=10)
                process.kill.assert_not_called()
                serial.close.assert_called_once_with()
                self.assert_handlers(previous)

    def test_no_owned_process_does_not_signal_a_stale_recorded_pid(self):
        lifecycle, status, _, serial = self.lifecycle()
        status['qemu_pid'] = 123456  # Historical evidence is never signal authority.
        with patch.object(vm.os, 'kill', side_effect=AssertionError('numeric signal forbidden')):
            with self.assertRaises(vm.VMInterrupted):
                with lifecycle:
                    signal.getsignal(signal.SIGTERM)(signal.SIGTERM, None)
        self.assertEqual(status['state'], 'interrupted')
        serial.close.assert_called_once_with()

    def test_evidence_write_failure_cannot_skip_cleanup_or_mask_interruption(self):
        for signum in (signal.SIGTERM, signal.SIGINT):
            with self.subTest(signum=signum):
                process = unittest.mock.Mock()
                process.poll.return_value = None
                save = unittest.mock.Mock(side_effect=OSError('fixture disk full/fsync failure'))
                lifecycle, status, _, serial = self.lifecycle(process, save=save)
                previous = self.previous_handlers()
                with self.assertRaises(vm.VMInterrupted):
                    with lifecycle:
                        signal.getsignal(signum)(signum, None)
                self.assertEqual(save.call_args_list, [unittest.mock.call('interrupted')] * 2)
                process.terminate.assert_called_once_with()
                process.wait.assert_called_once_with(timeout=10)
                serial.close.assert_called_once_with()
                self.assertIn('disk full', status['evidence_error'])
                self.assert_handlers(previous)

    def test_failed_state_write_failure_still_cleans_up_and_preserves_primary_error(self):
        process = unittest.mock.Mock()
        process.poll.return_value = None
        lifecycle, status, _, serial = self.lifecycle(process, save=unittest.mock.Mock(side_effect=PermissionError('fixture denied')))
        previous = self.previous_handlers()
        original = RuntimeError('primary installation failure')
        with self.assertRaises(RuntimeError) as caught:
            with lifecycle:
                raise original
        self.assertIs(caught.exception, original)
        process.terminate.assert_called_once_with()
        serial.close.assert_called_once_with()
        self.assert_handlers(previous)

    def test_terminate_timeout_uses_only_owned_popen_and_original_cleanup_budgets(self):
        process = unittest.mock.Mock()
        process.poll.return_value = None
        process.wait.side_effect = [subprocess.TimeoutExpired('owned-qemu', 10), 0]
        lifecycle, status, _, serial = self.lifecycle(process)
        previous = self.previous_handlers()
        with self.assertRaises(vm.VMInterrupted):
            with lifecycle:
                signal.getsignal(signal.SIGTERM)(signal.SIGTERM, None)
        process.terminate.assert_called_once_with()
        process.kill.assert_called_once_with()
        self.assertEqual(process.wait.call_args_list, [unittest.mock.call(timeout=10)] * 2)
        serial.close.assert_called_once_with()
        self.assert_handlers(previous)

    def test_partial_handler_registration_restores_original_handlers(self):
        lifecycle, _, _, _ = self.lifecycle()
        previous = self.previous_handlers()
        install = signal.signal
        def fail_second(signum, handler):
            if signum == signal.SIGINT and handler == lifecycle.handle_signal:
                raise RuntimeError('fixture registration failure')
            return install(signum, handler)
        with patch.object(vm.signal, 'signal', side_effect=fail_second):
            with self.assertRaisesRegex(RuntimeError, 'registration failure'):
                with lifecycle:
                    self.fail('body must not run')
        self.assert_handlers(previous)

    def test_failed_terminal_pass_write_is_corrected_to_failed_and_still_raised(self):
        process = unittest.mock.Mock()
        process.poll.return_value = 0
        lifecycle, status, saved, serial = self.lifecycle(process)
        original_save = lifecycle.save
        first_error = OSError('fixture terminal fsync failed after publishing')
        calls = []
        def save(phase):
            calls.append(phase)
            original_save(phase)
            if phase == 'passed':
                raise first_error
        lifecycle.save = save
        previous = self.previous_handlers()
        with self.assertRaises(OSError) as caught:
            with lifecycle:
                pass
        self.assertIs(caught.exception, first_error)
        self.assertEqual(calls, ['passed', 'failed'])
        self.assertEqual(status['state'], 'failed')
        self.assertEqual(saved[-1]['state'], 'failed')
        self.assertIn('terminal fsync', status['evidence_error'])
        self.assertIn('terminal evidence write failed', status['failure'])
        serial.close.assert_called_once_with()
        self.assert_handlers(previous)

    def test_signal_during_popen_records_ownership_before_exception(self):
        process = unittest.mock.Mock()
        process.poll.return_value = None
        lifecycle, status, _, serial = self.lifecycle()
        def launch(*args, **kwargs):
            signal.getsignal(signal.SIGTERM)(signal.SIGTERM, None)
            return process
        with patch.object(vm.subprocess, 'Popen', side_effect=launch):
            with self.assertRaises(vm.VMInterrupted):
                with lifecycle:
                    lifecycle.start_process(['fake-owned-qemu'])
        process.terminate.assert_called_once_with()
        process.wait.assert_called_once_with(timeout=10)
        serial.close.assert_called_once_with()
        self.assertEqual(status['state'], 'interrupted')

    def test_signal_during_cleanup_cannot_leave_a_passed_result(self):
        process = unittest.mock.Mock()
        process.poll.return_value = None
        lifecycle, status, _, _ = self.lifecycle(process)
        process.terminate.side_effect = lambda: signal.getsignal(signal.SIGINT)(signal.SIGINT, None)
        with self.assertRaises(vm.VMInterrupted):
            with lifecycle:
                status['checks']['guest_reboot'] = 'passed'
        self.assertEqual(status['state'], 'interrupted')
        self.assertEqual(status['checks']['guest_reboot'], 'passed')

    def test_normal_failure_cleanup_and_success_restore_handlers(self):
        for success in (False, True):
            with self.subTest(success=success):
                process = unittest.mock.Mock()
                process.poll.return_value = 0
                lifecycle, status, _, serial = self.lifecycle(process)
                previous = self.previous_handlers()
                if success:
                    with lifecycle:
                        pass
                else:
                    with self.assertRaisesRegex(RuntimeError, 'original'):
                        with lifecycle:
                            raise RuntimeError('original')
                self.assertEqual(status['state'], 'passed' if success else 'failed')
                process.terminate.assert_not_called()
                serial.close.assert_called_once_with()
                self.assert_handlers(previous)

    def test_real_signal_delivery_is_confined_to_a_separate_test_child(self):
        script = r'''
import json, signal, sys
from unittest.mock import Mock
sys.path.insert(0, sys.argv[1])
from vm_acceptance import VMRunLifecycle, VMInterrupted
status = {'state': 'guest-abrupt-shutdown', 'checks': {'fresh_install': 'passed'}}
prior = {s: signal.getsignal(s) for s in (signal.SIGTERM, signal.SIGINT)}
process = Mock(); process.poll.return_value = None
serial = Mock()
def save(phase): status['state'] = phase
try:
    with VMRunLifecycle(status, save, lambda: process, lambda: serial):
        signal.raise_signal(int(sys.argv[2]))
except VMInterrupted as error:
    assert error.signum == int(sys.argv[2])
else:
    raise AssertionError('signal did not interrupt')
assert status['state'] == 'interrupted' and status['checks'] == {'fresh_install': 'passed'}
assert all(signal.getsignal(s) == handler for s, handler in prior.items())
process.terminate.assert_called_once_with()
process.wait.assert_called_once_with(timeout=10)
serial.close.assert_called_once_with()
print(json.dumps(status))
'''
        for signum in (signal.SIGTERM, signal.SIGINT):
            with self.subTest(signum=signum):
                result = subprocess.run([sys.executable, '-B', '-c', script,
                                         str(Path(vm.__file__).parent), str(signum)],
                                        text=True, capture_output=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(json.loads(result.stdout)['state'], 'interrupted')

    def test_main_real_stop_signal_keeps_partial_proof_and_cleans_owned_fake_qemu(self):
        # Actual signal delivery is confined to this fresh Python test child.
        # Every build/SSH/QEMU operation is replaced; no guest is launched.
        script = r'''
import hashlib, json, os, signal, subprocess, sys, types
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0, sys.argv[1])
import vm_acceptance as vm
baseline = os.environ.get('VM_SIGNAL_TEST_BASELINE_REF')
if baseline:
    source = subprocess.check_output(['git', 'show', baseline + ':scripts/vm_acceptance.py'],
                                    cwd=Path(sys.argv[1]).parent, text=True)
    module = types.ModuleType('vm_acceptance_before')
    module.__file__ = vm.__file__
    exec(compile(source, vm.__file__, 'exec'), module.__dict__)
    vm = module
base = Path(sys.argv[2]); signum = int(sys.argv[3])
root = base / 'SERVER'; (root / 'bin').mkdir(parents=True)
for binary in ('cairn', 'cairnd'):
    (root / 'bin' / binary).write_bytes(b'fake build artifact, not executable')
busybox = base / 'Mini-Docker/rootfs/bin/busybox'
busybox.parent.mkdir(parents=True); busybox.write_bytes(b'fake busybox, never executed')
out = base / 'evidence'; out.mkdir()
image = base / 'image.qcow2'; image.write_bytes(b'fake image, never booted')
digest = hashlib.sha256(image.read_bytes()).hexdigest()
(base / 'SHA256SUMS').write_text(digest + ' *image.qcow2\n')
source_inputs = {name: {'commit': 'frozen-source', 'working_tree': '', 'diff_sha256': 'frozen'}
                 for name in ('SERVER', 'DURAFLOW', 'Mini-Docker')}
previous = {'state': 'failed', 'checks': {}, 'image_sha256': digest,
            'fixture': {'service': 'notes-proof-01234567'},
            'installed_units': ['cairn.service', 'cairn-runtime.service']}
(out / 'status.json').write_text(json.dumps(previous))
(out / 'seed.img').write_bytes(b'fake original seed')
(out / 'source-proof.txt').write_text('preserve this existing proof')
prior = {s: signal.getsignal(s) for s in (signal.SIGTERM, signal.SIGINT)}
data = 'first\n'
class Archive:
    def __enter__(self): return self
    def __exit__(self, *_): pass
    def add(self, *args, **kwargs): pass
class FakeQemu:
    pid = 424242
    exited = False
    def poll(self): return 0 if self.exited else None
    def terminate(self):
        self.exited = True
        (out / 'owned-cleanup.txt').write_text('terminated only actual owned Popen')
    def wait(self, timeout):
        assert timeout == 10
        return 0
    def kill(self): raise AssertionError('unexpected QEMU kill')
process = FakeQemu()
def fake_run(argv, **kwargs):
    global data
    output = ''
    if '--outdir' in argv:
        wheels = Path(argv[argv.index('--outdir') + 1]); wheels.mkdir()
        (wheels / 'fixture.whl').write_bytes(b'fake wheel, never installed')
    elif argv[0] == 'ssh':
        command = argv[-1]
        if command == 'cat /proc/sys/kernel/random/boot_id': output = 'fake-boot\n'
        elif 'http://localhost/services/' in command: output = '{"desired_state":"running"}'
        elif command.startswith('systemctl is-active '): output = 'active\nactive\n' + data
        elif "--data 'before-reboot'" in command: data = 'first\nbefore-reboot\n'
        elif command == 'sudo reboot': signal.raise_signal(signum)
    return subprocess.CompletedProcess(argv, 0, stdout=output, stderr='')
argv = ['vm_acceptance', '--image', str(image), '--output', str(out),
        '--resume', '--resume-installed', '--install-mode', 'verified-binaries']
with patch.object(vm, 'ROOT', root), patch.object(sys, 'argv', argv), \
     patch.object(vm, 'capture_source_inputs', return_value=source_inputs), \
     patch.object(vm, 'verify_source_inputs'), patch.object(vm.socket, 'socket'), \
     patch.object(vm.tarfile, 'open', return_value=Archive()), \
     patch.object(vm.subprocess, 'check_output', return_value=b''), \
     patch.object(vm.subprocess, 'run', side_effect=fake_run), \
     patch.object(vm.subprocess, 'Popen', return_value=process), \
     patch.object(vm, 'prelaunch_inventory', return_value={'active_qemu': []}):
    try:
        vm.main()
    except BaseException:
        pass
    else:
        raise AssertionError('interrupted main returned normally')
assert all(signal.getsignal(s) == handler for s, handler in prior.items())
'''
        for signum in (signal.SIGTERM, signal.SIGINT):
            with self.subTest(signum=signum), tempfile.TemporaryDirectory(prefix='vm-signal-main-') as directory:
                result = subprocess.run([sys.executable, '-B', '-c', script,
                                         str(Path(vm.__file__).parent), directory, str(signum)],
                                        text=True, capture_output=True, timeout=10)
                evidence = Path(directory) / 'evidence'
                status = json.loads((evidence / 'status.json').read_text())
                self.assertEqual(status['state'], 'interrupted', result.stdout + result.stderr)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(status['checks'], {'fresh_install': 'passed'})
                self.assertEqual(status['source_inputs']['SERVER']['commit'], 'frozen-source')
                self.assertEqual(status['interruption']['phase'], 'guest-graceful-reboot')
                self.assertEqual((evidence / 'source-proof.txt').read_text(), 'preserve this existing proof')
                self.assertEqual(len(list(evidence.glob('previous-status-*.json'))), 1)
                self.assertTrue((evidence / 'source-revisions.json').exists())
                self.assertTrue((evidence / 'owned-cleanup.txt').exists())


if __name__ == '__main__':
    unittest.main()
