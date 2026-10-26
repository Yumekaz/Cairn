import importlib.util
import json
import os
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('system_install', Path(__file__).with_name('system_install.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallationTests(unittest.TestCase):
    def test_documented_prefix_is_accepted_without_allowing_broad_paths(self):
        self.assertEqual(installer.check_install_prefix('/opt/cairn'), installer.PREFIX)
        self.assertEqual(installer.check_install_prefix('/usr/local/lib/cairn'), Path('/usr/local/lib/cairn'))
        for value in ('/', '/opt', '/usr', '/opt/other', '/opt/cairn/../other', '/opt/cairn/%h'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                installer.check_install_prefix(value)

    def test_documented_prefix_still_checks_symlinks_and_unsafe_ancestors(self):
        with patch.object(Path, 'is_symlink', autospec=True, side_effect=lambda path: path == Path('/opt')):
            with self.assertRaisesRegex(ValueError, 'symlink'):
                installer.check_install_prefix('/opt/cairn')
        def metadata(path):
            return types.SimpleNamespace(st_uid=0, st_mode=0o40775 if path == Path('/opt') else 0o40755)
        with patch.object(Path, 'lstat', autospec=True, side_effect=metadata), patch.object(Path, 'mkdir') as create:
            with self.assertRaisesRegex(ValueError, 'not root-controlled: /opt'):
                installer.secure_root_directory(installer.check_install_prefix('/opt/cairn'))
            create.assert_not_called()

    def test_verified_outputs_are_private_at_creation_even_under_umask_zero(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source, target = root / 'source', root / 'target'
            source.write_bytes(b'verified bytes')
            observed = []
            original_open = os.open
            def audit_open(path, flags, mode=0o777, **kwargs):
                descriptor = original_open(path, flags, mode, **kwargs)
                if Path(path) == target:
                    observed.append(os.fstat(descriptor).st_mode & 0o777)
                return descriptor
            previous = os.umask(0)
            try:
                with patch.object(installer.os, 'open', side_effect=audit_open):
                    installer.copy_verified(source, target, installer.sha(source), 0o755)
            finally:
                os.umask(previous)
            self.assertEqual(observed, [0o600])
            self.assertEqual(target.stat().st_mode & 0o777, 0o755)
            self.assertEqual(installer.sha(source), installer.sha(target))

    def test_private_install_umask_is_restrictive_and_restored_on_failure(self):
        previous = os.umask(0)
        try:
            with self.assertRaisesRegex(RuntimeError, 'injected'):
                with installer.private_install_umask():
                    active = os.umask(0o077)
                    self.assertEqual(active, 0o077)
                    raise RuntimeError('injected')
            restored = os.umask(0)
            self.assertEqual(restored, 0)
        finally:
            os.umask(previous)

    def test_privileged_venv_uses_isolation_and_controlled_environment(self):
        import ast
        source = Path(installer.__file__).read_text()
        calls = [node for node in ast.walk(ast.parse(source)) if isinstance(node, ast.Call)
                 and isinstance(node.func, ast.Name) and node.func.id == 'run'
                 and node.args and isinstance(node.args[0], ast.List)]
        venv_calls = [node for node in calls if any(isinstance(item, ast.Constant) and item.value == 'venv' for item in node.args[0].elts)]
        self.assertEqual(len(venv_calls), 1)
        argv = venv_calls[0].args[0].elts
        self.assertEqual([item.value for item in argv[:4]], ['/usr/bin/python3', '-I', '-m', 'venv'])
        self.assertTrue({'cwd', 'env'} <= {item.arg for item in venv_calls[0].keywords})
        self.assertEqual(set(installer.privileged_python_environment()), {'PATH', 'LANG'})

    def prebuilt(self, root):
        source, target = root / 'source', root / 'output'
        source.mkdir(); target.mkdir()
        for name in ('cairn', 'cairnd', 'runtime.whl'):
            (source / name).write_bytes(name.encode())
        (source / 'binary-hashes.json').write_text(json.dumps({name: installer.sha(source / name) for name in ('cairn', 'cairnd')}))
        (source / 'wheel-hashes.json').write_text(json.dumps({'runtime.whl': installer.sha(source / 'runtime.whl')}))
        return source, target

    def test_prebuilt_staging_checks_binary_and_wheel_before_execution(self):
        with tempfile.TemporaryDirectory() as directory:
            source, target = self.prebuilt(Path(directory))
            self.assertEqual(installer.stage_prebuilt(source, target), [target / 'runtime.whl'])
            for name in ('cairn', 'cairnd', 'runtime.whl'):
                self.assertEqual((source / name).read_bytes(), (target / name).read_bytes())
            self.assertEqual((target / 'cairn').stat().st_mode & 0o777, 0o755)

    def test_corrupted_prebuilt_binary_and_escaping_wheel_are_rejected(self):
        for kind in ('binary', 'wheel'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as directory:
                source, target = self.prebuilt(Path(directory))
                if kind == 'binary':
                    (source / 'cairn').write_bytes(b'changed')
                else:
                    (source / 'wheel-hashes.json').write_text(json.dumps({'../escape.whl': '0' * 64}))
                with self.assertRaises(ValueError):
                    installer.stage_prebuilt(source, target)

    def test_unsafe_ancestor_is_rejected_before_any_directory_is_created(self):
        def metadata(path):
            return types.SimpleNamespace(st_uid=0, st_mode=0o40775 if str(path) == '/opt' else 0o40755)
        with patch.object(Path, 'lstat', autospec=True, side_effect=metadata), patch.object(Path, 'mkdir') as create:
            with self.assertRaisesRegex(ValueError, 'not root-controlled: /opt'):
                installer.secure_root_directory(Path('/opt/cairn'))
            create.assert_not_called()

    def test_root_controlled_alternative_keeps_the_same_ancestor_checks(self):
        with patch.object(Path, 'lstat', return_value=types.SimpleNamespace(st_uid=0, st_mode=0o40755)), patch.object(Path, 'mkdir') as create:
            installer.secure_root_directory(Path('/usr/local/lib/cairn'))
            create.assert_not_called()  # Existing ancestor permissions are never broadened.

    def test_new_prefix_is_traversable_even_with_private_process_umask(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'prefix'
            original_lstat = Path.lstat
            def metadata(path):
                if path == target:
                    real = original_lstat(path)
                    return types.SimpleNamespace(st_uid=0, st_mode=real.st_mode)
                return types.SimpleNamespace(st_uid=0, st_mode=0o40755)
            with patch.object(Path, 'lstat', autospec=True, side_effect=metadata), installer.private_install_umask():
                installer.secure_root_directory(target)
            self.assertEqual(target.stat().st_mode & 0o777, 0o755)

    def test_existing_private_prefix_is_not_silently_made_public(self):
        with patch.object(Path, 'lstat', return_value=types.SimpleNamespace(st_uid=0, st_mode=0o40700)), patch.object(Path, 'mkdir') as create:
            with self.assertRaisesRegex(ValueError, 'controller traversal'):
                installer.secure_root_directory(Path('/usr/local/lib/cairn'))
            create.assert_not_called()

    def test_root_owned_symlinked_ancestor_is_rejected(self):
        def metadata(path):
            return types.SimpleNamespace(st_uid=0, st_mode=0o120777 if str(path) == '/usr/local' else 0o40755)
        with patch.object(Path, 'lstat', autospec=True, side_effect=metadata), patch.object(Path, 'mkdir') as create:
            with self.assertRaisesRegex(ValueError, 'symlinked'):
                installer.secure_root_directory(Path('/usr/local/lib/cairn'))
            create.assert_not_called()

    def account(self, home='/home/owner'):
        return types.SimpleNamespace(pw_dir=home, pw_uid=1001, pw_gid=1002)

    def config(self):
        return {'data_dir': '/home/owner/.cairn', 'socket_path': '/home/owner/.cairn/cairnd.sock',
                'database_path': '/home/owner/.cairn/cairn.db', 'volume_dir': '/home/owner/.cairn/volumes',
                'backup_dir': '/home/owner/.cairn/backups', 'dashboard_addr': '127.0.0.1:2476'}

    def test_install_keeps_data_paths_and_enables_private_authentication(self):
        cfg = self.config()
        original = dict(cfg)
        installer.validate_config(cfg, self.account())
        for key in ('database_path', 'data_dir', 'volume_dir', 'backup_dir', 'socket_path'):
            self.assertEqual(cfg[key], original[key])
        self.assertEqual(cfg['mini_docker_socket'], installer.SOCKET)
        self.assertEqual(cfg['admin_token_file'], '/home/owner/.cairn/admin.token')

    def test_no_broad_paths_public_listeners_or_external_state(self):
        for key, value in [('volume_dir', '/'), ('backup_dir', '/home/owner'),
                           ('socket_path', '/run/other.sock'), ('data_dir', '/var/lib/cairn'),
                           ('dashboard_addr', '0.0.0.0:2476')]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                installer.validate_config(dict(self.config(), **{key: value}), self.account())

    def test_paths_cannot_inject_units_or_follow_symlinks(self):
        for path in ('/opt/cairn/releases/a\nExecStart=/bin/false', '/opt/cairn/%h',
                     '/opt/cairn/../../etc', '/opt/cairn/a b'):
            with self.subTest(path=path), self.assertRaises(ValueError):
                installer.check_path(path)
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary)
            (path / 'link').symlink_to('/etc')
            with self.assertRaises(ValueError):
                installer.check_path(path / 'link' / 'unit')

    def test_units_run_packaged_code_and_keep_controller_unprivileged(self):
        units = installer.unit_texts('/opt/cairn/releases/abcdef', self.account(), self.config())
        daemon = units['cairn.service']
        runtime = units['cairn-runtime.service']
        self.assertIn('User=1001\n', daemon)
        self.assertIn('ProtectSystem=strict', daemon)
        self.assertIn('ReadWritePaths=/home/owner/.cairn', daemon)
        self.assertIn(' -I -m mini_docker ', runtime)
        self.assertIn('KillMode=process', runtime)
        self.assertIn('Delegate=yes', runtime)
        self.assertNotIn('PYTHONPATH', runtime)
        self.assertNotIn('Desktop', daemon + runtime)

    def test_copy_verifies_bytes_and_rejects_symlinks_and_corruption(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / 'input'
            source.write_bytes(b'original')
            installer.copy_verified(source, root / 'copy', installer.sha(source), 0o755)
            self.assertEqual((root / 'copy').read_bytes(), b'original')
            (root / 'link').symlink_to(source)
            with self.assertRaises(OSError):
                installer.copy_verified(root / 'link', root / 'bad-copy', installer.sha(source), 0o755)
            with self.assertRaisesRegex(ValueError, 'checksum'):
                installer.copy_verified(source, root / 'corrupt', '0' * 64, 0o755)

    def test_atomic_config_replacement_preserves_json_and_private_mode(self):
        with tempfile.TemporaryDirectory() as temporary:
            target = Path(temporary) / 'config'
            installer.atomic(target, json.dumps(self.config()).encode())
            self.assertEqual(json.loads(target.read_text()), self.config())
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
