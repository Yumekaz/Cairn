"""Hermetic fault injection: the real harness main, never a live API."""
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import task_identity_acceptance as harness


class IdentityCleanupTests(unittest.TestCase):
    def exercise(self, *, lost=False, stop_error=False, timer_error=False, foreign=False):
        with tempfile.TemporaryDirectory(prefix='identity-harness-test-') as directory:
            home = Path(directory)
            output = home / 'evidence'
            calls = []
            created = {}

            def api(method, route, body=None):
                calls.append((method, route))
                if (method, route) == ('POST', '/volumes'):
                    return {'name': body['name'], 'host_path': str(home / '.cairn/volumes' / body['name'])}
                if (method, route) == ('POST', '/services'):
                    created.update(id='owned-service-id', name=body['name'], kind='cron')
                    if lost:
                        raise TimeoutError('deploy response lost after creation')
                    return dict(created)
                if (method, route) == ('GET', '/services'):
                    return [dict(created)] if created else []
                if method == 'GET' and route.endswith('/deploys'):
                    return [{'id': 'owned-deploy', 'status': 'success'}]
                if (method, route) == ('GET', '/cron'):
                    return [dict(id='owned-timer', name=created['name'], service_id='foreign-id' if foreign else created['id']),
                            dict(id='unrelated-timer', name='unrelated', service_id='unrelated-id')]
                if method == 'POST' and route.endswith('/stop'):
                    if stop_error:
                        raise RuntimeError('injected Stop failure')
                    return dict(created, desired_state='stopped', actual_state='stopped')
                if method == 'DELETE' and route == '/cron/' + created['name']:
                    if timer_error:
                        raise RuntimeError('injected timer removal failure')
                    return None
                raise AssertionError(f'unexpected API call {method} {route}')

            with mock.patch.object(harness, 'api', side_effect=api), \
                    mock.patch.object(Path, 'home', return_value=home), \
                    mock.patch.dict('os.environ', {'CAIRN_ROOTFS': '/mock/rootfs'}), \
                    mock.patch('sys.argv', ['task_identity_acceptance.py', '--output', str(output)]), \
                    contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(Exception):
                    # Without marker files, a successful mocked POST also
                    # exercises main's real failure/finally path immediately.
                    harness.main()
            return calls, json.loads((output / 'status.json').read_text()), dict(created)

    def test_lost_creation_response_still_stops_and_removes_own_timer(self):
        calls, state, service = self.exercise(lost=True)
        self.assertIn(('POST', '/services/' + service['name'] + '/stop'), calls)
        self.assertIn(('DELETE', '/cron/' + service['name']), calls)
        self.assertEqual(state['state'], 'failed')
        self.assertIn('response lost', state['failure'])
        self.assertFalse(state.get('cleanup_errors'))
        self.assertTrue(all('unrelated' not in route for method, route in calls if method != 'GET'))

    def test_stop_failure_does_not_skip_timer_removal(self):
        calls, state, service = self.exercise(stop_error=True)
        self.assertIn(('DELETE', '/cron/' + service['name']), calls)
        self.assertIn('injected Stop failure', json.dumps(state['cleanup_errors']))
        self.assertNotIn('owned service stopped', state.get('cleanup', ''))

    def test_all_cleanup_errors_and_original_failure_are_retained(self):
        _, state, _ = self.exercise(stop_error=True, timer_error=True)
        errors = state['cleanup_errors']
        self.assertEqual(len(errors), 2)
        self.assertIn('injected Stop failure', json.dumps(errors))
        self.assertIn('injected timer removal failure', json.dumps(errors))
        self.assertIn('failure', state)

    def test_foreign_schedule_is_never_deleted(self):
        calls, state, _ = self.exercise(foreign=True)
        self.assertFalse(any(method == 'DELETE' for method, _ in calls))
        self.assertIn('ownership', json.dumps(state['cleanup_errors']))


if __name__ == '__main__':
    unittest.main()
