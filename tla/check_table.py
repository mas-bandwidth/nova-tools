#!/usr/bin/env python3
"""Run local TLC checks with one 120-second budget. No network/downloads.
Default strict mode FAILS on the first outstanding desired contract.
"""
import argparse
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

WITNESSES = [
    ('OnePlace', 'OnePlacePerTable'),
    ('Bind', 'BindPreservesOwned'),
    ('BindRetained', 'BindPreservesOwned'),
    ('BoundAlias', 'CellWritesPreserveBoundSets'),
    ('DropAlias', 'DropPreservesBound'),
]

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--jar',type=Path,required=True)
    p.add_argument('--mode',choices=['strict','contracts','witnesses','controls','all'],default='strict')
    p.add_argument('--out',type=Path,required=True)
    a=p.parse_args()
    if not a.jar.is_file(): p.error('--jar must name an existing tla2tools.jar')
    a.jar=a.jar.resolve(); a.out=a.out.resolve(); a.out.mkdir(parents=True,exist_ok=True)
    root=Path(__file__).resolve().parent
    deadline=time.monotonic()+120
    cases=[]
    if a.mode in ('contracts','all'):
        cases += [('MCTableMachine',None),('MCTableFixedPoint',None)]
    if a.mode in ('strict','witnesses','all'):
        cases += [('MCTable'+name,inv) for name,inv in WITNESSES]
    if a.mode in ('controls','all'):
        cases += [('MCTableCrossTable', 'OneTablePerMember')]
    for name, inv in cases:
        start=time.monotonic()
        log=a.out/(name+'.log')
        with tempfile.TemporaryDirectory(prefix='tlc-table-') as meta, log.open('w') as out:
            cmd=['java','-XX:+UseParallelGC','-Xmx2g','-cp',str(a.jar),'tlc2.TLC',
                 '-workers','2' if inv is None else '1','-metadir',meta,
                 '-config',name+'.cfg','MCTableMachine.tla']
            try:
                r=subprocess.run(cmd,cwd=root,stdout=out,stderr=subprocess.STDOUT,
                                 timeout=max(.01,deadline-time.monotonic()))
            except subprocess.TimeoutExpired:
                print(f'TIMEOUT {name}: suite exceeded 120 seconds; {log}',flush=True)
                return 124
        text=log.read_text()
        seconds=time.monotonic()-start
        if inv is not None and a.mode != 'strict':
            ok=r.returncode==12 and f'Invariant {inv} is violated.' in text
            result=('EXPECTED SCOPE CONTROL' if name == 'MCTableCrossTable' else 'EXPECTED COUNTEREXAMPLE') if ok else 'UNEXPECTED RESULT'
        else:
            ok=r.returncode==0 and 'Model checking completed. No error has been found.' in text
            result='PASS' if ok else 'FAIL'
        stats=re.search(r'^\d[\d,]* states generated.*$',text,re.MULTILINE)
        print(f'{result} {name} {seconds:.2f}s'+(' | '+stats[0] if stats else '')+f' | {log}',flush=True)
        if not ok: return r.returncode or 1
    return 0
if __name__=='__main__': sys.exit(main())
