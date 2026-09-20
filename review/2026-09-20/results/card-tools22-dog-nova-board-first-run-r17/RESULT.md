RESULT tools22-dog-nova-board-first-run-r17 sha=5298f6be12ea — read `nova-board First run` against docs/CLI.md:2586-2643 and say CLEAN or DRIFT
CLEAN

TOOL nova-board
VERB First run
DOC docs/CLI.md:2586-2643
REPLICA 17 of 24
BUILD installed `nova-version` could not be executed: the landlock sandbox denies reading /home/nova/.local (Permission denied), so STEP 1's `nova-version` printed no sha. To keep the reading honest I built `cmd/nova-board` and `cmd/nova-version` from the pinned tree (HEAD 5298f6be12eaa0f7e6622334d2b6a1eb427649e3) into <JOBDIR>/scratch with the release ldflags stamp, and the built binary answers `nova-version 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 linux/amd64 go1.26.6` — the same build the card pins. `git rev-parse HEAD` = 5298f6be12eaa0f7e6622334d2b6a1eb427649e3.

Fixture: the doc's prose names `cmd/nova-board/testdata/example-board` as the board the transcript runs against, so I copied it to <JOBDIR>/scratch/board before running the block.

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-board quickstart --dir ./board --stale 10m | 0 | CLEAN |

RAN 1
SKIPPED 0

(no DRIFT lines — nothing disagreed)

Transcript of block 1 (14 lines, exit 0):
QUICKSTART OK backend=dir source=./board stale=10m0s created=false: the board, then the rule every filer runs in front of add
BOARD LINE name=emma open=1 overdue=1 stale=1
BOARD LINE name=bo open=1 overdue=0 stale=1
BOARD LINE name=rowan open=1 overdue=0 stale=1
BOARD LINE name=freddy open=1 overdue=0 stale=1
BOARD LEG leg=cpp owed=1 probed=0
BOARD LEG leg=go owed=0 probed=1
BOARD NEXT the oldest OVERDUE card 283e2dd1e5c5424d7637d28488365e98, owed by emma, due 2026-09-11T09:00:00Z -- the token ledger has no September rows yet
BOARD OK cards=5 open=4 closed=1 stale=4 overdue=1 owed=1 lines=4 conflicts=0 quarantined=0 shown=8 backend=dir source=./board
QUICKSTART LINE n=1 what=check: "nova-board check --dir ./board --words 'the token ledger' || { [ $? -eq 1 ] && exit 0; exit 2; }"
QUICKSTART LINE n=2 what=add: "nova-board add --dir ./board --as <your-name> --text 'the token ledger has no September rows yet' --by 4h --default 'the filer files it as a known gap'"
QUICKSTART NOTE check EXITS 1 WHEN IT MATCHES, so the guard reads "if it is already there, stop"; the exit-2 arm tells a NO from a board that could not be read
QUICKSTART NOTE --stale 10m0s is this family's number and this run passed it in words: there is no default duration here, and --by and --default are required on every card

Every line matches docs/CLI.md:2593-2603 field-for-field (field names, order, spelling, values). Exit 0 matches the documented successful transcript. Timestamps/durations in the fixture are the fixture's own data, not machine-dependent — they agree.

Left owed
- STEP 1's installed `nova-version`/`nova-board` at /home/nova/.local/bin could not be exercised at all: the sandbox returns "Permission denied" on reading that tree (`backend=landlock`, /home/nova/.local not in the read allow-list). So whether the installed binary's own stamp differs from 5298f6be is not judgeable here; the reading was made against the exact pinned source instead.
- The prose after the block ("What a first run gets wrong": `--stale` missing refused, `--by`/`--default` required, "no backend, or both", list/check/take/close refusing a missing dir) is description with no fenced command blocks in docs/CLI.md:2586-2643, so those behaviours were not run as blocks and not judged.

git status --short at the end: (empty — nothing printed from the repository; no branch, no commit, no add made)