#!/usr/bin/env python3
"""Require each named end-to-end test, not a count inflated by subtests."""
from pathlib import Path
import re
import sys

REQUIRED = {
    'TestEndToEndMLP', 'TestMigrationsAndRollback', 'TestWorkersOneOffAndCron',
    'TestDatabaseServiceDumpAndRestore', 'TestDashboardV1', 'TestDuraFlowCrashRecovery',
    'TestReliabilityHardening', 'TestReverseProxyAndEnvs', 'TestRedisAndMongoBackupRestore',
}


def verify(text):
    passed = set(re.findall(r'^--- PASS: (Test\w+) \(', text, re.MULTILINE))
    assert REQUIRED <= passed, f'Integration tests missing: {sorted(REQUIRED-passed)}'
    assert not re.search(r'^\s*--- (?:SKIP|FAIL):', text, re.MULTILINE), 'integration test skipped or failed'
    assert '\nPASS\n' in text, 'suite did not report PASS'


if __name__ == '__main__':
    verify(Path(sys.argv[1]).read_text())
    print('PASS: all nine required integration tests executed without skips')
