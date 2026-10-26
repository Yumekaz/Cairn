#!/usr/bin/env python3
"""Live non-root migration/cron identity and timer lifecycle proof.

Creates only its own uniquely named service/volume, keeps data/evidence, and
removes only its own timer registration after stopping the service.
"""
import argparse
import json
import os
from pathlib import Path
import time
import uuid

from notes_smoke import api


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if os.geteuid() == 0:
        parser.error('run as the ordinary controller owner, not root')
    rootfs = os.environ.get('CAIRN_ROOTFS')
    if not rootfs or not Path(rootfs).is_absolute():
        parser.error('explicit installed CAIRN_ROOTFS is required')
    args.output.mkdir(parents=True, exist_ok=False, mode=0o700)
    name = 'identity-proof-' + uuid.uuid4().hex[:8]
    volume_name = name + '-data'
    uid, gid = os.getuid(), os.getgid()
    state = {'state': 'running', 'service': name, 'volume': volume_name,
             'expected_uid': uid, 'expected_gid': gid, 'checks': [],
             'started_unix': time.time(), 'limits': {'marker_files': 4, 'each_bytes': 32}}

    def save():
        temporary = args.output / 'status.pending'
        with temporary.open('w') as stream:
            json.dump(state, stream, indent=2); stream.flush(); os.fsync(stream.fileno())
        temporary.replace(args.output / 'status.json')

    def identity_files(kind):
        return (f'/bin/busybox id -u > /data/{kind}.uid; '
                f'/bin/busybox id -g > /data/{kind}.gid; '
                f'/bin/busybox sync /data/{kind}.uid /data/{kind}.gid /data')

    volume = None
    creation_attempted = False
    save()
    try:
        volume = api('POST', '/volumes', {'name': volume_name, 'mount_path': '/data'})
        data = Path(volume['host_path'])
        if data != Path.home() / '.cairn/volumes' / volume_name:
            raise AssertionError('fixture volume escaped its expected owned directory')
        cfg = {'name': name, 'kind': 'cron', 'image': rootfs, 'user': f'{uid}:{gid}',
               'schedule': '* * * * *', 'run': identity_files('cron'),
               'migration': identity_files('migration'),
               'volumes': [{'name': volume_name, 'mount_path': '/data'}]}
        if any(row['name'] == name for row in (api('GET', '/services') or [])):
            raise AssertionError('refusing to redeploy an existing service with the fixture name')
        # A committed deployment can lose its HTTP acknowledgement. Arm exact
        # name reconciliation before making the request, not after its reply.
        creation_attempted = True
        state['creation_attempted'] = True
        save()
        deployed = api('POST', '/services', cfg)
        state['deployed'] = deployed

        def verify_identity(kind):
            observed = {}
            for field, expected in (('uid', uid), ('gid', gid)):
                marker = data / f'{kind}.{field}'
                if marker.is_symlink() or marker.stat().st_size > 32:
                    raise AssertionError('invalid identity marker')
                actual = marker.read_text()
                if actual != str(expected) + '\n':
                    raise AssertionError(f'{kind} ran with {field}={actual!r}, expected {expected}')
                info = marker.stat()
                if (info.st_uid, info.st_gid) != (uid, gid):
                    raise AssertionError('marker ownership does not match actual configured identity')
                observed[field] = expected
            return observed

        state['checks'].append({'name': 'actual migration UID/GID and file ownership',
                                'state': 'passed', 'observed': verify_identity('migration')})
        save()

        def history():
            rows = api('GET', f'/cron/{name}/history') or []
            if any(row['status'] in ('failed', 'error') or row.get('exit_code') not in (None, 0) for row in rows):
                state['failed_job_history'] = rows; save()
                raise AssertionError('a scheduled identity probe failed; no retry-until-green')
            return rows

        def completed():
            rows = history()
            return [row for row in rows if row['status'] == 'success' and row.get('exit_code') == 0]

        def await_next(previous_count):
            deadline = time.monotonic() + 100
            while time.monotonic() < deadline:
                rows = completed()
                if len(rows) > previous_count:
                    verify_identity('cron')
                    return rows
                time.sleep(2)
            raise TimeoutError('no actual successful scheduled cron execution within 100 seconds')

        first = await_next(0)
        state['checks'].append({'name': 'actual scheduled cron UID/GID and file ownership',
                                'state': 'passed', 'observed': verify_identity('cron'), 'job_runs': first})
        stopped = api('POST', f'/services/{name}/stop')
        if stopped['desired_state'] != 'stopped' or stopped['actual_state'] != 'stopped':
            raise AssertionError('Stop did not pause cron scheduling')
        # Already admitted jobs may finish after Stop. Compare all run IDs,
        # not just completion counts, so those finishes are not false failures.
        admitted = {row['id'] for row in history()}
        deadline = time.monotonic() + 70
        while time.monotonic() < deadline:
            if {row['id'] for row in history()} != admitted:
                raise AssertionError('stopped cron service still launched a scheduled job')
            time.sleep(min(2, max(0, deadline-time.monotonic())))
        before_resume = completed()
        if len(before_resume) != len(admitted):
            raise AssertionError('a pre-Stop identity probe did not finish within the bounded window')
        state['checks'].append({'name': 'Stop blocks future scheduling across a full minute', 'state': 'passed',
                                'observed_seconds': 70, 'unchanged_admitted_run_ids': sorted(admitted)})
        save()
        started = api('POST', f'/services/{name}/start')
        if started['desired_state'] != 'active' or started['actual_state'] != 'active':
            raise AssertionError('Start did not reactivate cron scheduling')
        resumed = await_next(len(before_resume))
        restarted = api('POST', f'/services/{name}/restart')
        if restarted['desired_state'] != 'active' or restarted['actual_state'] != 'active':
            raise AssertionError('Restart did not retain active cron scheduling')
        resumed = await_next(len(resumed))
        state['checks'].append({'name': 'Start resumes scheduling; Restart retains active timer state',
                                'state': 'passed', 'job_runs': resumed})
        state['state'] = 'passed'
    except BaseException as exc:
        state.update(state='failed', failure=str(exc)); raise
    finally:
        errors = []
        outcomes = {}

        def cleanup_step(label, action):
            try:
                value = action()
                outcomes[label] = 'passed'
                return value
            except Exception as exc:
                errors.append({'step': label, 'error': str(exc)})
                outcomes[label] = 'failed'
                return None

        if creation_attempted:
            def reconcile_service():
                matches = [row for row in (api('GET', '/services') or []) if row['name'] == name]
                if not matches:
                    return None
                if len(matches) != 1 or matches[0].get('kind') != 'cron' or not matches[0].get('id'):
                    raise AssertionError('fixture service ownership cannot be confirmed')
                known_id = state.get('deployed', {}).get('id')
                if known_id and matches[0]['id'] != known_id:
                    raise AssertionError('fixture service ownership changed')
                return matches[0]

            owned = cleanup_step('reconcile_service', reconcile_service)
            if owned:
                def check_deploy_terminal():
                    rows = api('GET', f'/services/{name}/deploys') or []
                    if any(row['status'] in ('pending', 'running') for row in rows):
                        raise AssertionError('deployment still in flight; cleanup cannot claim quiescence')

                cleanup_step('deployment_quiescence', check_deploy_terminal)

                def stop_owned():
                    stopped = api('POST', f'/services/{name}/stop')
                    if stopped.get('id') != owned['id'] or stopped.get('desired_state') != 'stopped' or stopped.get('actual_state') != 'stopped':
                        raise AssertionError('owned cron service was not confirmed stopped')

                cleanup_step('stop', stop_owned)

            def remove_owned_timer():
                matches = [row for row in (api('GET', '/cron') or []) if row['name'] == name]
                if not matches:
                    return
                if len(matches) != 1 or not owned or matches[0].get('service_id') != owned['id']:
                    raise AssertionError('fixture timer ownership cannot be confirmed; refusing deletion')
                # Independent of Stop's outcome: removing this exact owned
                # registration also prevents future task admission.
                api('DELETE', f'/cron/{name}')

            cleanup_step('remove_timer', remove_owned_timer)
        state['cleanup_results'] = outcomes
        state['cleanup_errors'] = errors
        if errors:
            state.update(state='failed', cleanup_error='; '.join(f"{item['step']}: {item['error']}" for item in errors))
        else:
            state['cleanup'] = ('owned fixture reconciled: service/timer absent or stopped/removed; volume and evidence retained'
                                if creation_attempted else 'service creation not attempted; volume and evidence retained')
        state['finished_unix'] = time.time(); save()
        print(json.dumps(state), flush=True)
        if state.get('cleanup_error'):
            raise RuntimeError('owned task fixture cleanup failed; inspect saved status')


if __name__ == '__main__':
    main()
