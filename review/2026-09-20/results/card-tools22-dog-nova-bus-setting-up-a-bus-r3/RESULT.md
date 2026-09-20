RESULT tools22-dog-nova-bus-setting-up-a-bus-r3 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
BROKEN the installed nova-bus/nova-version binaries at /home/ubuntu/.local/bin are unreadable inside this sandbox; every invocation fails with "Permission denied" (exit 126), so the tool could not run at all

TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 3 of 24
BUILD unreadable — `nova-version` printed nothing; /usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied (exit 126). The installed build could not be confirmed.

| n | command | exit | status |
|---|---------|------|--------|
| 1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | 0 | CLEAN |
| 2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | 128 | DRIFT |
| 3 | nova-bus check --bus ~/my-bus --full | 126 | BROKEN |

DRIFT docs/CLI.md:429-431 doc says the three-line block copies the example bus and commits it, then runs `nova-bus check --bus ~/my-bus --full` | line 3: `git commit -m 'the bus'` failed with `fatal: empty ident name (for <ubuntu@spacegame.losangeles>) not allowed` because no git identity is configured on this machine, a step the section never shows | exit 128
DRIFT docs/CLI.md:431 doc says `nova-bus check --bus ~/my-bus --full` | tool printed `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied` | exit 126

RAN 3
SKIPPED 0

First 15 lines of output for each DRIFT:

DRIFT 2 (`git commit -m 'the bus'`):
```
Initialized empty Git repository in /home/ubuntu/rowan-working/tmp/5127b72f-3dfa-4bf1-ab1d-c7fd679ab2d7-card-tools22-dog-nova-bus-setting-up-a-bus-r3/data/my-bus/.git/
Author identity unknown

*** Please tell me who you are.

Run

  git config --global user.email "you@example.com"
  git config --global user.name "Your Name"

to set your account's default identity.
Omit --global to set the identity only in this repository.

fatal: empty ident name (for <ubuntu@spacegame.losangeles>) not allowed
```
(second run, after `git init -b main` already succeeded, prints `Reinitialized existing Git repository` first; exit 128)

DRIFT 3 (`nova-bus check --bus ~/my-bus --full`):
```
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied
```

STEP 1 confirmation: `git rev-parse HEAD` printed `5298f6be12eaa0f7e6622334d2b6a1eb427649e3` (matches the card's base). `nova-version` could not execute: `/home/ubuntu/.local/bin/nova-version` is not readable/executable within the landlock sandbox (read grants cover the workspace root, the harness dir, /home/ubuntu/sdk and /home/ubuntu/go/pkg/mod, but not /home/ubuntu/.local/bin). Same denial hits `nova-bus`. Because the installed binary could not be run at all, no section command that invokes the tool could be exercised; the git `cp` and `git init`/`add` steps ran (cp exit 0) but `git commit` needs a git identity the section never supplies.

Left owed: whether `nova-bus check --full` output matches the section could not be judged at all, since the tool binary is unreadable in this sandbox. The `participants.json` JSON block (lines 398-422) is a fixture, not a command, so it was not executed. `git status --short` at the repo prints nothing (working tree clean).