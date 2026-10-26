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
    def test_release_mode_cannot_pass_with_missing_pending_or_failed_hosted_soaks(self):
        for state in ('missing','in_progress','failure','success'):
            with self.subTest(state=state), tempfile.TemporaryDirectory() as temporary:
                output=Path(temporary)/'report'
                def fetch(request):
                    repo=request.full_url.split('/repos/Yumekaz/')[1].split('/')[0]
                    names=next(required for _,remote,required in ci.PROJECTS if remote==repo)
                    runs=[{'id':index+1,'name':name,'head_sha':'a'*40,'status':'completed',
                           'conclusion':'success','html_url':'https://github.com/fixture'}
                          for index,name in enumerate(names)]
                    if repo=='Cairn' and state!='missing':
                        runs.append({'id':99,'name':'bounded-soaks','head_sha':'a'*40,
                                     'status':'in_progress' if state=='in_progress' else 'completed',
                                     'conclusion':None if state=='in_progress' else state,
                                     'html_url':'https://github.com/fixture-soak'})
                    return {'workflow_runs':runs}
                with patch('sys.argv',['snapshot','--require-hosted-soaks','--output',str(output)]), \
                     patch.object(ci.subprocess,'check_output',return_value='a'*40), \
                     patch.object(ci,'fetch_runs',side_effect=fetch):
                    with self.assertRaises(SystemExit) as result:
                        ci.main()
                self.assertEqual(result.exception.code,0 if state=='success' else 1)
                report=json.loads((output/'report.json').read_text())
                self.assertEqual(report['all_exact_commit_checks_passed'],state=='success')
                self.assertTrue(report['hosted_soaks_required'])
        self.assertEqual(ci.PROJECTS[0][2],{'smoke'},'release mode must not mutate ordinary CI requirements')

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
