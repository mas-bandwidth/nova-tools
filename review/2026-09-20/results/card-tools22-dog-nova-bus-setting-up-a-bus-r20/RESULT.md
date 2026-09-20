RESULT tools22-dog-nova-bus-setting-up-a-bus-r20 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
BROKEN the installed nova tools cannot run at all in this sandbox: `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied` (exit 126)

TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 20 of 24
BUILD nova-version refused to run, so no build sha was ever printed: `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied` (exit 126); installed build unconfirmed

| n | command | exit | status |
| 1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | not run | SKIP |
| 2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | not run | SKIP |
| 3 | nova-bus check --bus ~/my-bus --full | 126 | BROKEN |

No DRIFT lines: the section could not be exercised at all, so no line-by-line comparison against the document's output was possible.

RAN 1
SKIPPED 2

Exact failure of the one command attempted (run verbatim from <JOBDIR>/scratch, and again with an in-job --bus path to rule out the fixture):
```
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied
```
(exit 126, identical for both the documented `--bus ~/my-bus` form and `--bus <JOBDIR>/scratch`.) The binary sits in `/home/ubuntu/.local/bin`, a path the landlock sandbox denies read/exec on (`file` reports "writable, executable, regular file, no read permission"; `cat` and exec both return Permission denied). `nova-version` fails the same way, so STEP 1's installed-build check could not pass.

Why rows 1-2 are SKIP rather than BROKEN: `~/my-bus` resolves to `$HOME/.../data/my-bus` (HOME is set to `/home/ubuntu/rowan-working/tmp/0cc0d926-.../data`), which is OUTSIDE the job directory. The card's RULES say "Never write outside the job directory", so the documented `cp`/`git init` steps cannot be run as written without violating the card. Even if they were run, the section's own final command `nova-bus check` cannot execute, so no comparison would be possible.

Left owed: every part of the section — whether `nova-bus check --bus ~/my-bus --full` runs and what it prints, and so the whole CLEAN/DRIFT judgement. The example bus fixture `cmd/nova-bus/testdata/example-bus/` exists in the repo (participants.json + from-ada/ + from-bo/ + README.md) and the repo HEAD is the pinned base `5298f6be12eaa0f7e6622334d2b6a1eb427649e3`, but the installed tool build could not be confirmed and the tool could not be executed.

git status --short (from repo): prints nothing (clean).