#!/usr/bin/env python3
"""Check controlled table execution traces against EpochMemberTable and replay receipts.

Uses only two disposable, TCP-disabled Redis servers. No fleet access, downloads,
credentials or live-store capture. The finite trace starts with declared records,
columns and epoch domain; member creation/ID reuse and drop-definition are covered
by the Go functional controls, not silently added to the existing TLA model.
The entire suite (capture, receipt replay, TLC and negative controls) has a 120s cap.
"""
import argparse
import contextlib
import copy
import hashlib
import importlib.util
import json
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

TABLES = ['t1', 't2']
EPOCHS = [1, 2]
ROWS = ['r1', 'r2']
COLS = ['c1', 'c2', 'c3']
MEMBERS = {'m1': 1, 'm2': 2, 'm3': 1}
WRITERS = ['w1', 'w2']
EXTERNAL = [['external', 0], 'external', 'external']
NO_PLACE = [['none', 0], 'none', 'none']
FIELDS = {'order': ','.join(COLS), 'footer': 'total', 'created_at': '2026-09-27T00:00:00Z',
          'epoch_key': 'replay:epoch', 'epoch_field': 'n',
          **{'col:' + c: 'count:sum:0:' for c in COLS}}


def pairs(flat):
    return dict(zip(flat[::2], flat[1::2]))


def prefix(table, epoch):
    return f'table:{table}:{epoch}'


def cell(table, epoch, row, col):
    return [[table, epoch], row, col]


def check_time(deadline):
    if time.monotonic() >= deadline:
        raise TimeoutError('member replay suite exceeded 120 seconds')


@contextlib.contextmanager
def store(redis_type, source, root, deadline):
    sock = str(root / 'redis.sock')
    with (root / 'redis.log').open('w') as log:
        proc = subprocess.Popen(['redis-server', '--port', '0', '--unixsocket', sock,
                                 '--unixsocketperm', '700', '--save', '', '--appendonly', 'no'],
                                stdout=log, stderr=log)
        r = None
        try:
            startup = min(deadline, time.monotonic() + 15)
            while not Path(sock).exists():
                if proc.poll() is not None:
                    raise RuntimeError('disposable Redis exited during startup')
                check_time(startup)
                time.sleep(.02)
            r = redis_type(sock)
            r.cmd('FUNCTION', 'LOAD', '#!lua name=member_replay\n' + source)
            yield r
        finally:
            if r:
                r.f.close()
                r.s.close()
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)


def options(epoch, actor):
    return {'epoch': str(epoch), 'actor': actor, 'fence': 'fixture-fence', 'idem': 'fixture-attempt'}


def call(r, verb, args, opts):
    return r.cmd('FCALL', 'ns_table_' + verb, 0, *args, json.dumps(opts, sort_keys=True))


def seed(r):
    r.cmd('HSET', 'replay:epoch', 'n', 1)
    for member, epoch in MEMBERS.items():
        r.cmd('HSET', 'table::member:' + member, 'epoch', epoch)
    for table in TABLES:
        assert call(r, 'create', [table, json.dumps(FIELDS, sort_keys=True)], options(1, 'seed'))[0] == 'OK'
        for row in ROWS:
            assert call(r, 'row_add', [table, row, '{}'], options(1, 'seed'))[0] == 'ROW'
    assert call(r, 'cell_add', ['t1', 'r1', 'c1', '1', 'm1'], options(1, 'seed'))[0] == 'OK'
    r.cmd('ZADD', 'external', 2, 'm2')


def snapshot(r, seen):
    out = {'live': [], 'rows': [], 'binds': [], 'data': [], 'place': [],
           'active': int(r.cmd('HGET', 'replay:epoch', 'n')), 'seen': dict(seen)}
    for table in TABLES:
        for epoch in EPOCHS:
            p = prefix(table, epoch)
            # The model predeclares each future empty physical table. A stable
            # template supplies that virtual presence; an explicit drop overrides it.
            snap = pairs(r.cmd('HGETALL', p + ':definition'))
            if snap.get('_present') != '0':
                out['live'].append([table, epoch])
            for row in r.cmd('ZRANGE', p + ':rows', 0, -1):
                out['rows'].append([[table, epoch], row])
                h = pairs(r.cmd('HGETALL', p + ':row:' + row))
                for col in COLS:
                    target = h.get('key:' + col)
                    if target:
                        assert target == 'external', 'unexpected binding in controlled trace'
                        out['binds'].append([cell(table, epoch, row, col), EXTERNAL])
            for row in ROWS:
                for col in COLS:
                    values = r.cmd('ZRANGE', p + ':cell:' + row + ':' + col, 0, -1, 'WITHSCORES')
                    for member, score in pairs(values).items():
                        out['data'].append([cell(table, epoch, row, col), member, int(score)])
    for member, score in pairs(r.cmd('ZRANGE', 'external', 0, -1, 'WITHSCORES')).items():
        out['data'].append([EXTERNAL, member, int(score)])
    for member, epoch in MEMBERS.items():
        h = pairs(r.cmd('HGETALL', 'table::member:' + member))
        assert h.get('epoch') == str(epoch), 'member epoch changed: ' + member
        locations = []
        for table in TABLES:
            for e in EPOCHS:
                value = NO_PLACE
                if h.get('place:' + table) and epoch == e:
                    row, col = h['place:' + table].rsplit(':', 1)
                    value = cell(table, e, row, col)
                locations.append([[table, e], value])
        out['place'].append([member, locations])
    return out


def actions():
    # These are source API actions in the formal model's finite universe. Every
    # captured store state, including refused attempts, is checked by TLC.
    return [
        ('cell_move', ['t1', 'r1', 'c1', 'c2', 'm1'], 'w1'),
        ('cell_add', ['t1', 'r2', 'c1', '2', 'm1'], 'w2'),
        ('bind', ['t1', json.dumps({'fields': FIELDS, 'rows': []}, sort_keys=True)], 'w1'),
        ('row_add', ['t1', 'r1', json.dumps({'binds': {'c2': 'external'}})], 'w2'),
        ('row_add', ['t1', 'r2', json.dumps({'binds': {'c3': 'external'}})], 'w1'),
        ('row_add', ['t1', 'r2', json.dumps({'binds': {'c3': 'table:t2:1:cell:r1:c1'}})], 'w2'),
        ('clear', ['t1'], 'w2'),
        ('cell_remove', ['t1', 'r2', 'c3', 'm2'], 'w2'),
        ('cell_add', ['t2', 'r1', 'c1', '1', 'm1'], 'w1'),
        ('cell_add', ['t2', 'r2', 'c1', '2', 'm3'], 'w2'),
        ('cell_remove', ['t1', 'r1', 'c2', 'm1'], 'w1'),
        ('cell_remove', ['t1', 'r1', 'c2', 'm1'], 'w1'),
        ('row_del', ['t1', 'r1'], 'w1'),
        ('row_add', ['t1', 'r1', '{}'], 'w1'),
        ('cell_add', ['t1', 'r1', 'c1', '1', 'm1'], 'w1'),
        ('drop', ['t1'], 'w1'),
        ('create', ['t1', json.dumps(FIELDS, sort_keys=True)], 'w1'),
        ('advance', [], 'w1'),
        ('cell_add', ['t1', 'r1', 'c1', '1', 'm1'], 'w2'),
        ('read_epoch', [], 'w1'),
        ('row_add', ['t1', 'r1', '{}'], 'w1'),
        ('cell_add', ['t1', 'r1', 'c1', '1', 'm1'], 'w1'),
        ('cell_add', ['t1', 'r1', 'c1', '2', 'm2'], 'w1'),
        ('cell_move', ['t1', 'r1', 'c1', 'c2', 'm2'], 'w1'),
        ('row_del', ['t1', 'r1'], 'w2'),
        ('read_epoch', [], 'w2'),
        ('row_del', ['t1', 'r1'], 'w2'),
        ('bind', ['t1', json.dumps({'fields': FIELDS, 'rows': [{'key': 'r2'}]}, sort_keys=True)], 'w1'),
        ('cell_add', ['t1', 'r2', 'c1', '1', 'm2'], 'w1'),
        ('clear', ['t1'], 'w1'),
        ('drop', ['t1'], 'w1'),
        ('create', ['t1', json.dumps(FIELDS, sort_keys=True)], 'w1'),
    ]


def receipt(r, verb, args, opts, reply, before):
    table = args[0]
    after = int(r.cmd('HGET', 'table:' + table + ':revision', 'n') or 0)
    if reply[0] == 'REFUSED':
        assert after == before, 'refusal advanced revision'
        return None
    assert after == before + 1, 'revision gap'
    event_id, flat = r.cmd('XREVRANGE', 'table:' + table + ':changes', '+', '-', 'COUNT', 1)[0]
    event = pairs(flat)
    assert event['verb'] == verb and json.loads(event['args']) == args, 'receipt arguments differ'
    assert int(event['rev_before']) == before and int(event['rev_after']) == after, 'receipt revision gap'
    for key in ('epoch', 'actor', 'fence', 'idem'):
        assert event[key] == opts[key], 'receipt option differs: ' + key
    assert event['outcome'] in ('changed', 'noop')
    assert isinstance(json.loads(event['cells']), list) and isinstance(json.loads(event['members']), list)
    assert reply[-1] == ['RECEIPT', event_id, opts['epoch'], str(before), str(after), event['outcome']]
    event['id'] = event_id
    return event


def execute(r, verb, args, actor, seen, deadline, saved=None):
    check_time(deadline)
    if verb == 'read_epoch':
        seen[actor] = int(r.cmd('HGET', 'replay:epoch', 'n'))
        return None, None
    if verb == 'advance':
        current = int(r.cmd('HGET', 'replay:epoch', 'n'))
        assert seen[actor] == current
        r.cmd('HSET', 'replay:epoch', 'n', current + 1)
        return None, None
    before = int(r.cmd('HGET', 'table:' + args[0] + ':revision', 'n') or 0)
    count = r.cmd('XLEN', 'table:' + args[0] + ':changes')
    opts = options(seen[actor], actor)
    if saved:
        continuity(saved, before)
        # Replay the durable event's command/arguments/options, not the separate
        # test action. Refused calls have no event and use their admission log.
        verb, args = saved['verb'], json.loads(saved['args'])
        opts = {k: saved[k] for k in ('epoch', 'actor', 'fence', 'idem')}
    reply = call(r, verb, args, opts)
    event = receipt(r, verb, args, opts, reply, before)
    assert r.cmd('XLEN', 'table:' + args[0] + ':changes') == count + (event is not None), 'wrong event count'
    return reply[0] == 'REFUSED', event


def continuity(event, before):
    if int(event['rev_before']) != before or int(event['rev_after']) != before + 1:
        raise ValueError('GAP: expected revision ' + str(before) + ', receipt starts ' + event['rev_before'])


def validate_delta(event, before, after):
    table = json.loads(event['args'])[0]
    epoch = int(event['epoch'])
    physical = [table, epoch]
    def places(state):
        return {m: next(c for t, c in locations if t == physical) for m, locations in state['place']}
    def score(state, at, member):
        return str(next(n for c, m, n in state['data'] if c == at and m == member))
    def endpoint(at):
        return '' if at == NO_PLACE else at[1] + ':' + at[2]
    old, new = places(before), places(after)
    expected = []
    for member in MEMBERS:
        if old[member] != new[member]:
            sc = score(before, old[member], member) if new[member] == NO_PLACE else score(after, new[member], member)
            expected.append({'id': member, 'from': endpoint(old[member]), 'to': endpoint(new[member]), 'score': sc})
    actual = json.loads(event['members'])
    assert sorted(actual, key=lambda x: x['id']) == sorted(expected, key=lambda x: x['id']), 'receipt member delta disagrees with store'
    affected = json.loads(event['cells'])
    assert len(affected) == len(set(affected)), 'duplicate affected cell'
    def contents(state, at):
        present = [physical, at[1]] in state['rows']
        bound = next((target for c, target in state['binds'] if c == at), None)
        data = sorted((m, sc) for c, m, sc in state['data'] if c == at)
        return present, bound, data
    for row in ROWS:
        for col in COLS:
            at = cell(table, epoch, row, col)
            if contents(before, at) != contents(after, at):
                assert row + ':' + col in affected, 'receipt omits an affected cell'
    if event['outcome'] == 'noop':
        assert before == after, 'receipt calls a state change a noop'


def target(key):
    if key == 'external':
        return EXTERNAL
    match = re.fullmatch(r'table:(t[12]):([12]):cell:(r[12]):(c[123])', key)
    if not match:
        raise ValueError('unmapped binding: ' + key)
    table, epoch, row, col = match.groups()
    return cell(table, int(epoch), row, col)


def tla(value):
    if isinstance(value, str):
        return json.dumps(value)
    if isinstance(value, int):
        return str(value)
    return '<<' + ','.join(tla(x) for x in value) + '>>'


def tset(values):
    return '{' + ','.join(tla(x) for x in values) + '}'


def tfun(values):
    return '(' + ' @@ '.join('(' + tla(k) + ' :> ' + v + ')' for k, v in values) + ')'


def state_tla(s):
    place = tfun([(m, tfun([(t, tla(c)) for t, c in locations])) for m, locations in s['place']])
    seen = tfun([(w, str(s['seen'][w])) for w in WRITERS])
    return '<<' + ','.join([tset(s[k]) for k in ('live', 'rows', 'binds', 'data')] +
                          [place, str(s['active']), seen]) + '>>'


def action_tla(verb, args, actor, epoch):
    w = tla(actor)
    if verb == 'advance':
        return 'Advance(' + w + ')'
    if verb == 'read_epoch':
        return 'ReadEpoch(' + w + ')'
    table = tla([args[0], epoch])
    if verb in ('clear', 'drop', 'create'):
        return 'Epoch' + verb.title() + '(' + w + ',' + table + ')'
    if verb == 'row_del':
        return 'EpochRowDelete(' + w + ',' + table + ',' + tla(args[1]) + ')'
    if verb == 'row_add':
        binds = json.loads(args[2]).get('binds', {})
        nb = [[cell(args[0], epoch, args[1], col), target(key)] for col, key in binds.items()]
        return 'EpochRowAdd(' + w + ',' + table + ',' + tla(args[1]) + ',' + tset(nb) + ')'
    if verb == 'bind':
        rows = json.loads(args[1])['rows']
        nb = [[cell(args[0], epoch, row['key'], col), target(key)] for row in rows for col, key in row.get('binds', {}).items()]
        return 'EpochBind(' + w + ',' + table + ',' + tset([r['key'] for r in rows]) + ',' + tset(nb) + ')'
    c = tla(cell(args[0], epoch, args[1], args[2]))
    m = tla(args[3])
    if verb == 'cell_add':
        return 'EpochAdd(' + ','.join([w, c, tla(args[4]), args[3]]) + ')'
    if verb == 'cell_remove':
        return 'EpochRemove(' + ','.join([w, c, m]) + ')'
    if verb == 'cell_move':
        dst = tla(cell(args[0], epoch, args[1], args[3]))
        return 'EpochMove(' + ','.join([w, c, dst, tla(args[4])]) + ')'
    raise ValueError('unmapped verb: ' + verb)


def tlc(models, jar, out, initial, trace, deadline, mutate=False):
    observed = [initial] + [s['state'] for s in trace]
    if mutate:
        observed = copy.deepcopy(observed)
        # Destroy one observed record link after the real move. A validator
        # that merely runs the model without inspecting execution stays green.
        observed[1]['place'][0][1][0][1] = NO_PLACE
    name = 'MemberReceiptReplay'
    with tempfile.TemporaryDirectory(prefix='member-tlc-') as tmp:
        root = Path(tmp)
        for module in ('TableMachine', 'MemberTable', 'EpochMemberTable', 'MCEpochMemberTable'):
            shutil.copy2(models / (module + '.tla'), root)
        steps = []
        for i, step in enumerate(trace):
            steps.append(('CASE ' if i == 0 else '  [] ') + 'step=' + str(i) + ' -> ' + step['model'])
        steps.append('  [] OTHER -> UNCHANGED xvars')
        module = ('---------------- MODULE ' + name + ' ----------------\n'
                  'EXTENDS MCEpochMemberTable, TLC, Sequences\n'
                  'Observed == <<\n' + ',\n'.join(state_tla(s) for s in observed) + '\n>>\n'
                  'ActualState == <<live,rows,binds,data,place,activeEpoch,seenEpoch>>\n'
                  'MatchesExecution == ActualState=Observed[step+1]\n'
                  'ReplayNext ==\n' + '\n'.join(steps) + '\n'
                  'ReplaySpec == EpochInit /\\ [][ReplayNext]_xvars\n'
                  '=================================================================\n')
        cfg = (models / 'MCEpochMemberTable.cfg').read_text().replace('SPECIFICATION EpochSpec', 'SPECIFICATION ReplaySpec')
        cfg = cfg.replace('MaxSteps = 3', 'MaxSteps = ' + str(len(trace)))
        cfg += '\nINVARIANT MatchesExecution\n'
        (root / (name + '.tla')).write_text(module)
        (root / (name + '.cfg')).write_text(cfg)
        label = 'mutated-observation' if mutate else 'execution'
        artifact = out / label
        artifact.mkdir(parents=True, exist_ok=True)
        for path in root.glob('*.tla'):
            shutil.copy2(path, artifact / path.name)
        shutil.copy2(root / (name + '.cfg'), artifact / (name + '.cfg'))
        log = out / (label + '.log')
        with log.open('w') as stream:
            result = subprocess.run(['java', '-XX:+UseParallelGC', '-Xmx1g', '-cp', str(jar),
                                     'tlc2.TLC', '-workers', '1', '-metadir', str(root / 'states'),
                                     '-config', name + '.cfg', name + '.tla'], cwd=root,
                                    stdout=stream, stderr=subprocess.STDOUT,
                                    timeout=max(.01, deadline - time.monotonic()))
        text = log.read_text()
        if mutate:
            assert result.returncode == 12 and 'Invariant MatchesExecution is violated.' in text, text
        else:
            assert result.returncode == 0 and 'Model checking completed. No error has been found.' in text, text
        print(('EXPECTED MUTATION FAILURE ' if mutate else 'PASS ') + label + ': ' + str(log), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--jar', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--models', type=Path, default=Path(__file__).resolve().parent)
    args = parser.parse_args()
    for p in (args.source, args.jar, args.models / 'EpochMemberTable.tla'):
        if not p.is_file():
            parser.error('required file does not exist: ' + str(p))
    args.source, args.jar, args.models, args.out = [p.resolve() for p in (args.source, args.jar, args.models, args.out)]
    args.out.mkdir(parents=True, exist_ok=True)
    loader = importlib.util.spec_from_file_location('witness_redis', args.models / 'check_lua_witnesses.py')
    module = importlib.util.module_from_spec(loader)
    sys.dont_write_bytecode = True
    loader.loader.exec_module(module)
    start = time.monotonic()
    deadline = start + 120
    class BudgetRedis(module.Redis):
        def cmd(self, *values):
            check_time(deadline)
            self.s.settimeout(min(15, max(.01, deadline - time.monotonic())))
            return super().cmd(*values)
    source = args.source.read_text()
    trace = []
    with tempfile.TemporaryDirectory(prefix='member-source-') as tmp:
        with store(BudgetRedis, source, Path(tmp), deadline) as r:
            seed(r)
            seen = dict.fromkeys(WRITERS, 1)
            initial = snapshot(r, seen)
            for verb, params, actor in actions():
                model = action_tla(verb, params, actor, int(r.cmd('HGET', 'replay:epoch', 'n')))
                before = snapshot(r, seen)
                refused, event = execute(r, verb, params, actor, seen, deadline)
                after = snapshot(r, seen)
                if event:
                    validate_delta(event, before, after)
                trace.append({'verb': verb, 'args': params, 'actor': actor, 'refused': refused,
                              'receipt': event, 'model': model, 'state': after})
    (args.out / 'trace.json').write_text(json.dumps({'source_sha256': hashlib.sha256(source.encode()).hexdigest(),
        'model_sha256': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in args.models.glob('*.tla')},
        'initial': initial, 'steps': trace}, indent=2) + '\n')
    with tempfile.TemporaryDirectory(prefix='member-replay-') as tmp:
        with store(BudgetRedis, source, Path(tmp), deadline) as r:
            seed(r)
            seen = dict.fromkeys(WRITERS, 1)
            assert snapshot(r, seen) == initial
            for step in trace:
                refused, event = execute(r, step['verb'], step['args'], step['actor'], seen, deadline, step['receipt'])
                assert refused == step['refused'] and snapshot(r, seen) == step['state'], 'receipt replay diverged'
                if event:
                    expected = dict(step['receipt'])
                    expected['id'] = event['id']
                    assert event == expected, 'receipt payload changed during replay'
    print('PASS receipt replay: ' + str(len(trace)) + ' controlled transitions', flush=True)
    bad = copy.deepcopy(trace[0]['receipt'])
    bad['rev_before'] = str(int(bad['rev_before']) + 1)
    try:
        continuity(bad, int(trace[0]['receipt']['rev_before']))
    except ValueError as error:
        assert str(error).startswith('GAP:')
    else:
        raise AssertionError('gap mutation went undetected')
    bad = copy.deepcopy(trace[0]['receipt'])
    change = json.loads(bad['members'])
    change[0]['to'] = ''
    bad['members'] = json.dumps(change)
    try:
        validate_delta(bad, initial, trace[0]['state'])
    except AssertionError as error:
        assert str(error) == 'receipt member delta disagrees with store'
    else:
        raise AssertionError('receipt mutation went undetected')
    print('EXPECTED MUTATION FAILURES revision gap and receipt member delta', flush=True)
    tlc(args.models, args.jar, args.out, initial, trace, deadline)
    tlc(args.models, args.jar, args.out, initial, trace, deadline, mutate=True)
    print(f'SUITE execution replay passed in {time.monotonic()-start:.2f}s', flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
