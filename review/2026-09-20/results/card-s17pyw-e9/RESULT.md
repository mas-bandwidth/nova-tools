RESULT: s17pyw-e9 sha=7c29513b0e8f

**BLOCKED**

What stopped me: the base repository was never provisioned into this bench. The
card's base-repo bundle `/tmp/schema14-ftf.bundle` (base-sha `7c29513b0e8f42f4...`,
base-branch `fixed-table-form`) does not exist and is not readable on this machine,
and the job's git repo contains **zero objects** — no commits, no refs, no HEAD.
Every material this card needs to read or build on is absent.

Python3.14.7 IS installed, so this is NOT `BLOCKED-TOOLCHAIN`; the interpreter is
the bench's fault in a sense but it is present. The blocker is the missing base repo.

STEP 1 evidence (`git rev-parse HEAD` must print `7c29513b0e8f...` — it cannot):

```
$ git rev-parse HEAD
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
Use '--' to separate paths from revisions, like this:
'git <command> [<revision>...] -- [<file>...]'
HEAD
rc=128

$ git count-objects -v
count: 0
size: 0
in-pack: 0
packs: 0
size-pack: 0
prune-packable: 0
garbage: 0
size-garbage: 0

$ git status --porcelain
(empty; only untracked harness artifacts: .lease, .nova-sandbox-tmp/, harness-output.log, opencode.json)
```

STEP 2/3 evidence — the law docs and the C++ oracle do not exist here at all:

```
$ ls docs/ bench/ make/ test/
ls: bench/: No such file or directory
ls: docs/: No such file or directory
ls: make/: No such file or directory
ls: test/: No such file or directory

$ grep -rn "python\|py3" make/ Makefile .github/workflows/ci-fast.yml
grep: make/: No such file or directory
grep: Makefile: No such file or directory
grep: .github/workflows/ci-fast.yml: No such file or directory

$ ls -la /tmp/schema14-ftf.bundle
ls: /tmp/schema14-ftf.bundle: Operation not permitted
```

Exhaustive search of the accessible tree (`find / -maxdepth 3 -name "schema14*"`,
`find ... -name "*.bundle"`, the whole `NOVA_SWARM_JOB` job tree, `$HOME/opencode/repos`,
`$HOME/opencode/snapshot`) found no copy of the `schema` repo and no bundle file.

Consequences for the card steps:
- STEP 1: cannot print the required sha12; `git rev-parse HEAD` errors rc=128.
- STEP 2: `docs/SPEC.md` and `docs/FIXED-FORM-ALGORITHM.md` do not exist, so the row-E9
  rule cannot be quoted. (Cannot even distinguish `NO LAW` — the doc is not there.)
- STEP 3: no C++ oracle files and no `bench/paired/corpus`; the reference cannot be read.
- STEP 4/5/6: nothing to port against, and with no base commit there is no `HEAD~1`
  for the diffstat and no branch `rowan/py-e9-packet` to branch from.

Per the card, "BLOCKED naming exactly what stopped you" — the bench failed to
provision the base repository, so row E9 could not be started. No files were created
under `test/py-packet/` or `python/` and no commit was made. This is the bench's
fault, not the row's.