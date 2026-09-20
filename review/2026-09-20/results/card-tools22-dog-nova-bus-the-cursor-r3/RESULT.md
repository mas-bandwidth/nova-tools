RESULT tools22-dog-nova-bus-the-cursor-r3 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
BROKEN installed nova-* tools cannot execute: every invocation exits 126 'Permission denied'

TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 3 of 24
BUILD could not be determined: nova-version exited 126 'Permission denied', printed nothing

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | `git rev-parse HEAD` (step 1 head check) | 0 | CLEAN |
| 2 | `nova-version` (step 1 build check) | 126 | SKIP (could not run: Permission denied) |
| 3 | `sed -n '522,527p' docs/CLI.md` (section read) | 0 | CLEAN |

The section at docs/CLI.md:522-527 is prose only; it contains no fenced command
blocks to execute (verified: no ``` lines inside 522-527). There were therefore no
documented commands to run beyond the section read itself, and no DRIFTs to judge.

STEP 1 gate:
  git rev-parse HEAD -> 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 (matches card base)
  nova-version     -> /usr/bin/bash: line 1: /home/gaffer/.local/bin/nova-version:
                      Permission denied, exit 126

The installed tool cannot run AT ALL on this machine. Direct execution of
/home/gaffer/.local/bin/nova-version, nova-bus and nova-check all exit 126
'Permission denied'; even `cp` of the binary fails with 'Permission denied'
(cannot open for reading). The file stat shows -rwxr-xr-x glenn gaffer, but the
sandbox denies read/execute on the .local/bin tree regardless of user/group, so no
nova-* tool can be started, and no build check can pass. Quote of the exact
failure: `/usr/bin/bash: line 1: /home/gaffer/.local/bin/nova-version: Permission
denied` (exit 126).

RAN 0
SKIPPED 0

First 15 lines of output for each command:
  git rev-parse HEAD (exit 0):
    5298f6be12eaa0f7e6622334d2b6a1eb427649e3
  nova-version (exit 126):
    /usr/bin/bash: line 1: /home/gaffer/.local/bin/nova-version: Permission denied
  nova-bus (exit 126):
    /usr/bin/bash: line 1: /home/gaffer/.local/bin/nova-bus: Permission denied
  nova-check (exit 126):
    /usr/bin/bash: line 1: /home/gaffer/.local/bin/nova-check: Permission denied
  sed -n '522,527p' docs/CLI.md (exit 0):
    ### The cursor
    `inbox` and `check` do not walk the bus. Each reader keeps a cursor, the commit
    they last read to, in their own lane, and a run reads `git diff` from there: ten
    thousand notes on the bus and one new one is one parse. Three files in a lane make
    that work, all rebuildable from the notes: `CURSOR` (the commit, the count
    carried, and the switch-day line if you drew one), `OPEN` (the notes you have been
    shown and not answered, each as its whole display line so a later run prints it
    without opening the note), and `INDEX` (the lane's catalogue of its own notes, so
    a thread resolves by lookup). All three are written to a temp file beside
    themselves and renamed, so a run killed mid-write leaves the old file entire.
    The price, said plainly: closing is driven by what is new, so if somebody edits a
    note you answered long ago you are shown it again. You are asked twice; you are
    never told a note is answered when it is not. If your cursor is refused (the
    history was rewritten under it, or the open list is missing beside a cursor that
    says it was carrying notes, or the open list is from an older version), read once
    with `--full --advance`, which replaces all three. Those refusals are deliberate:
    a reader told "nothing new" by a stale cursor has been lied to.

Left owed: everything. The whole `### The cursor` section cannot be exercised on this
machine because the installed nova-* tooling cannot run at all (every invocation
exits 126 'Permission denied'; the binary is unreadable from this sandbox, so not
even the STEP 1 build check `nova-version` could print its sha). No cursor/inbox/
check behaviour, no --full --advance reading, no CURSOR/OPEN/INDEX file mechanics
could be observed.

git status --short (from job root repo):