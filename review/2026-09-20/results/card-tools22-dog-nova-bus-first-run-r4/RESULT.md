RESULT tools22-dog-nova-bus-first-run-r4 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
DRIFT 2 findings

- TOOL nova-bus
- VERB First run
- DOC docs/CLI.md:361-392
- REPLICA 4 of 24
- BUILD nova-version was not installed on PATH (`/bin/bash: nova-version: command not found`), so STEP 1's installed-build check could not be done through the card's command. I built `nova-bus` and `nova-version` from the pinned base into `<JOBDIR>/scratch`; both report `nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/amd64 go1.27.1` / `nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/amd64 go1.27.1`, i.e. build identity 5298f6be12ea = the card's base sha. The reading below is against that build.

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-bus names --bus ./bus | 0 | CLEAN |
| 2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | 0 | CLEAN |
| 3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | 1 | DRIFT |
| 4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | 1 | CLEAN |
| 5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | 1 | DRIFT |

DRIFT docs/CLI.md:375 doc says the send example runs as written and prints `SEND OK id=bo-a57f65f4f21c path=from-bo/2026-09-12T2033Z-gate-a57f65f4f21c.md commit=ae0580b5f796c4593641c6ebb9a58846d5795b55 pushed=true attempts=1 wakes=1 body_bytes=46` | tool printed `SEND REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository` | exit 1

DRIFT docs/CLI.md:381 doc says the full read prints `INBOX SCOPE mode=full cursor=- changed=0 carrying=3` ... `INBOX OK as=Ada carrying=3 open=2 notes=1 receipts=1 heard=1 unaddressed=0 unreadable=0` `INBOX CURSOR commit=... carrying=3 pushed=true attempts=1` | tool printed `INBOX SCOPE mode=full cursor=- changed=0 carrying=2` `INBOX OPEN carrying=2 heard=1 large=false remedy=inbox --advance` `INBOX HEARD ...` `INBOX RECEIPT ...` `INBOX OK as=Ada carrying=2 open=1 notes=0 receipts=1 heard=1 unaddressed=0 unreadable=0` then `INBOX REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository` | exit 1

RAN 5, SKIPPED 0.

First 15 lines of output for DRIFT 1 (block 3, send), verbatim:
```
SEND REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository
fatal: Could not read from remote repository.

Please make sure you have the correct access rights
and the repository exists.
```

First 15 lines of output for DRIFT 2 (block 5, inbox --full --advance), verbatim:
```
INBOX SCOPE mode=full cursor=- changed=0 carrying=2
INBOX OPEN carrying=2 heard=1 large=false remedy=inbox --advance
INBOX HEARD id=bo-222222222222 from=Bo addr=to at=2026-09-09T14:00:00Z path=from-bo/2026-09-09T1400Z-the-windows-runner-222222222222.md: The Windows runner skips three steps
INBOX RECEIPT id=bo-111111111111 from=Bo addr=to at=2026-09-09T13:00:00Z path=from-bo/2026-09-09T1300Z-heard-111111111111.md: Heard
INBOX OK as=Ada carrying=2 open=1 notes=0 receipts=1 heard=1 unaddressed=0 unreadable=0
INBOX REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository
fatal: Could not read from remote repository.

Please make sure you have the correct access rights
and the repository exists.
```

Left owed — Both DRIFTs are the same missing step: every command block uses `--remote origin` and expects `pushed=true`, but neither this section (docs/CLI.md:361-392) nor the recipe it names (cmd/nova-bus/testdata/example-bus/README.md, which says only `cp -R`, `git init -b main`, `git add -A`, `git commit`) tells the reader to create the `origin` remote. On a bus made exactly as the document says (copied-out example bus, `git init -b main`, committed, no remote), block 3 and block 5 cannot complete and print `SEND REFUSED`/`INBOX REFUSED` about `origin` (exit 1). I separately verified — the way this repo's own transcript test (cmd/nova-bus/firstrun_test.go, firstTrialBus) builds the fixture — that once a bare `origin` exists and the bus is pushed, the tool reproduces the documented field shapes exactly: `SEND OK ... pushed=true attempts=1 wakes=1` and `INBOX SCOPE carrying=3` ... `INBOX CURSOR ... pushed=true`, with block 4's `INBOX REFUSED` line identical to the doc. So the drift is a step missing from the document, not a disagreement in the tool's output grammar. Cosmetic (not counted): ids/commits/timestamps/paths and `body_bytes` (my prepared draft body produced `body_bytes=47` against the doc's example `46`; the doc never fixes the body text) differ with the fixture body and the run's clock. The `draft` verb also writes a stderr hint line (`DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>`) that the doc does not show; the doc's "prints the header ... and nothing else" describes the redirected stdout, which is exactly the header, so not counted. `nova-version` could not be run as STEP 1 prescribes because the tool is not installed on this machine; the build identity is confirmed from the base-built binaries instead. `git status --short` in the repo prints nothing (clean).