RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r3 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 3 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1 (bare `nova-version` prints `VERSION REFUSED: a verb is required`; `nova-version version` prints the line above. Its sha is 5298f6be12ea, matching the card base.)

STEP 1: `git rev-parse HEAD` printed 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 (matches). nova-* tools are Go commands in this tree and are not preinstalled on PATH; I built ./cmd/... at the pinned base into scratch/bin and ran from there, so the exercised binaries are the base build.

Fixture: per docs/CLI.md `### Setting up a bus` / `### First run` (same ## nova-bus section), I copied cmd/nova-bus/testdata/example-bus to scratch/bus, gave it a repository of its own with `git init -b main`, committed `the bus`, and gave it an origin (bare remote scratch/bus.git, push main) because the documented commands carry `--remote origin --branch main` and the tool's own tests clone from a bare origin.

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | 0 | CLEAN |
| 2 | nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main | 0 | CLEAN |

Block 1 printed:
BUS SCOPE mode=full cursor=- changed=0
BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0

Block 2 (--as Ada) printed:
INBOX SCOPE mode=full cursor=- changed=0 carrying=0
INBOX LEGACY before=2026-09-20T21:24:45Z notes=2 unreadable=0
INBOX OPEN carrying=0 heard=0 large=false remedy=inbox --advance
INBOX OK as=Ada carrying=0 open=0 notes=0 receipts=0 heard=0 unaddressed=0 unreadable=0
INBOX CURSOR commit=1aa4c945d282c5c2ee53cfa800231c7ba842e442 carrying=0 pushed=true attempts=1

This matches the doc's prose: the two dated-before-the-line notes were counted on one `INBOX LEGACY` line (`notes=2`), not carried (`carrying=0`) and not listed, and `--legacy-now` produced an instant. The refusal claim was also verified: a reader's first bare `--advance` (Bo) over notes dated before now was refused (exit 1) with `INBOX REFUSED: this is the first advance on from-bo/CURSOR and 1 of the 1 notes it would carry are dated before now...` handing the exact `--full --legacy-now --advance` line or `--carry-history`, exactly as the doc says. The follow-ups the doc names also ran clean: `check --full --rebuild-index` (exit 0) and the loop `inbox --as Ada --receipt-max-words 40 --advance --remote origin --branch main` (exit 0), which needs no switch-day flag.

RAN 2, SKIPPED 0.

DRIFT lines: none.

Left owed: the doc's conditional "if your cursor's line is a date standing at today or later, every run prints one INBOX SWITCH line" — the example-bus cursor is a commit, not a date, and this section does not show how to construct a date-line cursor, so that branch could not be exercised. It is a conditional, not a disagreement.

git status --short (from repo/, the job clone):
(empty — nothing printed)