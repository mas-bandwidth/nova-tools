RESULT tools22-dog-nova-bus-first-run-r21 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
DRIFT 6 findings
TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 21 of 24
BUILD v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6 (built from dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3)
| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-bus names --bus ./bus | 0 | CLEAN |
| 2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | 0 | DRIFT |
| 3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | 1 | DRIFT |
| 4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | 1 | CLEAN |
| 5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | 1 | DRIFT |

DRIFT docs/CLI.md:391 doc says "`draft` prints the header a first note needs — From:, To: and Subject: — and nothing else" | tool wrote: From: Bo\nTo: Ada\nSubject: gate\n\n<the note goes here>\nDRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file> | exit 0
DRIFT docs/CLI.md:376 doc shows SEND OK id=bo-a57f65f4f21c path=from-bo/2026-09-12T2033Z-gate-a57f65f4f21c.md commit=ae0580b5f796c4593641c6ebb9a58846d5795b55 pushed=true attempts=1 wakes=1 body_bytes=46 | tool printed SEND FAIL draft.md: the body is the unedited template placeholder (<the note goes here>) | exit 1
DRIFT docs/CLI.md:382 doc says carrying=3 | tool carried=2 | exit 1
DRIFT docs/CLI.md:384 doc shows INBOX NOTE id=bo-a57f65f4f21c from=Bo addr=to at=2026-09-12T20:33:50Z path=from-bo/2026-09-12T2033Z-gate-a57f65f4f21c.md: gate | tool did not produce INBOX NOTE line | exit 1
DRIFT docs/CLI.md:387 doc says open=2 notes=1 carrying=3 | tool open=1 notes=0 carrying=2 | exit 1
DRIFT docs/CLI.md:388 doc shows INBOX CURSOR commit=ae0580b5f796c4593641c6ebb9a58846d5795b55 carrying=3 pushed=true attempts=1 | tool produced INBOX REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository instead | exit 1

RAN 5
SKIPPED 0

CMD 2 output (first 15 lines):
From: Bo
To: Ada
Subject: gate

<the note goes here>
DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>

CMD 3 output (first 15 lines):
SEND FAIL draft.md: the body is the unedited template placeholder (<the note goes here>)

CMD 5 output (first 15 lines):
INBOX SCOPE mode=full cursor=- changed=0 carrying=2
INBOX OPEN carrying=2 heard=1 large=false remedy=inbox --advance
INBOX HEARD id=bo-222222222222 from=Bo addr=to at=2026-09-09T14:00:00Z path=from-bo/2026-09-09T1400Z-the-windows-runner-222222222222.md: The Windows runner skips three steps
INBOX RECEIPT id=bo-111111111111 from=Bo addr=to at=2026-09-09T13:00:00Z path=from-bo/2026-09-09T1300Z-heard-111111111111.md: Heard
INBOX OK as=Ada carrying=2 open=1 notes=0 receipts=1 heard=1 unaddressed=0 unreadable=0
INBOX REFUSED: the fetch that would say whether this branch is level with origin/main failed: git -C ./bus fetch origin main: exit status 128: fatal: 'origin' does not appear to be a git repository\x0afatal: Could not read from remote repository.\x0a\x0aPlease make sure you have the correct access rights\x0aand the repository exists.

Left owed
The section never tells you how to create a git remote called "origin". Commands 3 (send) and 5 (inbox --full --advance) both depend on `--remote origin --branch main`. CMD 3 could not complete without origin, which cascaded into all CMD 5 discrepancies (carrying count, absent INBOX NOTE, different INBOX OK fields, INBOX CURSOR replaced by fetch REFUSED). If origin were configured and the draft placeholder replaced between commands 2 and 3, the send/inbox flow might match the document. Also: CMD 3's fenced code block shows send directly after draft without showing the required step of replacing `<the note goes here>` in the file before sending — the **Reading it** prose mentions this replacement but it is not in the fenced code block.
