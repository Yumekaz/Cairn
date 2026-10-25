import importlib.util
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('system_install', Path(__file__).with_name('system_install.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallationTests(unittest.TestCase):
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
            create.assert_called_once_with(mode=0o755, parents=True, exist_ok=True)

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
