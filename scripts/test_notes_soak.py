import unittest
from unittest.mock import patch
import notes_soak


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
