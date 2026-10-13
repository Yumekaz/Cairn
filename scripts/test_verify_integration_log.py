import unittest
from verify_integration_log import REQUIRED, verify


class IntegrationEvidenceTests(unittest.TestCase):
    def test_complete_named_suite_passes(self):
        verify('\n'.join(f'--- PASS: {name} (0.01s)' for name in REQUIRED)+'\nPASS\n')

    def test_subtests_cannot_replace_missing_top_level_tests(self):
        log='\n'.join(f'    --- PASS: TestReliabilityHardening/subtest{index} (0.01s)' for index in range(12))+'\nPASS\n'
        with self.assertRaises(AssertionError): verify(log)

    def test_any_skip_remains_a_failed_gate(self):
        log='\n'.join(f'--- PASS: {name} (0.01s)' for name in REQUIRED)+'\n    --- SKIP: TestExtra/subtest (0.00s)\nPASS\n'
        with self.assertRaises(AssertionError): verify(log)
