#!/usr/bin/env python3
"""Run the declared TLC cases on a Linux bench, with a bounded total budget."""
import argparse
import csv
import hashlib
import os
from pathlib import Path
import platform
import re
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[1]
FIELDS = ['config', 'module', 'input_sha256', 'jar_sha256', 'host', 'started_utc',
          'generated', 'distinct', 'seconds', 'exit', 'result', 'expected', 'property', 'budget', 'mode']


def inputs_digest():
    paths = [*ROOT.glob('tla/*.tla'), *ROOT.glob('tla/MC*.cfg'),
             ROOT / 'tla/CASES.tsv', ROOT / 'tools/tlc.py']
    digest = hashlib.sha256()
    for path in sorted(paths):
        digest.update(path.relative_to(ROOT).as_posix().encode() + b'\0')
        digest.update(path.read_bytes() + b'\0')
    return digest.hexdigest()


def cases():
    with (ROOT / 'tla/CASES.tsv').open() as source:
        rows = list(csv.DictReader(source, delimiter='\t'))
    names = [row['config'] for row in rows]
    actual = sorted(p.name for p in ROOT.glob('tla/MC*.cfg'))
    if sorted(names) != actual or len(set(names)) != len(names):
        raise ValueError('CASES.tsv must name every MC*.cfg exactly once')
    for row in rows:
        if not re.fullmatch(r'MC[A-Za-z0-9]+\.tla', row['module']):
            raise ValueError('invalid instance module: ' + row['module'])
        if not (ROOT / 'tla' / row['module']).is_file():
            raise ValueError('missing instance module: ' + row['module'])
        if row['expected'] not in ('pass', 'invariant', 'action', 'temporal'):
            raise ValueError('invalid expected outcome: ' + row['config'])
        if row['deadlock'] not in ('check', 'ignore-terminal'):
            raise ValueError('invalid deadlock policy: ' + row['config'])
        if (row['expected'] == 'pass') != (row['property'] == '-'):
            raise ValueError('expected property required only for a counterexample: ' + row['config'])
        if row['gate'] not in ('required', 'bench') or not re.fullmatch(r'[a-z][a-z0-9-]*', row['group']):
            raise ValueError('invalid gate/group: ' + row['config'])
        if row['gate'] == 'required' and row['debt'] != '-':
            raise ValueError('required case cannot be waived as debt: ' + row['config'])
    return rows


def accepted(case, code, output):
    if case['expected'] == 'pass':
        return code == 0 and 'Model checking completed. No error has been found.' in output
    if case['expected'] == 'temporal':
        # TLC reports liveness counterexamples collectively; these configurations
        # each select one temporal property, named explicitly in CASES.tsv.
        config = (ROOT / 'tla' / case['config']).read_text()
        property_line = re.search(r'^PROPERT(?:Y|IES)\s+([^\n]+)', config, re.M)
        return (code == 13 and 'Temporal properties were violated.' in output
                and property_line is not None and property_line[1].strip() == case['property'])
    prefix = 'Invariant' if case['expected'] == 'invariant' else 'Action property'
    expected_code = 12 if case['expected'] == 'invariant' else 13
    return code == expected_code and any(
        f'{prefix} {name} is violated.' in output for name in case['property'].split('|'))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--jar', type=Path)
    parser.add_argument('--out', type=Path)
    parser.add_argument('--budget', type=float, default=110)
    parser.add_argument('--workers', type=int, default=2)
    parser.add_argument('--shards', type=int, default=1)
    parser.add_argument('--shard', type=int, default=0)
    parser.add_argument('--group', help='run one declared CASES.tsv group')
    parser.add_argument('--manual', action='store_true', help='explicit manual bench experiment; forbidden in CI')
    parser.add_argument('--list-groups', action='store_true', help='print required CI groups as JSON, without running TLC')
    args = parser.parse_args()
    try:
        all_cases = cases()
    except (ValueError, KeyError, OSError) as error:
        parser.error(str(error))
    if args.list_groups:
        import json
        print(json.dumps(sorted({r['group'] for r in all_cases if r['gate'] == 'required'})))
        return 0
    if args.manual and (os.environ.get('GITHUB_ACTIONS') == 'true' or os.environ.get('NOVA_CI') == '1'):
        parser.error('--manual is forbidden in CI; every workflow job stays under two minutes')
    if args.jar is None or args.out is None:
        parser.error('--jar and --out are required for a run')
    if platform.system() != 'Linux':
        parser.error('TLC runs on a Linux bench; run this target over ssh or in the Linux CI job')
    cap = 3600 if args.manual else 110
    if not 0 < args.budget <= cap or args.workers not in (1, 2):
        parser.error(f'budget must be positive and at most {cap} seconds; workers must be 1 or 2')
    if args.shards < 1 or not 0 <= args.shard < args.shards:
        parser.error('use a positive shard count and a zero-based shard below it')
    if not args.jar.is_file():
        parser.error('--jar must name the installed TLC jar; this runner never downloads tools')
    if args.shards > len(all_cases):
        parser.error('shard count exceeds the number of declared cases')
    if args.group:
        if args.shards != 1 or args.shard != 0:
            parser.error('--group and shard selection cannot be combined')
        chosen = [r for r in all_cases if r['group'] == args.group]
        if not chosen:
            parser.error('unknown group: ' + args.group)
    else:
        chosen = all_cases[args.shard::args.shards]
    jar = args.jar.resolve()
    args.out = args.out.resolve()
    args.out.mkdir(parents=True, exist_ok=True)
    digest = inputs_digest()
    jar_digest = hashlib.sha256(jar.read_bytes()).hexdigest()
    deadline = time.monotonic() + args.budget
    records = []
    failed = False
    for case in chosen:
        started = datetime.now(timezone.utc).isoformat()
        start = time.monotonic()
        log = args.out / (case['config'] + '.log')
        code = 124
        with tempfile.TemporaryDirectory(prefix='tlc-', dir=args.out) as scratch:
            command = ['java', '-XX:+UseParallelGC', '-XX:ActiveProcessorCount=2', '-Xmx2g',
                       '-Djava.io.tmpdir=' + scratch, '-cp', str(jar), 'tlc2.TLC',
                       '-lncheck', 'final',
                       '-workers', str(args.workers if case['expected'] == 'pass' else 1),
                       '-metadir', str(Path(scratch) / 'states'),
                       '-config', case['config'], case['module']]
            if case['deadlock'] == 'ignore-terminal':
                command.insert(command.index('-metadir'), '-deadlock')
            with log.open('w') as output:
                try:
                    remaining = deadline - start
                    if remaining <= 0:
                        output.write('TLC suite budget exhausted before starting this case\n')
                    else:
                        result = subprocess.run(command, cwd=ROOT / 'tla', stdout=output,
                                                stderr=subprocess.STDOUT, timeout=remaining)
                        code = result.returncode
                except subprocess.TimeoutExpired:
                    output.write('TLC suite budget exhausted during this case\n')
                except OSError as error:
                    output.write(str(error) + '\n')
                    code = 127
        output = log.read_text()
        stats = re.search(r'([\d,]+) states generated, ([\d,]+) distinct states found', output)
        ok = accepted(case, code, output) and stats is not None
        failed = failed or not ok
        row = dict(config=case['config'], module=case['module'], input_sha256=digest,
                   jar_sha256=jar_digest, host=platform.node(), started_utc=started,
                   generated=stats[1].replace(',', '') if stats else '-',
                   distinct=stats[2].replace(',', '') if stats else '-',
                   seconds=f'{time.monotonic() - start:.3f}', exit=code,
                   result='PASS' if ok else 'FAIL', expected=case['expected'], property=case['property'],
                   budget=f'{args.budget:g}', mode='manual' if args.manual else 'bounded')
        records.append(row)
        print(f"{row['result']} {row['config']} {row['seconds']}s exit={code}", flush=True)
        if time.monotonic() >= deadline:
            break
    if inputs_digest() != digest:
        print('refusing run records: model inputs changed during execution', file=sys.stderr)
        return 1
    with (args.out / 'RUNS.tsv').open('w') as output:
        writer = csv.DictWriter(output, fieldnames=FIELDS, delimiter='\t', lineterminator='\n')
        writer.writeheader()
        writer.writerows(records)
    if len(records) != len(chosen):
        print('FAIL: budget expired before all selected cases ran', file=sys.stderr)
        failed = True
    return int(failed)


if __name__ == '__main__':
    sys.exit(main())
