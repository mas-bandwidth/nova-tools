RESULT tools22-dog-nova-board-first-run-r13 sha=5298f6be12ea
DRIFT 1 findings
TOOL nova-board
VERB First run
DOC docs/CLI.md:2586-2643
REPLICA 13 of 24
BUILD devel darwin/amd64 go1.27.1

| # | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-board quickstart --dir ./board --stale 10m | 0 | DRIFT |

DRIFT docs/CLI.md:2599 doc says $ nova-board quickstart --dir ./board --stale 10m prints BOARD LINE name=emma open=1 overdue=1 stale=1 etc. and BOARD NEXT the oldest OVERDUE card 283e2dd1e5c5424d7637d28488365e98 | tool printed empty board (created=true, cards=0, nothing owed) because ./board did not exist and the section never instructs how to populate it | exit 0

RAN 1
SKIPPED 0

Drift #1 first 15 lines (verbatim):
QUICKSTART OK backend=dir source=./board stale=10m0s created=true: the board, then the rule every filer runs in front of add
BOARD NEXT nothing owed
BOARD OK cards=0 open=0 closed=0 stale=0 overdue=0 owed=0 lines=0 conflicts=0 quarantined=0 shown=2 backend=dir source=./board
QUICKSTART LINE n=1 what=check: "nova-board check --dir ./board --words 'the thing you are about to file' || { [ $? -eq 1 ] && exit 0; exit 2; }"
QUICKSTART LINE n=2 what=add: "nova-board add --dir ./board --as <your-name> --text 'the thing you are about to file' --by 4h --default 'the filer files it as a known gap'"
QUICKSTART NOTE check EXITS 1 WHEN IT MATCHES, so the guard reads "if it is already there, stop"; the exit-2 arm tells a NO from a board that could not be read
QUICKSTART NOTE --stale 10m0s is this family's number and this run passed it in words: there is no default duration here, and --by and --default are required on every card

Left owed
- `nova-version` CLI subcommand is not installed; BUILD line above comes from `nova-board version` which reported `devel` (built from HEAD 5298f6be which matches). A reading against a different binary would prove nothing, but HEAD confirmed.
- Lines 2627-2643 cover `nova-swarm`, not `nova-board`; not exercised here.

git status --short:
(repo clean)
