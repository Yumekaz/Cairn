"""Transport failures must retain every repository and fail closed."""
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import urllib.error

import github_ci_snapshot as ci


class SnapshotTests(unittest.TestCase):
    def test_transient_timeouts_are_bounded_and_retried(self):
        calls, pauses = [], []
        def open_response(request, timeout):
            calls.append(timeout)
            if len(calls) < 3:
                raise TimeoutError('read timeout')
            return io.BytesIO(b'{"workflow_runs": []}')
        self.assertEqual(ci.fetch_runs('request', open_response, pauses.append), {'workflow_runs': []})
        self.assertEqual(calls, [30, 30, 30])
        self.assertEqual(pauses, [1, 2])

    def test_permanent_http_error_is_not_retried(self):
        def denied(request, timeout):
            raise urllib.error.HTTPError('https://api.github.com', 403, 'denied', {}, None)
        with self.assertRaises(urllib.error.HTTPError), patch.object(ci.time, 'sleep') as pause:
            ci.fetch_runs('request', denied, pause)
        pause.assert_not_called()

    def test_failed_fetch_still_records_all_six_repositories(self):
        with tempfile.TemporaryDirectory() as temporary:
            output=Path(temporary)/'report'
            with patch('sys.argv', ['snapshot', '--output', str(output)]), \
                 patch.object(ci.subprocess, 'check_output', return_value='a'*40), \
                 patch.object(ci, 'fetch_runs', side_effect=TimeoutError('read timeout')):
                with self.assertRaises(SystemExit) as result:
                    ci.main()
            self.assertEqual(result.exception.code, 1)
            report=json.loads((output/'report.json').read_text())
            self.assertEqual(len(report['repositories']), 6)
            self.assertFalse(report['all_exact_commit_checks_passed'])
            self.assertTrue(all(row['result']=='not-passed' and row['error'] for row in report['repositories']))
            self.assertTrue((output/'report.md').is_file())


if __name__ == '__main__':
    unittest.main()
