import ast
import hashlib
import json
from pathlib import Path
import sys
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import vm_acceptance as vm


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
        self.assertEqual(status['state'], 'preparing')
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
        launch = next(node for node in calls if isinstance(node.func, ast.Attribute) and node.func.attr == 'Popen')
        self.assertLess(inventory.lineno, launch.lineno)
        self.assertTrue(any(isinstance(node.func, ast.Name) and node.func.id == 'network_arguments' for node in calls))
        text = ast.get_source_segment(Path(vm.__file__).read_text(), main)
        for retained in ('ready(boot)', 'ready(new_boot)', 'process.kill(); process.wait(timeout=10)',
                         'status["checks"]["fresh_install"] = "passed"',
                         'status["checks"]["guest_reboot"] = "passed"',
                         'status["checks"]["abrupt_vm_shutdown"] = "passed"',
                         'if last == "active\\nactive\\n" + expected_data:',
                         'completed gates require explicit --reverify-installed',
                         'source_inputs = capture_source_inputs(args.allow_dirty)',
                         'verify_source_inputs(source_inputs)'):
            self.assertIn(retained, text)


if __name__ == '__main__':
    unittest.main()
