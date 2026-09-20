RESULT tools22-dog-nova-bus-first-run-r8 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
SKIP installed nova-* tools unreachable: /home/ubuntu/.local/bin/nova-version and /home/ubuntu/.local/bin/nova-bus each return `Permission denied` (exit 126) from the sandbox, so STEP 1's installed-build check cannot be completed and the section cannot be exercised against the installed tool

TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 8 of 24
BUILD none — installed nova-version printed nothing (`/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied`, exit 126). A from-source build at the pinned base prints `nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea` (supplemental only; see Left owed).

STEP 1.
`git rev-parse HEAD` -> 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 (matches the card's base; not BLOCKED on head).
`nova-version` -> could not run. Exact output, every attempt:
```
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied
```
exit 126. `which nova-version` and `type -a` resolve only to /home/ubuntu/.local/bin/nova-version. The directory /home/ubuntu/.local/bin is fully walled off: `ls` of it, `file`, `head`, `cp`, the Read tool, and exec all return Permission denied (the harness's own native.log describes the wall as `backend=landlock abi=8 used=6 read=3 read-noexec=1 write=5 net=nopromise`). No other nova-* binary exists on PATH (/usr/local/bin, /usr/bin, /home/ubuntu/sdk/bin, /home/ubuntu/go/bin contain none). The installed build can therefore not be confirmed, and a reading of the document against a different binary would prove nothing.

Command blocks (installed tool; run from <JOBDIR>/scratch, made first):
| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-bus names --bus ./bus | 126 | SKIP |
| 2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | 126 | SKIP |
| 3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | 126 | SKIP |
| 4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | 126 | SKIP |
| 5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | 126 | SKIP |

Every block failed before the tool ran, identically:
```
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied
```
exit 126.

DRIFT lines: none — the official verdict is SKIP, not DRIFT.

RAN 0
SKIPPED 5

Left owed
- The whole section, against the installed tool. Every documented command exits 126 with `Permission denied` before the tool executes, so nothing printed and nothing could be compared; the section cannot be tried here against the installed binary. The installed build's identity (STEP 1) is unverifiable, which per the card makes any reading against a substitute binary inconclusive.
- Supplemental, NOT the verdict basis (the card gates the verdict on the installed binary): I built nova-bus and nova-version from the pinned base source (`go build -o ../scratch/nova-version ./cmd/nova-version`, `go build -o ../scratch/nova-bus ./cmd/nova-bus`; both exit 0) and exercised the section against that build from scratch, copying out `cmd/nova-bus/testdata/example-bus` and giving it a repository per its README recipe (git init -b main, add, commit). Results against the base build:
  - cmd 1 `names` exit 0: output identical to the doc line-for-line (NAMES NAME x3, NAMES GROUP, NAMES OK participants=3 groups=1 senders=2).
  - cmd 2 `draft` exit 0: prints From:/To:/Subject: + `<the note goes here>` placeholder to stdout (matches the doc's prose).
  - cmd 3 `send` exit 1: FAILS verbatim because the recipe never creates `origin` — `SEND REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository`. The doc's transcript prints `SEND OK id=bo-a57f65f4f21c ... pushed=true`, so as written the example is missing a step (the tests create a bare origin: firstrun_test.go `git init --bare` + `git remote add origin` + push). Candidate DRIFT, reported here only.
  - After adding a bare `origin` remote, `send` exit 0: `SEND OK id=... path=from-bo/...-gate-....md commit=... pushed=true attempts=1 wakes=1 body_bytes=47` — field names/order match the doc; only id/path-timestamp/commit/body_bytes differ (ids, timestamps, shas and content-dependent counts are the card's listed non-drift cosmetics).
  - cmd 4 `inbox --advance` exit 1: refusal text identical to the doc, including the same cursor sha `3f9a1c2b8d40e7c6a5b4938271605f4e3d2c1b0a`.
  - cmd 5 `inbox --full --advance` exit 0: seven INBOX lines, all field names/order/spelling identical to the doc (SCOPE, OPEN, NOTE, HEARD, RECEIPT, OK, CURSOR); only the new note's id, at/path timestamps and the CURSOR commit sha differ (cosmetic). HEARD and RECEIPT lines match exactly.
  - net: against a from-source base build the section matches the doc except that the send/inbox sequence cannot be run as written without an `origin` remote the recipe never creates.

git status --short (repo):
```
```
(empty — nothing from the repository; no files added, edited or committed.)