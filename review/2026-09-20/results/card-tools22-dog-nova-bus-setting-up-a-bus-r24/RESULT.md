RESULT tools22-dog-nova-bus-setting-up-a-bus-r24 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
SKIP the installed nova-* tools cannot be executed in this sandbox: `nova-version` and `nova-bus` both fail `Permission denied` (exit 126); the Landlock read grant covers only the run dir, /home/gaffer/nova-bench/harness-v1.18.20 and /home/gaffer/nova-bench/sdk, not the install dir /home/gaffer/.local/bin, so STEP 1's build confirmation and the section's only command block cannot be exercised here.

TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 24 of 24
BUILD unverifiable — `nova-version` could not execute: `Permission denied`, exit 126

Table of the section's command block (one fenced block, three lines):

1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | n/a (not run) | SKIP
2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | n/a (not run) | SKIP
3 | nova-bus check --bus ~/my-bus --full | 126 on direct attempt | SKIP

DRIFT lines: none.

RAN 0
SKIPPED 3

Output captured for the attempted executions (first 15 lines each, verbatim):

`git rev-parse HEAD` (STEP 1, from repo/):
5298f6be12eaa0f7e6622334d2b6a1eb427649e3

`nova-version` (STEP 1):
/usr/bin/bash: line 1: /home/gaffer/.local/bin/nova-version: Permission denied

`nova-bus` (direct attempt, exit 126):
/usr/bin/bash: line 1: /home/gaffer/.local/bin/nova-bus: Permission denied

Supporting evidence that the install dir is outside the sandbox read grant:
cp: cannot open '/home/gaffer/.local/bin/nova-version' for reading: Permission denied
ls: cannot open directory '/home/gaffer/.local/bin': Permission denied

Left owed — the whole section could not be judged. STEP 1's build confirmation (`nova-version`) is impossible: the installed binary at /home/gaffer/.local/bin is not readable/executable inside the Landlock sandbox (read rules: run dir, /home/gaffer/nova-bench/harness-v1.18.20, /home/gaffer/nova-bench/sdk; /home/gaffer/.local is not granted). Because the installed build cannot be confirmed as 5298f6be, no section command was run against it (per the card, a reading against a different binary proves nothing), so no CLEAN/DRIFT judgement is possible. The block's `cp`/`git` lines also write to ~/my-bus, outside the job directory, which the card's RULES forbid. The documented example bus does exist at cmd/nova-bus/testdata/example-bus in the pinned tree.

git status --short (run from repo/, prints nothing):