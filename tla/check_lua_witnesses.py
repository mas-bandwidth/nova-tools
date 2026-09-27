#!/usr/bin/env python3
"""Replay table-model findings against only an isolated, disposable Redis.
Usage: python3 check_lua_witnesses.py /path/to/pinned/table.lua
No fleet addresses, credentials, or installed library are used.
"""
import json, pathlib, socket, subprocess, sys, tempfile, time

class Redis:
    def __init__(self, path):
        self.s = socket.socket(socket.AF_UNIX)
        self.s.settimeout(15)
        self.s.connect(path)
        self.f = self.s.makefile('rb')
    def cmd(self, *args):
        args = [str(x).encode() for x in args]
        self.s.sendall(b'*%d\r\n' % len(args) + b''.join(b'$%d\r\n' % len(x)+x+b'\r\n' for x in args))
        return self.read()
    def read(self):
        line = self.f.readline()
        if not line: raise RuntimeError('Redis closed the connection')
        code, value = line[:1], line[1:-2]
        if code == b'-': raise RuntimeError(value.decode())
        if code == b'+': return value.decode()
        if code == b':': return int(value)
        if code == b'$':
            n=int(value)
            if n == -1: return None
            out=self.f.read(n); assert self.f.read(2)==b'\r\n'
            return out.decode()
        if code == b'*': return [self.read() for _ in range(int(value))]
        raise RuntimeError(repr(line))

def run(source):
    with tempfile.TemporaryDirectory(prefix='table-model-',dir='/tmp') as tmp:
        sock=tmp+'/redis.sock'
        with open(tmp+'/redis.log','w') as log:
            p=subprocess.Popen(['redis-server','--port','0','--unixsocket',sock,'--unixsocketperm','700','--save','','--appendonly','no'],stdout=log,stderr=log)
            try:
                deadline=time.monotonic()+15
                while not pathlib.Path(sock).exists():
                    if p.poll() is not None: raise RuntimeError('Redis failed to start')
                    if time.monotonic()>=deadline: raise TimeoutError('Redis socket startup')
                    time.sleep(.02)
                r=Redis(sock)
                r.cmd('FUNCTION','LOAD','REPLACE','#!lua name=stella_table_model\n'+pathlib.Path(source).read_text())
                fields={'order':'c1,c2,c3','footer':'total','created_at':'2026-09-27T00:00:00Z',**{'col:'+c:'members:none:10:'+c for c in ['c1','c2','c3']}}
                def call(name,*args): return r.cmd('FCALL','ns_table_'+name,0,*args)
                def setup():
                    r.cmd('FLUSHDB')
                    for t in ['t1','t2']:
                        assert call('create',t,json.dumps(fields))==['OK']
                        for row in ['r1','r2']: assert call('row_add',t,row,'{}')[0]=='ROW'
                    assert call('cell_add','t1','r1','c1','m1',1)[0]=='OK'
                    r.cmd('ZADD','external',2,'m2')
                def binding(rows): return json.dumps({'fields':fields,'rows':rows})
                setup()
                assert call('cell_add','t1','r1','c2','m1',1)[0]=='OK'
                assert r.cmd('ZSCORE','table:t1:cell:r1:c1','m1')=='1'
                assert r.cmd('ZSCORE','table:t1:cell:r1:c2','m1')=='1'
                print('CONFIRMED OnePlacePerTable fails: add permits m1 in two owned cells',flush=True)
                setup()
                assert call('cell_add','t2','r1','c1','m1',1)[0]=='OK'
                assert r.cmd('ZSCORE','table:t1:cell:r1:c1','m1')=='1'
                assert r.cmd('ZSCORE','table:t2:cell:r1:c1','m1')=='1'
                print('PASS scope control: same member can be in both tables, as intended by the per-table placement rule',flush=True)
                setup()
                assert call('bind','t1',binding([]))==['OK']
                assert r.cmd('ZSCORE','table:t1:cell:r1:c1','m1') is None
                print('CONFIRMED BindPreservesOwned fails: removing r1 deletes its owned m1',flush=True)
                setup()
                assert call('bind','t1',binding([{'key':'r1','binds':{'c1':'external'}},{'key':'r2'}]))==['OK']
                assert r.cmd('ZSCORE','table:t1:cell:r1:c1','m1')=='1'
                assert call('members','t1','r1','c1')==['MEMBERS',['m2','2']]
                print('CONFIRMED BindPreservesOwned fails: retained r1 can hide m1 behind a binding',flush=True)
                setup()
                assert call('row_add','t1','r1',json.dumps({'binds':{'c2':'table:t1:cell:r1:c1'}}))[0]=='ROW'
                assert call('members','t1','r1','c2')==['MEMBERS',['m1','1']]
                assert call('drop','t1')[0]=='OK'
                assert r.cmd('ZSCORE','table:t1:cell:r1:c1','m1') is None
                print('CONFIRMED DropPreservesBound fails with alias: unbound owner deletion removes bound view target',flush=True)
                setup()
                assert call('row_add','t1','r1',json.dumps({'binds':{'c1':'table:t1:cell:r1:c2'}}))[0]=='ROW'
                assert call('cell_add','t1','r1','c2','m1',1)[0]=='OK'
                assert call('members','t1','r1','c1')==['MEMBERS',['m1','1']]
                print('CONFIRMED CellWritesPreserveBoundSets fails for owned-key alias: owner may still add member',flush=True)
                setup()
                assert call('cell_move','t1','r1','c1','m1','c2')[0]=='OK'
                assert r.cmd('ZSCORE','table:t1:cell:r1:c1','m1') is None
                assert r.cmd('ZSCORE','table:t1:cell:r1:c2','m1')=='1'
                r.cmd('ZADD','external',2,'m2')
                assert call('row_add','t1','r2',json.dumps({'binds':{'c1':'external'}}))[0]=='ROW'
                before=r.cmd('ZRANGE','external',0,-1,'WITHSCORES')
                assert call('cell_add','t1','r2','c1','m1',1)[:2]==['REFUSED','BOUND']
                assert call('cell_remove','t1','r2','c1','m2')[:2]==['REFUSED','BOUND']
                assert call('cell_move','t1','r2','c1','m2','c2')[:2]==['REFUSED','BOUND']
                assert call('cell_move','t1','r2','c2','m2','c1')[:2]==['REFUSED','BOUND']
                assert call('clear','t1')[:2]==['REFUSED','BOUND']
                assert r.cmd('ZSCORE','table:t1:cell:r1:c2','m1')=='1'
                assert call('drop','t1')[0]=='OK'
                assert r.cmd('ZRANGE','external',0,-1,'WITHSCORES')==before
                print('PASS controls: move keeps score; bound add/remove/move/clear refuse; drop leaves disjoint external set',flush=True)
                setup()
                assert call('clear','t1')[0]=='OK'
                assert r.cmd('EXISTS','table:t1')==1
                assert r.cmd('ZCARD','table:t1:rows')==0
                assert r.cmd('ZCARD','table:t1:cell:r1:c1')==0
                print('PASS control: successful clear removes owned rows/members and keeps definition',flush=True)
                r.s.close()
            finally:
                p.terminate()
                try: p.wait(timeout=10)
                except subprocess.TimeoutExpired: p.kill(); p.wait()
if __name__=='__main__': run(sys.argv[1])
