#!/usr/bin/env python3
"""Seed/remove one explicitly labelled metadata-display event on a test service."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import sqlite3
import uuid


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action',choices=['create','remove'])
    parser.add_argument('--service',required=True)
    parser.add_argument('--database',type=Path,default=Path.home()/'.cairn/cairn.db')
    parser.add_argument('--evidence',type=Path,required=True)
    args=parser.parse_args()
    if not re.fullmatch(r'notes-proof-[0-9a-f]{8}',args.service):
        parser.error('only named disposable notes-proof fixtures are accepted')
    with sqlite3.connect(args.database,timeout=5) as connection:
        service=connection.execute('SELECT id FROM services WHERE name=?',(args.service,)).fetchone()
        if not service: raise RuntimeError('test service not found')
        message='Acceptance fixture: metadata display and escaping test'
        if args.action=='create':
            if args.evidence.exists(): raise RuntimeError('refusing to overwrite fixture evidence')
            identifier=str(uuid.uuid4())
            metadata={'test_only':True,'service':args.service,'escaped_text':'<img src=x onerror="alert(1)">','nested':{'snapshot_verified':True}}
            connection.execute('INSERT INTO events(id,service_id,type,message,metadata_json,created_at) VALUES(?,?,?,?,?,?)',
                               (identifier,service[0],'AcceptanceMetadataProbe',message,json.dumps(metadata),datetime.now(timezone.utc).isoformat()))
            args.evidence.write_text(json.dumps({'event_id':identifier,'service':args.service,'metadata':metadata},indent=2))
            print('Created explicitly labelled test metadata event',identifier)
        else:
            evidence=json.loads(args.evidence.read_text())
            if evidence['service']!=args.service: raise RuntimeError('fixture ownership mismatch')
            uuid.UUID(evidence['event_id'])
            changed=connection.execute('DELETE FROM events WHERE id=? AND service_id=? AND type=? AND message=?',
                                       (evidence['event_id'],service[0],'AcceptanceMetadataProbe',message)).rowcount
            if changed!=1: raise RuntimeError('exact labelled fixture event was not found')
            print('Removed only the labelled test event; its evidence remains saved')


if __name__=='__main__':main()
