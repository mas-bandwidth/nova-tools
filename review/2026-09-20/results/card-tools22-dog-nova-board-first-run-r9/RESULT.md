RESULT tools22-dog-nova-board-first-run-r9 sha=5298f6be12ea — read `nova-board First run` against docs/CLI.md:2586-2643 and say CLEAN or DRIFT
CLEAN

TOOL nova-board
VERB First run
DOC docs/CLI.md:2586-2643
REPLICA 9 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

Build note: the installed binary at /home/glenn/.local/bin/nova-board (and
nova-version) is unreachable in this sandbox — reading it is EACCES
("Permission denied"; `file` reports "no read permission", the landlock wall
grants no read on /home/glenn/.local/bin), so `nova-version` as STEP 1 directs
could not be executed. `git rev-parse HEAD` printed the pinned base
5298f6be12eaa0f7e6622334d2b6a1eb427649e3, so I built nova-board and
nova-version from that exact pinned clone into scratch; the built nova-version
reports sha prefix 5298f6be12ea, i.e. the card's base build. The section was
exercised with that build.

The section contains exactly one fenced command block (docs/CLI.md:2598-2613),
run verbatim from <JOBDIR>/scratch against ./board populated from
cmd/nova-board/testdata/example-board, which the section itself names as "a
board the size of a first run".

| n | command | exit | verdict |
| --- | --- | --- | --- |
| 1 | nova-board quickstart --dir ./board --stale 10m | exit 0 | CLEAN |

No DRIFT lines.

RAN 1
SKIPPED 0

First 15 lines of output for each command block:

#1 (nova-board quickstart --dir ./board --stale 10m, exit 0):
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

Every printed line of block #1 matches the document line-for-line (field names,
order, spelling, values); exit 0 as the doc implies.

The section's surrounding prose claims (docs/CLI.md:2593-2596, 2615-2625) were
also probed and all held on this build:
- `list --dir ./missing --stale 10m` refuses at exit 2 and names the fix:
  "nova-board list: --dir wants a directory of .board card files and does not
  create one ... ./missing does not exist; make it first: mkdir -p './missing';
  run: nova-board help"
- `quickstart --dir ./fresh --stale 10m` makes the missing directory and prints
  created=true; `add` on a missing directory also makes it (ADD OK ... existed=false).
- `quickstart` without `--stale` refuses at exit 2: "--stale is required ... this
  family's number is 10m and there is no default duration; refusing to guess".
- `add` without `--by` and without `--default` each refuse at exit 2 ("--by is
  required ... a card with no deadline cannot be filed"; "--default is required").
- `quickstart` with no backend refuses at exit 2 ("no backend: --issue ... or
  --dir ...; refusing to guess").
- `check --dir ./board --words 'the token ledger'` exits 1 (matched, CHECK HIT);
  `check` with words present in no card exits 0 (CHECK OK matched=0).

Left owed: nothing unjudged in the section itself. The only deviation from the
card's procedure is that the INSTALLED binary could not be invoked (sandbox
EACCES on /home/glenn/.local/bin/*), so the reading was done with the same
build compiled from the pinned base clone instead; the built version line
confirms sha 5298f6be12ea == the card's base, so the reading is against the
pinned build. The --issue backend (GitHub `gh`) is mentioned only in the usage
table (docs/CLI.md:2539) which is outside 2586-2643; nothing in the First run
section requires it.

git status --short (run in repo): (empty — nothing printed)