"""Finite model witness, not a production codec or persistence test."""
import hashlib,itertools,json,random

def natural(n):
    assert n >= 0
    s=str(n).encode('ascii')
    return '1'*len(s)+'0'+''.join(f'{b:08b}' for b in s)

def opaque(s):
    return ''.join('1'+f'{b:08b}' for b in s)+'0'

def no_prefix(keys):
    keys=sorted(set(keys))
    assert all(not b.startswith(a) for a,b in zip(keys,keys[1:]))

rng=random.Random(914)
nums=list(range(10000))+[2**53,2**64,2**128,10**100]+[rng.getrandbits(200) for _ in range(1000)]
nums=sorted(set(nums))
assert sorted(nums,key=natural)==nums
no_prefix(map(natural,nums))
ids=[bytes(t) for n in range(4) for t in itertools.product((0,1,97,98,255),repeat=n)]
ids += ['é'.encode(),'e\u0301'.encode(),'朋友'.encode()]
assert sorted(ids,key=opaque)==sorted(ids)
no_prefix(map(opaque,ids))
keys=[natural(n)+opaque(i) for n in (0,1,2,10,2**64) for i in ids]
no_prefix(keys)
assert sorted(((n,i) for n in (0,1,2,10,2**64) for i in ids),key=lambda t:natural(t[0])+opaque(t[1]))==sorted((n,i) for n in (0,1,2,10,2**64) for i in ids)

# Canonical compressed radix partition: fixed 3-entry leaf limit.
# Digests summarize an abstract structure, not disk serialization/page-byte fit.
def leaf(rows): return ('leaf',tuple(sorted(rows)))
def branch(bit,left,right): return ('branch',bit,left,right)
def partition(rows):
    rows=sorted(rows)
    if len(rows)<=3:return leaf(rows)
    bit=next(i for i,(a,b) in enumerate(zip(rows[0],rows[-1])) if a!=b)
    return branch(bit,partition([k for k in rows if k[bit]=='0']),partition([k for k in rows if k[bit]=='1']))
def allkeys(tree):
    if tree[0]=='leaf': return list(tree[1])
    return allkeys(tree[2])+allkeys(tree[3])
def insert(tree,key):
    if tree[0]=='leaf':return partition(set(tree[1])|{key})
    # If the key diverges above this compact branch, introduce one ancestor.
    exemplar=tree
    while exemplar[0]!='leaf':exemplar=exemplar[2]
    sample=exemplar[1][0]
    diff=next((i for i,(a,b) in enumerate(zip(sample,key)) if a!=b),None)
    if diff is None:return tree
    if diff<tree[1]:
        new=leaf([key])
        return branch(diff,new,tree) if key[diff]=='0' else branch(diff,tree,new)
    side=2 if key[tree[1]]=='0' else 3
    return branch(tree[1],insert(tree[2],key) if side==2 else tree[2],insert(tree[3],key) if side==3 else tree[3])
sample=rng.sample(keys,200)
expected=partition(sample)
for _ in range(30):
    order=sample[:];rng.shuffle(order);tree=leaf([])
    for key in order:tree=insert(tree,key)
    assert tree==expected
assert allkeys(expected)==sorted(sample)
# Day concatenation breaks revision order when recorded times are backdated.
days={'2026-09-13':[3,8],'2026-09-14':[1,5]}
assert [r for day in sorted(days) for r in days[day]] != [1,3,5,8]
print(json.dumps({'status':'pass','natural_keys':len(nums),'opaque_keys':len(ids),'composite_keys':len(keys),'insertion_orders':30,'entries_per_order':200,'abstract_root_sha256':hashlib.sha256(repr(expected).encode()).hexdigest(),'limits':'Finite key ordering, prefix and abstract partition witnesses only; no disk codec, byte bounds, crash recovery or production correctness claim.'},indent=2))
