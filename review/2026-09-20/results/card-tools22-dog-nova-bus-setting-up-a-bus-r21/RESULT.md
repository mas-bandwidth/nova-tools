RESULT tools22-dog-nova-bus-setting-up-a-bus-r21 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
BROKEN the installed nova-bus/nova-version binaries cannot be executed at all: `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied` — the Landlock sandbox wall (see native.log: "SANDBOX OK backend=landlock ... read=3") denies read/exec of `/home/ubuntu/.local/bin`

TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 21 of 24
BUILD unconfirmed — `nova-version` could not be executed; it printed: `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied`

Table (one row per fenced block in the section):

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | participants.json (JSON data literal, step 2, docs/CLI.md:397-419) | n/a | data, not a command — not run |
| 2 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | exit 0 | CLEAN |
| 3 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | exit 128 | DRIFT |
| 4 | nova-bus check --bus ~/my-bus --full | exit 126 | BROKEN |

DRIFT docs/CLI.md:430 doc says cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | tool printed "Author identity unknown\n\n*** Please tell me who you are.\n\nRun\n\n  git config --global user.email "you@example.com"\n  git config --global user.name "Your Name"\n\nto set your account's default identity.\nOmit --global to set the identity only in this repository.\n\nfatal: empty ident name (for <ubuntu@spacegame.losangeles>) not allowed" | exit 128

First 15 lines of output for the DRIFT (verbatim):

```
Initialized empty Git repository in /home/ubuntu/rowan-working/tmp/75d80aea-2c3a-4dae-a534-791b71279675-card-tools22-dog-nova-bus-setting-up-a-bus-r21/data/my-bus/.git/
Author identity unknown

*** Please tell me who you are.

Run

  git config --global user.email "you@example.com"
  git config --global user.name "Your Name"

to set your account's default identity.
Omit --global to set the identity only in this repository.

fatal: empty ident name (for <ubuntu@spacegame.losangeles>) not allowed
```

Exact failure for the BROKEN command (docs/CLI.md:431), verbatim:

```
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied
```

RAN 3 (commands 2, 3, 4; command 1 is a data literal, not a command)
SKIPPED 0

Notes on how the commands were run:
- `cp` (docs/CLI.md:429) was run from the repo root (`repo/`), because the documented relative path `cmd/nova-bus/testdata/example-bus` only resolves there; the doc's step 3 implies repo-root cwd. It copied the shipped example bus to `$HOME/my-bus` (HOME is redirected to `<job-parent>/data` in this sandbox). Exit 0.
- `nova-version` (STEP 1) could not run either; the installed build could not be confirmed against base sha 5298f6be. The repo HEAD is `5298f6be12eaa0f7e6622334d2b6a1eb427649e3` as required.
- Every `nova-bus`/`nova-version` invocation fails identically because the sandbox denies opening anything under `/home/ubuntu/.local/bin` (same denial hits `/home/ubuntu/go/bin`, while `/usr`, `/bin`, `/etc` are readable). This is environmental, not a doc/tool disagreement, but it means the tool cannot be run at all on this machine.

`git status --short` in `repo/` at the end printed nothing (exit 0). Pasting it:

```
```

Left owed:
- docs/CLI.md:431 `nova-bus check --bus ~/my-bus --full` could not be judged (exit and output cannot be obtained — the binary cannot be executed in this sandbox).
- The `participants.json` literal (docs/CLI.md:397-419) was not exercised: it is data, and the doc's commands copy the shipped example-bus which carries its own roster; validating the literal needs `nova-bus`, which cannot run.
- Whether the tool's output field names/spelling match the doc could not be judged at all — the tool never produced output.
- STEP 1's installed-build check is owed: `nova-version` cannot run, so no sha comparison to the base was possible.