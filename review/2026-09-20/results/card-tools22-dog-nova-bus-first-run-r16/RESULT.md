RESULT tools22-dog-nova-bus-first-run-r16 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
DRIFT 1 findings

TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 16 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

Setup notes: no nova-* binary was installed on this machine (`nova-version` was
not on PATH), so I built ./bin/nova-version and ./bin/nova-bus from the pinned
checkout at 5298f6be12ea (the doc's own build recipe, docs/CLI.md:3534) and ran
those. The version stamp carries the pinned sha, so the reading is against the
base build. The bus was made the way the section says: `cmd/nova-bus/testdata/example-bus`
copied out, `git init -b main`, committed, per the README recipe.

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-bus names --bus ./bus | 0 | CLEAN |
| 2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | 0 | CLEAN |
| 3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | 0 | CLEAN |
| 4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | 1 | CLEAN |
| 5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | 0 | CLEAN |

DRIFT docs/CLI.md:375 doc says `nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main` then prints `SEND OK id=… pushed=true …` | tool printed `SEND REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository` | exit 1. The section (line 363) points the reader at the example-bus README as "the recipe"; that recipe is `git init -b main && git add -A && git commit -m 'the bus'` and stops there. It never creates the bare `origin` repository that `--remote origin` (send, and both inbox blocks) requires, so the example cannot be run as written — I had to create `bus.git` as a bare origin (as only TESTS.md's fixture note describes) before block 3 would run.

RAN 5
SKIPPED 0

First 15 lines of output for the DRIFT, verbatim:
SEND REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository\x0afatal: Could not read from remote repository.\x0a\x0aPlease make sure you have the correct access rights\x0aand the repository exists.

Left owed:
- body_bytes: block 3 printed `body_bytes=47` vs the doc's `body_bytes=46`. The section never specifies the draft body; the draft template ends with a trailing newline, so replacing the placeholder keeps it and yields 47. Writing the body without the trailing newline reproduces `body_bytes=46` exactly (verified). I treat the count as body-content-dependent, not a tool/doc disagreement, but the doc's number is not reproducible by the literal "replace the placeholder" step.
- Block 2: the tool prints `DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>` to stderr, which the doc's block does not show (the doc shows no output line for it). TESTS.md documents this line as stderr, so not a disagreement.
- Block 4 exit 1 is the tool's "ran and said NO" code (a refused cursor, per the help text), consistent with the doc's INBOX REFUSED example.
- The verifiable lines in blocks 4-5 (cursor value, `INBOX HEARD id=bo-222222222222 …`, `INBOX RECEIPT id=bo-111111111111 …`, `INBOX OK as=Ada carrying=3 open=2 notes=1 receipts=1 heard=1 unaddressed=0 unreadable=0`) matched exactly; ids/paths/timestamps/commits differ as content-and-time-derived values (cosmetic).

git status --short in the repo (pasted):