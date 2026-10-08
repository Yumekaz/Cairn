#!/usr/bin/env python3
"""Start session two only after session one's retained acceptance evidence passes."""
import json
from pathlib import Path
import subprocess
import time

ROOT=Path(__file__).resolve().parent.parent
FIRST=ROOT/'validation-runs/soak-session-1/status.json'
SECOND=ROOT/'validation-runs/soak-session-2'


def main():
    deadline=time.monotonic()+3*3600
    while time.monotonic()<deadline:
        status=json.loads(FIRST.read_text())
        if status['state']=='passed':
            assert status['target_seconds']==7200 and status['elapsed_seconds']>=7200
            assert status['errors']==0 and status.get('restore_verified') is True
            assert status.get('daemon_resources') and status.get('runtime_resources')
            assert not status.get('cleanup_error')
            break
        if status['state'] in {'failed','interrupted'}:
            raise RuntimeError('session one did not pass; no silent retry or duration aggregation')
        if time.time()-status['updated_unix']>90:
            raise RuntimeError('session one stopped advancing; its continuity is not proven')
        time.sleep(15)
    else:
        raise TimeoutError('session one did not complete within the bounded queue wait')
    if SECOND.exists():raise RuntimeError('refusing a duplicate session-two fixture')
    result=subprocess.run(['/usr/bin/python3',str(ROOT/'scripts/notes_soak.py'),'--output',str(SECOND),
                           '--port','8099','--duration-seconds','7200','--interval-seconds','5','--reads-per-sample','9'],
                          cwd=ROOT,timeout=7800)
    raise SystemExit(result.returncode)


if __name__=='__main__':main()
