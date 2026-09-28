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

## nova-play

Fixture: `story.txt`, three lines of prose holding the documented passage once,
written into `t.TempDir()` by `cmd/nova-play/firstrun_test.go`, which runs the
sitting below there. The notes live beside the source, in `story.txt.notes`;
every path is a flag, and nothing here leaves that directory.

This block is compared LINE BY LINE AND IN ORDER, which most of the transcripts
in this document are not: a run that prints one line fewer or one line more than
is written here is red. The ids are content addresses and reproduce exactly, so
they are compared as written; the `created=` instant is the one value belonging
to the run rather than to the document, and is matched as an instant.

### First run

```text
$ nova-play annotate --source story.txt --author Emma --passage "The lantern room held a brass fitting." --note "I wonder what alloy this is."
ANNOTATE OK id=f24beb35f0df author=Emma created=2026-09-19T06:29:53Z

$ nova-play read --source story.txt
READ OK source=story.txt notes=1
NOTE id=f24beb35f0df author=Emma created=2026-09-19T06:29:53Z
  PASSAGE The lantern room held a brass fitting.
  BODY I wonder what alloy this is.

$ nova-play reply --source story.txt --id f24beb35f0df --author Stella --body "Ship's brass, probably 70/30."
REPLY OK id=727fe2158637 author=Stella created=2026-09-19T06:29:53Z
```

## nova-friend

Run by `cmd/nova-friend/firstrun_test.go` on a throwaway redis-server holding
the nova_sprint function library and the roster `nova-config apply` writes
(rowan: 4 slots, frontier, coordinator; stella: 2 slots, reader; friend
revision 1), so it runs in the functional tier. The documented lines name no
`--redis`: on a bench the seat's address is the default (`NOVA_SPRINT_REDIS`,
then `NOVA_REDIS_ADDR`, then the seat's row), and the test appends the
throwaway server's. `--host` and `--session` are given on the line so every
value reproduces; nothing is normalised. The usage banner's `example:` block
is this same sitting, line for line.

### First run

```text
$ nova-friend here --as rowan --once --host studio --session s1
FRIEND HERE as=rowan host=studio session=s1 slots=4 taken=0

$ nova-friend list
FRIEND name=rowan state=up slots=4 tiers=frontier roles=coordinator host=studio working=0 rev=1
FRIEND name=stella state=down slots=2 tiers=- roles=reader host=- working=0 rev=1
FRIEND LIST friends=2 rev=1

$ nova-friend bye --as rowan
FRIEND BYE as=rowan was=up
```

`here --once` registers the session and ticks once: the beat is written
with no TTL, and the reader judges its age. `list` reads the store: rowan
is up (a beat under a minute old), stella has never beaten, and every line
ends with the friend revision `nova-config apply` stamped. `bye` deletes the
beat, so rowan reads down at once.

## nova-card

The card wrapper (#3059) is started by `nova-sprint card launch --stdin`, never
by hand, and its real run needs the sprint Redis and a dealt card; that run is
`TestWrapperOwnsOneCardEndToEnd` in `internal/nsprint/card/wrapper_functional_test.go`.
The first run a stranger can type reads nothing and writes nothing.

### First run

```text
$ nova-card version
nova-card v0.16.0-dev darwin/arm64 go1.26.6
```

## nova-test

The fixture is `cmd/nova-test/testdata/runs`: five runs at varied states, one
queued before the boundary. The clock comes from `--now` because a transcript
must read the same twice; a stranger's first run omits it and the real clock
answers. Plain files only: no runner, no network. `cmd/nova-test/firstrun_test.go`
runs the `$` line and compares every line printed.

### First run

```text
$ nova-test status --store cmd/nova-test/testdata/runs --since 2026-09-23T00:00:00Z --now 2026-09-23T10:30:00Z
STATUS RUN id=done state=completed queued=2026-09-23T10:00:00Z queue=30s drain=- exec=2m0s e2e=2m30s attempts=done.a1 prior_failures=-
STATUS RUN id=cancel state=cancelled queued=2026-09-23T10:05:00Z queue=5s drain=45s exec=1m40s e2e=1m45s attempts=cancel.a1 prior_failures=-
STATUS RUN id=retry state=completed queued=2026-09-23T10:10:00Z queue=20s drain=- exec=4m0s e2e=4m20s attempts=retry.a1,retry.a2 prior_failures=retry.a1:lint
STATUS RUN id=waiting state=queued queued=2026-09-23T10:20:00Z queue=10m0s+ drain=- exec=- e2e=10m0s+ attempts=- prior_failures=-
STATUS OK store=cmd/nova-test/testdata/runs since=2026-09-23T00:00:00Z now=2026-09-23T10:30:00Z runs=4 shown=4 older=1
```
