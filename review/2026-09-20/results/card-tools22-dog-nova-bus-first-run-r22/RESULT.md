RESULT tools22-dog-nova-bus-first-run-r22 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
DRIFT 1 findings
TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 22 of 24
BUILD nova-version 5298f6be linux/amd64 go1.26.6
| n | command | exit | status |
|---|---------|------|--------|
| 1 | nova-bus names --bus ./bus | 0 | CLEAN |
| 2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | 0 | DRIFT |
| 3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | N/A | SKIP no git remote 'origin' configured; section describes sending via git but does not tell how to create a remote |
| 4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | 1 | CLEAN |
| 5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | 0 | CLEAN |
DRIFT docs/CLI.md:391 doc says "`draft` prints the header a first note needs — `From:`, `To:` and `Subject:` — and nothing else" | tool printed additional line: `DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>` | exit 0

RAN 4
SKIPPED 1

Left owed send output entirely — could not judge against doc because the origin remote required by the documented `send` command was not available; section gives no instruction for creating one.

git status --short:
(nothing)
