#!/usr/bin/env python3
"""Check the proposed member protocol. Every selected suite has a 120s total cap.
No download or network. Default runs positive checks and all mutation controls.
The unchanged check_table.py remains the deliberately red current-code gate.
"""
import argparse
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

CASES = [
    ('MCMemberTable', 'MCMemberTable', None),
    ('MCMemberFixedPoint', 'MCMemberTable', None),
    ('MCMemberBrokenMove', 'MCMemberTable', 'Invariant RecordSetLink is violated.'),
    ('MCMemberBrokenAlias', 'MCMemberTable', 'Invariant NoOwnedAlias is violated.'),
    ('MCEpochMemberTable', 'MCEpochMemberTable', None),
    ('MCEpochMemberFixedPoint', 'MCEpochMemberTable', None),
    ('MCEpochMemberBroken', 'MCEpochMemberTable', 'Invariant NoEpochLeak is violated.'),
    ('MCEpochMemberBrokenStale', 'MCEpochMemberTable', 'Action property StaleWritesRefuse is violated.'),
]

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--jar', type=Path, required=True)
    p.add_argument('--out', type=Path, required=True)
    p.add_argument('--suite', choices=['all', 'member', 'epoch', 'small'], default='all')
    p.add_argument('--workers', type=int, default=4)
    a = p.parse_args()
    if not a.jar.is_file(): p.error('--jar must name an existing tla2tools.jar')
    if a.workers < 1: p.error('--workers must be positive')
    a.jar = a.jar.resolve()
    a.out = a.out.resolve()
    a.out.mkdir(parents=True, exist_ok=True)
    root = Path(__file__).resolve().parent
    start = time.monotonic()
    deadline = start + 120
    for name, module, failure in CASES:
        if a.suite == 'member' and module != 'MCMemberTable': continue
        if a.suite == 'epoch' and module != 'MCEpochMemberTable': continue
        if a.suite == 'small' and name in ('MCMemberTable', 'MCEpochMemberTable'): continue
        case_start = time.monotonic()
        log = a.out / (name + '.log')
        with tempfile.TemporaryDirectory(prefix='tlc-member-') as meta, log.open('w') as out:
            cmd = ['java', '-XX:+UseParallelGC', '-Xmx2g', '-cp', str(a.jar),
                   'tlc2.TLC', '-workers', str(a.workers if failure is None else 1),
                   '-metadir', meta, '-config', name+'.cfg', module+'.tla']
            try:
                r = subprocess.run(cmd, cwd=root, stdout=out, stderr=subprocess.STDOUT,
                                   timeout=max(.01, deadline-time.monotonic()))
            except subprocess.TimeoutExpired:
                print(f'TIMEOUT {name}: suite exceeded 120 seconds; {log}', flush=True)
                return 124
        result = log.read_text()
        expected_code = 13 if failure and failure.startswith('Action property') else 12
        ok = (r.returncode == expected_code and failure in result) if failure else (
            r.returncode == 0 and 'Model checking completed. No error has been found.' in result)
        label = ('EXPECTED MUTATION FAILURE' if failure else 'PASS') if ok else 'FAIL'
        stats = re.search(r'^\d[\d,]* states generated.*$', result, re.MULTILINE)
        print(f'{label} {name} {time.monotonic()-case_start:.2f}s'
              + (' | '+stats[0] if stats else '') + f' | {log}', flush=True)
        if not ok: return r.returncode or 1
    print(f'SUITE {a.suite} passed in {time.monotonic()-start:.2f}s', flush=True)
    return 0

if __name__ == '__main__': sys.exit(main())
