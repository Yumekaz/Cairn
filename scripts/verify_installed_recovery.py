#!/usr/bin/env python3
"""Restart only installed Cairn daemons, checking one named disposable fixture."""
import argparse
import http.client
import json
import re
import socket
import subprocess
import time
import notes_smoke as notes


def processes(name):
    class Runtime(http.client.HTTPConnection):
        def connect(self):
            self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            self.sock.settimeout(5)
            self.sock.connect('/run/cairn-runtime/mini-docker.sock')
    connection = Runtime('localhost', timeout=5)
    try:
        connection.request('GET', '/containers/json?all=true')
        response = connection.getresponse()
        assert response.status == 200
        return {c['id']: c['pid'] for c in json.loads(response.read())
                if name in c.get('name', '') and c['status'] == 'running'}
    finally:
        connection.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--service', required=True)
    parser.add_argument('--port', required=True, type=int)
    args = parser.parse_args()
    assert re.fullmatch(r'notes-proof-[0-9a-f]{8}', args.service)
    notes.PORT = args.port
    notes.api('POST', f'/services/{args.service}/start')
    try:
        notes.expect('first\n')
        before = processes(args.service)
        assert before, 'no running test workload found'
        for unit in ('cairn-runtime.service', 'cairn.service'):
            subprocess.run(['sudo', 'systemctl', 'restart', unit], check=True, timeout=60)
            # Type=simple activation is not API readiness. Preserve all PID
            # and data assertions after a bounded transport readiness wait.
            deadline = time.monotonic() + 30
            while True:
                try:
                    after = processes(args.service)
                    notes.api('GET', '/status')
                    break
                except (OSError, http.client.HTTPException):
                    if time.monotonic() >= deadline:
                        raise
                    time.sleep(.2)
            notes.expect('first\n')
            after = processes(args.service)
            assert after == before, 'daemon restart replaced/lost the live workload'
            print(json.dumps({'unit': unit, 'result': 'passed', 'data': 'first newline',
                              'unchanged_runtime_processes': after}), flush=True)
    finally:
        notes.api('POST', f'/services/{args.service}/stop')


if __name__ == '__main__':
    main()
