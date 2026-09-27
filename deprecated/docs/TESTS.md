# Deprecated tools: the first-run transcripts their tests execute

## nova-board

Fixture: `cmd/nova-board/testdata/example-board`.

### First run

```
$ nova-board quickstart --dir ./board --stale 10m
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

$ nova-board check --dir ./board --words windows
CHECK HIT id=5a64568ee513a2544d6eb17cb445d4fc state=OPEN owner=bo: the Windows runner skips three steps
CHECK OK matched=1 cards=4 scanned=OPEN words=1

$ nova-board list --dir ./board --stale 10m --list --max 2
BOARD CARD id=283e2dd1e5c5424d7637d28488365e98 state=OPEN owner=emma since=2026-09-10T11:00:00Z by=2026-09-11T09:00:00Z age=208h11m4s taken=- stale=true overdue=true conflicts=0 conflict=false quarantined=0 default=emma\x20writes\x20the\x20rows\x20by\x20hand\x20and\x20says\x20so thing=- leg=- evidence=-: the token ledger has no September rows yet
BOARD CARD id=5a64568ee513a2544d6eb17cb445d4fc state=OPEN owner=bo since=2026-09-10T11:00:00Z by=2126-01-01T00:00:00Z age=208h11m4s taken=208h11m4s stale=true overdue=false conflicts=0 conflict=false quarantined=0 default=rowan\x20files\x20it\x20on\x20the\x20schema\x20board\x20as\x20a\x20known\x20gap thing=- leg=- evidence=-: the Windows runner skips three steps
BOARD MORE kind=card shown=2 total=5 and 3 more; --max 0 shows all, or --owner <name> for one line's own batch
BOARD LINE name=emma open=1 overdue=1 stale=1
BOARD LINE name=bo open=1 overdue=0 stale=1
BOARD MORE kind=line shown=2 total=4 and 2 more; --max 0 shows all
BOARD LEG leg=cpp owed=1 probed=0
BOARD LEG leg=go owed=0 probed=1
BOARD NEXT the oldest OVERDUE card 283e2dd1e5c5424d7637d28488365e98, owed by emma, due 2026-09-11T09:00:00Z -- the token ledger has no September rows yet
BOARD OK cards=5 open=4 closed=1 stale=4 overdue=1 owed=1 lines=4 conflicts=0 quarantined=0 shown=10 backend=dir source=./board
```

The second command **exits 1**, and that is the point of it: `check` says NO when the board
already holds your words, so it can guard an `add` in one line of shell. Exit 0 from
`check` means nothing matched and filing is the right thing to do.
