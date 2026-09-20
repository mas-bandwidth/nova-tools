RESULT tools22-dog-nova-bus-first-run-r20 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
DRIFT 1 findings
TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 20 of 24
BUILD v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6
| 1 | nova-bus names --bus ./bus | exit 0 | CLEAN
| 2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | exit 0 | CLEAN
| 3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | exit 1 | SKIP
| 4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | exit 1 | CLEAN
| 5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | exit 0 | CLEAN
DRIFT docs/CLI.md:375 doc says example runs `nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main` cleanly after the draft command above | tool printed `SEND FAIL draft.md: the body is the unedited template placeholder (<the note goes here>)` | exit 1
RAN 4
SKIPPED 1
Command 3 output (first 15 lines):
SEND FAIL draft.md: the body is the unedited template placeholder (<the note goes here>)
Left owed: Command 3 could not be judged because the document describes replacing `<the note goes here>` with a body "before `send`" in its prose (line 391: "the writer replaces the `<the note goes here>` placeholder with a body before `send`") but the fenced command blocks provide no instruction between `draft` and `send` to perform that replacement. The documented example requires a manual editing step that is absent from the runnable commands. Additionally, `draft` prints an extra DRAFT NOTE informational line ("DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>") which contradicts the prose claim that "`draft` prints the header ... and nothing else", though this did not affect the redirect capture.
