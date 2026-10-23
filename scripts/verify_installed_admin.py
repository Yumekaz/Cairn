#!/usr/bin/env python3
"""Read-only installed admin checks; never prints credentials or changes data."""
import base64
import http.client
import json
from pathlib import Path


def main():
    token = Path.home() / '.cairn/admin.token'
    assert not token.is_symlink() and token.stat().st_mode & 0o077 == 0
    authorization = 'Basic ' + base64.b64encode(b'cairn:' + token.read_bytes().strip()).decode()
    cases = [('missing', {}, 401), ('wrong', {'Authorization': 'Basic Y2Fpcm46d3Jvbmc='}, 401),
             ('authenticated', {'Authorization': authorization}, 200),
             ('cross-origin', {'Authorization': authorization, 'Origin': 'https://hostile.invalid'}, 403),
             ('hostile-host', {'Authorization': authorization, 'Host': 'hostile.invalid:2476'}, 403)]
    for name, headers, expected in cases:
        connection = http.client.HTTPConnection('127.0.0.1', 2476, timeout=5)
        try:
            connection.request('GET', '/status', headers=headers)
            response = connection.getresponse()
            payload = response.read()
            assert response.status == expected, (name, response.status)
            assert token.read_bytes().strip() not in payload
            print(json.dumps({'case': name, 'status': response.status, 'result': 'passed'}))
        finally:
            connection.close()


if __name__ == '__main__':
    main()
