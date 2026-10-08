#!/usr/bin/env python3
"""Retain public Actions evidence for exact local commits; missing is not green."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import subprocess
import urllib.request

ROOT=Path(__file__).resolve().parent.parent
PROJECTS=[('SERVER','Cairn',{'smoke'}),('Mini-Docker','Mini-Docker',{'Tests','Lint','Root Runtime Validation'}),
          ('DURAFLOW','DURAFLOW',{'Verification'}),('FAILFORGE','FAILFORGE',{'Verification'}),
          ('Mini-Redis-Cassandra','Mini-Redis-Cassandra',{'Verification'}),('Coordination-service','Coordination-service',{'Verification'})]


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args()
    args.output.mkdir(parents=True,exist_ok=False)
    report={'checked_utc':datetime.now(timezone.utc).isoformat(),'repositories':[]}
    complete=True
    for local,remote,required in PROJECTS:
        directory=ROOT.parent/local
        commit=subprocess.check_output(['git','-C',str(directory),'rev-parse','HEAD'],text=True).strip()
        url=f'https://api.github.com/repos/Yumekaz/{remote}/actions/runs?head_sha={commit}&per_page=100'
        request=urllib.request.Request(url,headers={'User-Agent':'Cairn-acceptance','Accept':'application/vnd.github+json'})
        with urllib.request.urlopen(request,timeout=30) as response:
            payload=json.load(response)
        (args.output/f'{remote}-runs.json').write_text(json.dumps(payload,indent=2))
        latest={}
        for run in payload.get('workflow_runs',[]):
            if run.get('head_sha')!=commit or run.get('name') not in required:continue
            if run['name'] not in latest or run['id']>latest[run['name']]['id']:latest[run['name']]=run
        passed=required <= latest.keys() and all(r['status']=='completed' and r['conclusion']=='success' for r in latest.values())
        item={'repository':f'Yumekaz/{remote}','commit':commit,'result':'passed' if passed else 'not-passed',
              'missing_workflows':sorted(required-latest.keys()),'runs':[{k:run.get(k) for k in ['id','name','head_sha','status','conclusion','html_url']} for run in latest.values()]}
        report['repositories'].append(item);complete=complete and passed
        print(remote,commit,item['result'],flush=True)
        (args.output/'report.json').write_text(json.dumps(report,indent=2))
    report['all_exact_commit_checks_passed']=complete
    (args.output/'report.json').write_text(json.dumps(report,indent=2))
    lines=['# Exact-commit GitHub Actions checks','','| Repository | Commit | Result | Checks |','| --- | --- | --- | --- |']
    for item in report['repositories']:
        links=', '.join(f'[{r["name"]}]({r["html_url"]})' for r in item['runs'])
        lines.append(f'| {item["repository"]} | `{item["commit"]}` | {item["result"]} | {links or "missing"} |')
    (args.output/'report.md').write_text('\n'.join(lines)+'\n')
    raise SystemExit(0 if complete else 1)


if __name__=='__main__':main()
