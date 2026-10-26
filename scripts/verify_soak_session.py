#!/usr/bin/env python3
"""Validate one completed session; never retry it or aggregate other durations."""
import argparse
import json
from pathlib import Path
import re

import soak_session_queue as evidence

PROJECTS = {'SERVER', 'Mini-Docker', 'DURAFLOW', 'FAILFORGE',
            'Mini-Redis-Cassandra', 'Coordination-service'}


def verify_session(path, expected):
    if (not isinstance(expected, dict) or set(expected) != PROJECTS or any(not isinstance(commit, str)
            or re.fullmatch(r'[0-9a-f]{40}', commit) is None for commit in expected.values())):
        raise ValueError('require exact expected commits for all six repositories')
    status = evidence.read_status(path)
    if evidence.first_writer_running(path):
        raise ValueError('session writer has not exited; cleanup is not final')
    if evidence.read_status(path) != status:
        raise ValueError('session status changed during final validation')
    evidence.validate_passed(path, status)
    if any(status[role + '_restarts'] != 0 for role in ('daemon', 'runtime')):
        raise ValueError('unexpected process restart requires investigation')
    revisions = evidence.read_status(path.with_name('revisions.json'))
    if set(revisions) != PROJECTS:
        raise ValueError('missing or extra repository provenance')
    for project in PROJECTS:
        item = revisions[project]
        if (not isinstance(item, dict) or item.get('commit') != expected[project]
                or item.get('working_tree') != ''):
            raise ValueError('wrong revision or dirty source: ' + project)
    return {'result': 'passed', 'status_path': str(path),
            'target_seconds': status['target_seconds'],
            'sampled_elapsed_seconds': status['sampled_elapsed_seconds'],
            'samples': status['samples'], 'requests': status['requests'],
            'restore_verified': status['restore_verified'],
            'daemon_restarts': 0, 'runtime_restarts': 0,
            'source_commits': expected}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--status', required=True, type=Path)
    parser.add_argument('--expected-revisions', required=True, type=Path)
    args = parser.parse_args()
    try:
        expected = evidence.read_status(args.expected_revisions)
        print(json.dumps(verify_session(args.status, expected), indent=2))
    except (OSError, ValueError, TypeError, KeyError) as exc:
        parser.exit(1, 'session not accepted: ' + str(exc) + '\n')


if __name__ == '__main__':
    main()
