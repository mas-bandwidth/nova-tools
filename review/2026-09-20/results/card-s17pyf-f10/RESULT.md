RESULT: s17pyf-f10 sha=7c29513b0e8f

**BLOCKED** — the bench never provisioned the base repository, so there is nothing to read or port.

What stopped me, exactly:

1. `git rev-parse HEAD` fails — it does not print `7c29513b0e8f`. The repo has **no commits at all**:

```
$ git rev-parse HEAD
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
Use '--' to separate paths from revisions, like this:
'git <command> [<revision>...] -- [<file>...]'
HEAD

$ git log --oneline -3
fatal: your current branch 'master' does not have any commits yet

$ git status
On branch master
No commits yet
Untracked files:  .lease  .nova-sandbox-tmp/  harness-output.log  opencode.json
```

2. The base bundle `/tmp/schema14-ftf.bundle` (declared base-repo) is **not readable inside this sandbox**:

```
$ git bundle verify /tmp/schema14-ftf.bundle
error: could not open '/tmp/schema14-ftf.bundle'

$ python3 -c "print(open('/tmp/schema14-ftf.bundle','rb').read(10))"
PermissionError: [Errno 1] Operation not permitted: '/tmp/schema14-ftf.bundle'
```

3. Consequently the reference material the card requires does not exist here, so STEP 2 and STEP 3 cannot be done:

```
$ ls test/ docs/ make/ build/
ls: docs/: No such file or directory
ls: make/: No such file or directory
ls: test/: No such file or directory
```

There is no `docs/FIXED-FORM-ALGORITHM.md`, no `test/tables/fixedform_main.cpp`, no
`test/tables/fixedform_properties.cpp`, and no `build/fixedform-corpus` byte oracle to read. A
RED-FIRST case for row F10 cannot be written against an oracle that is not on the bench, and a
commit here would be an orphan root commit with no parent at base-sha `7c29513b0e8f`, which cannot
be replayed onto the harvested branch.

This is a bench provisioning fault (missing base repo / unreadable bundle), not a row fault. Per the
card I stop here and commit nothing. Python interpreter **is** present: `/opt/homebrew/bin/python3`, `Python 3.14.7`.