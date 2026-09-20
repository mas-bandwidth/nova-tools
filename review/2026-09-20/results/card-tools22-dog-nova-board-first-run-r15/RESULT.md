RESULT tools22-dog-nova-board-first-run-r15 sha=5298f6be12ea — read `nova-board First run` against docs/CLI.md:2586-2643 and say CLEAN or DRIFT
DRIFT 7 findings
TOOL nova-board
VERB First run
DOC docs/CLI.md:2586-2643
REPLICA 15 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/amd64 go1.27.1

| # | Command | exit | Status |
|---|---------|------|--------|
| 1 | nova-board quickstart --dir ./board --stale 10m | 0 | DRIFT |

DRIFT docs/CLI.md:2600 doc says `created=false` | tool printed `created=true` | exit 0
DRIFT docs/CLI.md:2601-2604 doc says 4 BOARD LINE entries (name=emma open=1 overdue=1 stale=1, name=bo open=1 overdue=0 stale=1, name=rowan open=1 overdue=0 stale=1, name=freddy open=1 overdue=0 stale=1) | tool printed no BOARD LINE entries | exit 0
DRIFT docs/CLI.md:2605-2606 doc says 2 BOARD LEG entries (leg=cpp owed=1 probed=0, leg=go owed=0 probed=1) | tool printed no BOARD LEG entries | exit 0
DRIFT docs/CLI.md:2607 doc says `BOARD NEXT the oldest OVERDUE card 283e2dd1e5c5424d7637d28488365e98, owed by emma, due 2026-09-11T09:00:00Z -- the token ledger has no September rows yet` | tool printed `BOARD NEXT nothing owed` | exit 0
DRIFT docs/CLI.md:2608 doc says `BOARD OK cards=5 open=4 closed=1 stale=4 overdue=1 owed=1 lines=4 conflicts=0 quarantined=0 shown=8 backend=dir source=./board` | tool printed `BOARD OK cards=0 open=0 closed=0 stale=0 overdue=0 owed=0 lines=0 conflicts=0 quarantined=0 shown=2 backend=dir source=./board` | exit 0
DRIFT docs/CLI.md:2609 doc says `'the token ledger'` in QUICKSTART LINE n=1 what=check text | tool printed `'the thing you are about to file'` | exit 0
DRIFT docs/CLI.md:2610 doc says `'the token ledger has no September rows yet'` in QUICKSTART LINE n=2 what=add text | tool printed `'the thing you are about to file'` | exit 0

RAN 1
SKIPPED 0

First 15 lines of output for DRIFT (command 1):
QUICKSTART OK backend=dir source=./board stale=10m0s created=true: the board, then the rule every filer runs in front of add
BOARD NEXT nothing owed
BOARD OK cards=0 open=0 closed=0 stale=0 overdue=0 owed=0 lines=0 conflicts=0 quarantined=0 shown=2 backend=dir source=./board
QUICKSTART LINE n=1 what=check: "nova-board check --dir ./board --words 'the thing you are about to file' || { [ $? -eq 1 ] && exit 0; exit 2; }"
QUICKSTART LINE n=2 what=add: "nova-board add --dir ./board --as <your-name> --text 'the thing you are about to file' --by 4h --default 'the filer files it as a known gap'"
QUICKSTART NOTE check EXITS 1 WHEN IT MATCHES, so the guard reads "if it is already there, stop"; the exit-2 arm tells a NO from a board that could not be read
QUICKSTART NOTE --stale 10m0s is this family's number and this run passed it in words: there is no default duration here, and --by and --default are required on every card

Left owed
None — all differences stem from the documented example assuming a pre-seeded board fixture (`cmd/nova-board/testdata/example-board`) that the section mentions descriptively at line 2590 but never instructs the reader to copy. Running the verbatim command fresh creates an empty board (`created=true`, zero counts, generic placeholder text), whereas the document expects `created=false` with four named boards, two legs, and specific card text.
