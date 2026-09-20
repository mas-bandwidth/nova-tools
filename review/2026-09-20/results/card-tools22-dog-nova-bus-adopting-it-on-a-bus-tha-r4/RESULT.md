RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r4 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
CLEAN

TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 4 of 24
BUILD no nova-version on PATH (command not found); built both binaries from the pinned checkout (git rev-parse HEAD = 5298f6be12eaa0f7e6622334d2b6a1eb427649e3) into the job's scratch. `nova-version version` printed: nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1 (sha prefix matches the card's base)

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | 0 | CLEAN |
| 2 | nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main | 0 | CLEAN |

RAN 2
SKIPPED 0

Left owed: the two blocks carry placeholders `<dir>` and `<you>` and presuppose a bus that already exists (the section's own premise) with an `origin` remote. No nova-bus binary was installed, so I built nova-bus/nova-version from the pinned base and exercised the blocks over the documented example bus copied out per `### Setting up a bus` (docs/CLI.md) and given a repository of its own, with a local bare git repository as `origin` (the sandbox has no network and the chapter's own recipe says to push). Prose claims in the section were each exercised and matched: block 1 exited 0 (`BUS SCOPE mode=full cursor=- changed=0`, `BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0`); block 2 exited 0 and printed exactly one `INBOX LEGACY before=2026-09-20T21:25:04Z notes=2 unreadable=0` line with no per-note listing, matching "not carried and not listed, only counted on one INBOX LEGACY line"; a first `--advance` on a cursorless lane with notes older than today was refused (exit 1) and handed the exact `--legacy-now` line, matching the section; a cursor whose switch-day line is a date at today printed one `INBOX SWITCH` line naming the command that redraws it at an instant; `check --full --rebuild-index` and the steady-state `inbox --as <you> --advance` loop both ran exit 0. The sentence "the loop is `inbox --as <you> --advance` with no flag at all" is prose shorthand — the tool still requires `--bus`, `--receipt-max-words`, `--remote` and `--branch`, which the section's own fenced block establishes — judged not drift. `git status --short` in the repo printed nothing.