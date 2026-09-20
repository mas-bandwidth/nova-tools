RESULT tools22-dog-nova-bus-first-run-r10 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
DRIFT 2 findings
TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 10 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

Setup: git rev-parse HEAD = 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 (matches the card's base). `nova-version` was not on PATH; I built ./cmd/nova-bus and ./cmd/nova-version from the pinned tree into <JOBDIR>/scratch/bin and confirmed `nova-version version` embeds sha 5298f6be12ea, so the binary is the base build. Bus made as line 363 tells: copied cmd/nova-bus/testdata/example-bus out, gave it a repository of its own per its README recipe (`git init -b main && git add -A && git commit -m 'the bus'`). The draft placeholder was replaced with a body before send, as the section's own prose instructs the writer to do. All commands run verbatim from <JOBDIR>/scratch.

1 | nova-bus names --bus ./bus | exit 0 | CLEAN
2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | exit 0 | CLEAN
3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | exit 1 | DRIFT
4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | exit 1 | CLEAN
5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | exit 1 | DRIFT

DRIFT docs/CLI.md:375 doc says `SEND OK id=bo-a57f65f4f21c path=from-bo/2026-09-12T2033Z-gate-a57f65f4f21c.md commit=ae0580b5f796c4593641c6ebb9a58846d5795b55 pushed=true attempts=1 wakes=1 body_bytes=46` | tool printed `SEND REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository` | exit 1. The section's setup (line 363) defers to the example README's recipe, which is only `git init -b main && git add -A && git commit` and never creates the `origin` remote this documented `--remote origin` block needs, so the example cannot be run as written.

DRIFT docs/CLI.md:381 doc says `INBOX SCOPE mode=full cursor=- changed=0 carrying=3` then the gate `INBOX NOTE` and a closing `INBOX CURSOR commit=ae0580b5f796c4593641c6ebb9a58846d5795b55 carrying=3 pushed=true attempts=1` | tool printed `INBOX SCOPE mode=full cursor=- changed=0 carrying=2` with no gate NOTE and no CURSOR line, ending `INBOX REFUSED: ... fatal: 'origin' does not appear to be a git repository` | exit 1. Cascade of the same missing step: with `send` never having pushed, the full read shows carrying=2 and the fetch for the cursor replacement cannot run.

RAN 5
SKIPPED 0

First 15 lines of output for each DRIFT, verbatim:

DRIFT 1 (docs/CLI.md:375):
SEND REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository\x0afatal: Could not read from remote repository.\x0a\x0aPlease make sure you have the correct access rights\x0aand the repository exists.
(exit 1)

DRIFT 2 (docs/CLI.md:381):
INBOX SCOPE mode=full cursor=- changed=0 carrying=2
INBOX OPEN carrying=2 heard=1 large=false remedy=inbox --advance
INBOX HEARD id=bo-222222222222 from=Bo addr=to at=2026-09-09T14:00:00Z path=from-bo/2026-09-09T1400Z-the-windows-runner-222222222222.md: The Windows runner skips three steps
INBOX RECEIPT id=bo-111111111111 from=Bo addr=to at=2026-09-09T13:00:00Z path=from-bo/2026-09-09T1300Z-heard-111111111111.md: Heard
INBOX OK as=Ada carrying=2 open=1 notes=0 receipts=1 heard=1 unaddressed=0 unreadable=0
INBOX REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository\x0afatal: Could not read from remote repository.\x0a\x0aPlease make sure you have the correct access rights\x0aand the repository exists.
(exit 1)

Not drift (saying so): values that differ run to run — ids (bo-a57f65f4f21c vs bo-96382aba22bd), commit shas, path timestamps, at= instants, body_bytes (46 vs 47, the writer's body and its trailing newline), and ANSI-less cosmetics. Block 4 (docs/CLI.md:378) printed the documented `INBOX REFUSED: the cursor 3f9a1c2b8d40e7c6a5b4938271605f4e3d2c1b0a is not an ancestor of HEAD ...` line verbatim, exit 1; the doc states no exit code for it. Block 2's `DRAFT NOTE redirect this to a file, then send: ...` is stderr (TESTS.md:96-97 documents it); stdout carried only the headers the doc's prose names, exit 0.

Supplementary check, for completeness only: re-ran blocks 3-5 with an `origin` set up exactly as the repo's own transcript fixture does (bare repo beside the bus, pushed, per cmd/nova-bus/firstrun_test.go:firstTrialBus and docs/TESTS.md:41). Everything then agreed with the document field-for-field — `SEND OK ... pushed=true attempts=1 wakes=1 body_bytes` (exit 0), block 4's REFUSED line, and block 5's `carrying=3` with the gate NOTE and the closing `INBOX CURSOR ... carrying=3 pushed=true attempts=1` (exit 0). So the tool itself matches the section once the missing `origin` step is supplied; the one defect is that the section and the README recipe it points to never tell a reader to create it.

Left owed: none — the whole section was exercised. The only unjudgeable-by-document item is exit codes the document never states.

git status --short (from repo) printed nothing.