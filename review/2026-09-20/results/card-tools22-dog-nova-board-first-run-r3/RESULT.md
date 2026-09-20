RESULT tools22-dog-nova-board-first-run-r3 sha=5298f6be12ea — read `nova-board First run` against docs/CLI.md:2586-2643 and say CLEAN or DRIFT
DRIFT 6 findings
TOOL nova-board
VERB First run
DOC docs/CLI.md:2586-2643
REPLICA 3 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

| 1 | nova-board quickstart --dir ./board --stale 10m | exit 0 | DRIFT |

DRIFT docs/CLI.md:2594 doc says created=false | tool printed created=true | exit 0
DRIFT docs/CLI.md:2595-2600 doc shows 4 BOARD LINE and 2 BOARD LEG lines | tool printed none (empty board) | exit 0
DRIFT docs/CLI.md:2601 doc says "BOARD NEXT the oldest OVERDUE card 283e2dd1e5c5424d7637d28488365e98, owed by emma, due 2026-09-11T09:00:00Z -- the token ledger has no September rows yet" | tool printed "BOARD NEXT nothing owed" | exit 0
DRIFT docs/CLI.md:2602 doc says "cards=5 open=4 closed=1 stale=4 overdue=1 owed=1 lines=4 conflicts=0 quarantined=0 shown=8" | tool printed "cards=0 open=0 closed=0 stale=0 overdue=0 owed=0 lines=0 conflicts=0 quarantined=0 shown=2" | exit 0
DRIFT docs/CLI.md:2603 doc says words='the token ledger' | tool printed words='the thing you are about to file' | exit 0
DRIFT docs/CLI.md:2604 doc says text='the token ledger has no September rows yet' | tool printed text='the thing you are about to file' | exit 0

First 15 lines of output for command 1:
QUICKSTART OK backend=dir source=./board stale=10m0s created=true: the board, then the rule every filer runs in front of add
BOARD NEXT nothing owed
BOARD OK cards=0 open=0 closed=0 stale=0 overdue=0 owed=0 lines=0 conflicts=0 quarantined=0 shown=2 backend=dir source=./board
QUICKSTART LINE n=1 what=check: "nova-board check --dir ./board --words 'the thing you are about to file' || { [ $? -eq 1 ] && exit 0; exit 2; }"
QUICKSTART LINE n=2 what=add: "nova-board add --dir ./board --as <your-name> --text 'the thing you are about to file' --by 4h --default 'the filer files it as a known gap'"
QUICKSTART NOTE check EXITS 1 WHEN IT MATCHES, so the guard reads "if it is already there, stop"; the exit-2 arm tells a NO from a board that could not be read
QUICKSTART NOTE --stale 10m0s is this family's number and this run passed it in words: there is no default duration here, and --by and --default are required on every card

RAN 1
SKIPPED 0
Left owed: none. The section has one fenced command block. The document mentions cmd/nova-board/testdata/example-board as "a board the size of a first run" and the output shown corresponds to running against that data, but it does not instruct the reader to copy it to ./board before running the command. Running the command verbatim from a clean scratch produces output from an empty board, which differs from the document's example output. All 6 DRIFT findings stem from this single root cause: the board is not pre-populated, so quickstart shows an empty board with generic placeholder text.