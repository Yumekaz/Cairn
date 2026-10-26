import copy
from pathlib import Path
import re
import subprocess
import unittest
from unittest.mock import patch

import verify_soak_session as verifier


class CompletedSessionTests(unittest.TestCase):
    def test_hosted_bootstrap_evidence_is_already_ignored_without_weakening_source_checks(self):
        root=Path(__file__).resolve().parent.parent
        text=(root/'.github/workflows/bounded-soaks.yml').read_text()
        value=re.search(r'^\s+BOOTSTRAP:\s+(.+)$',text,re.MULTILINE).group(1)
        path=re.sub(r'\$\{\{.*?\}\}','fixture',value)+'/prepare.log'
        self.assertTrue(path.startswith('validation-runs/'))
        self.assertNotIn('verification/',text)
        result=subprocess.run(['git','check-ignore','--no-index','--quiet','--',path],cwd=root)
        self.assertEqual(result.returncode,0,'bootstrap evidence must not dirty the raw source snapshot')
        self.assertIn("'status','--short'",text)

    def setUp(self):
        self.path = Path('/owned/session/status.json')
        self.expected = {name: 'a' * 40 for name in verifier.PROJECTS}
        self.revisions = {name: {'commit': commit, 'working_tree': ''}
                          for name, commit in self.expected.items()}
        self.status = {'target_seconds': 7200, 'sampled_elapsed_seconds': 7200.01,
                       'samples': 481, 'requests': 1924, 'restore_verified': True,
                       'daemon_restarts': 0, 'runtime_restarts': 0}
        self.reader = patch.object(verifier.evidence, 'read_status',
                                 side_effect=lambda path: copy.deepcopy(
                                     self.revisions if path.name == 'revisions.json' else self.status)).start()
        self.writer = patch.object(verifier.evidence, 'first_writer_running', return_value=False).start()
        self.validator = patch.object(verifier.evidence, 'validate_passed').start()
        self.addCleanup(patch.stopall)

    def test_full_shared_validator_and_final_writer_check_are_required(self):
        result = verifier.verify_session(self.path, self.expected)
        self.assertEqual(result['result'], 'passed')
        self.assertEqual(result['source_commits'], self.expected)
        self.validator.assert_called_once_with(self.path, self.status)
        self.writer.assert_called_once_with(self.path)

    def test_shared_resource_restore_duration_failure_cannot_be_overridden(self):
        self.validator.side_effect = ValueError('terminal/resource/restore evidence missing')
        with self.assertRaisesRegex(ValueError, 'terminal/resource/restore'):
            verifier.verify_session(self.path, self.expected)

    def test_still_running_writer_is_not_final_evidence(self):
        self.writer.return_value = True
        with self.assertRaisesRegex(ValueError, 'writer has not exited'):
            verifier.verify_session(self.path, self.expected)
        self.validator.assert_not_called()

    def test_status_mutation_during_finalization_is_rejected(self):
        self.reader.side_effect = [self.status, {**self.status, 'cleanup_error': 'failed'}]
        with self.assertRaisesRegex(ValueError, 'changed during'):
            verifier.verify_session(self.path, self.expected)

    def test_each_unexpected_restart_is_non_green(self):
        for role in ('daemon', 'runtime'):
            with self.subTest(role=role):
                self.status[role + '_restarts'] = 1
                with self.assertRaisesRegex(ValueError, 'restart requires investigation'):
                    verifier.verify_session(self.path, self.expected)
                self.status[role + '_restarts'] = 0

    def test_missing_extra_dirty_and_wrong_repository_evidence_is_rejected(self):
        good = copy.deepcopy(self.revisions)
        bad = [dict(good, extra={'commit': 'a'*40, 'working_tree': ''}),
               {name: item for name, item in good.items() if name != 'SERVER'},
               {**good, 'SERVER': {'commit': 'b'*40, 'working_tree': ''}},
               {**good, 'SERVER': {'commit': 'a'*40, 'working_tree': ' M source'}},
               {**good, 'SERVER': {'commit': 'a'*40}}, {**good, 'SERVER': None}]
        for revisions in bad:
            with self.subTest(revisions=revisions):
                self.revisions = revisions
                with self.assertRaises(ValueError):
                    verifier.verify_session(self.path, self.expected)

    def test_all_six_exact_expected_commits_are_required(self):
        for expected in ({}, {**self.expected, 'SERVER': 'main'},
                         {**self.expected, 'SERVER': 'a'*39},
                         {**self.expected, 'SERVER': None}):
            with self.subTest(expected=expected), self.assertRaises(ValueError):
                verifier.verify_session(self.path, expected)
        self.validator.assert_not_called()


if __name__ == '__main__':
    unittest.main()
