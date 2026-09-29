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

## nova-work

No fixture: the graph file is created by the run itself under `--graph`, and every
line below is local — plain JSON nodes and `:deps` edges, no Redis, no remote, no
network. A `:deps` cycle is refused at exit 2 before anything is written.

The bounded reader for a `.work` plan. No fixture and no network: create the
exact local input before the transcript, then every line below reads local bytes
alone:

```sh
printf '%s\n' '(:plan :version 1 (:node :id "n1" :kind docs))' > ./work.work
```

This minimal plan demonstrates `plan check`; `plan expand` also requires each
node to declare `:output`.

`cmd/nova-work/firstrun_test.go` performs that setup and runs each `$` line
against it, so the `./work.work` below is a fresh file per run.

### First run

```text
$ nova-work dependencies --graph ./deps.json --node b
DEPENDENCIES OK nodes=1 edges=0

$ nova-work dependencies --graph ./deps.json --node a --needs b
DEPENDENCIES OK nodes=2 edges=1

$ nova-work ready --node a --graph ./deps.json
READY node=a ready=false blocker=b state=open resolver="nova-merge queue"

$ nova-work ready --node b --graph ./deps.json
READY node=b ready=true

$ nova-work plan check --file ./work.work
PLAN OK file=./work.work bytes=47 version=1 nodes=1 edges=0
```

The two verbs read one plan and one graph as data; a `:deps` cycle is refused at
exit 2 before anything is written.

### Refusals

```text
$ nova-work
WORK REFUSED: a verb is required; run: nova-work help

$ nova-work dependencies --graph ./deps.json --node b --needs a
nova-work dependencies: rule 3: :deps edges contain a cycle: b -> a -> b; run: nova-work help
```

Both exit 2 and print one line on stderr, and the two spellings are deliberate
rather than a drift: `WORK REFUSED:` is what the client spec gives an invocation
that could not run at all, and `nova-work <verb>:` is what a verb that ran and read
its input says about the input. `cmd/nova-work/firstrun_test.go` executes this
block as well as the one above; until 2026-09-19 it executed neither refusal, and
what that cost is written below.

### The event bridge

`nova-work events` publishes the pub/sub messages `nova-merge react` subscribes to
(docs/SPEC-JOBS.md, "Events, not ticks"). It makes no model call and writes no
record: the bus is a signal, git is the record.

It has no `$` line above because it cannot have one. The relay needs a local Redis
(`--redis <addr>`), and naming the repository with `--repo` switches ON a
`gh pr list` fallback that reaches the forge — without `--repo` only the stream is
bridged. `cmd/nova-work/events_log_test.go` drives it against a miniredis and a
fake forge, and the usage banner's `example:` block runs it without `--repo`. That
is where a line needing a running service belongs: every `$` line in this file is
one a stranger can type on a fresh bench.

This description used to head a SECOND `## nova-work` section further down this
file. Because `onboarding.Section` reads the first match of a name, no test ever
executed it, and it drifted into a refusal sentence the binary had stopped printing
(`nova-work: no verb given`) and an `events` line carrying `--repo`. Both
reproduced as DEFECT on space and on hulk in the 2026-09-18 two-bench run while
every test in this repository was green. `internal/ci/onboarding_test.go` now
refuses a repeated `## ` heading here.

## nova-decide

Fixture: a questions file and a state file of your own; the example below names
`./questions.json` and `./state.md`. The key is read only from the environment
(`JEV_API_KEY`, or `TYPESAFE_API_KEY`) and is never printed; a bare invocation,
a missing key and an unreadable questions file are each one line on stderr at
exit 2. Below the floor the answer is still one line, and the exit is 3 — a
suggestion, never an authorization.

**The keyed line is NOT in the block below, and that is deliberate.** Asking a
model is the one thing in this section that needs a key and a network, so a
test can only run it by reaching a model from `go test` — which this repository
does not do — or by skipping it, which is a step that never runs at all wearing
the clothes of one that does. What it prints, when you run it yourself with a
key in your environment, is `DECIDE gate=go conf=0.94 floor=0.90 below=-` from
`nova-decide --questions ./questions.json --state ./state.md --floor 0.9`. Every
line in the blocks below is executed by `cmd/nova-decide/firstrun_test.go`.

Two parts of the `version` line belong to the run and not to the document, and
the test declares both: the `<goos>/<goarch> go<version>` tail is whichever
machine runs it, and the version word is what the build stamped itself with --
the stamp shown here in a build you make, and `devel` in the unstamped binary
`go test` builds.

### First run

```
$ nova-decide
DECIDE REFUSED reason=no-arguments --questions is required, refusing to guess; run: nova-decide help

$ nova-decide version
nova-decide v0.16.0-dev.c839379e.0.20260919154525-3c3efc0e155c darwin/arm64 go1.27.1
```

`route`, `help` and `log` need no key and no network with `--no-jev`: the rules
alone answer, the same way every time. These lines were produced by running the
binary built on this branch. `log` reads a log of your own: the one below is
the single row the first `route` line writes when it is given `--log
./decide.jsonl`, and `cmd/nova-decide/firstrun_test.go` writes exactly that row
before it runs the block.

```
$ nova-decide route --unit-id card-41 --kind rebase --files 2 --packages 1 --no-jev
ROUTE unit=card-41 rung=flash confidence=0.90 floor=0.65 floor_from=built-in read=- wait=- next=- steps=1 reason="kind rebase starts at rung flash" ask=card ms=-

$ nova-decide route --unit-id card-9 --kind fleet-chore --files 1 --guard --no-jev
ROUTE unit=card-9 rung=flash confidence=0.90 floor=0.70 floor_from=kind read=johnny wait=- next=- steps=1 reason="security is a kind and not a height: guard, so a security READ by johnny is attached to this unit at any height, at any floor and after any attempt -- johnny is reserved for reads and for the STOP a read can call, and the WORK goes to the rung the evidence supports; kind fleet-chore starts at rung flash" ask=card ms=-

$ nova-decide route --unit-id s-1 --kind guard --files 1 --attempt johnny:timeout --no-jev
ROUTE unit=s-1 rung=johnny confidence=1.00 floor=0.65 floor_from=built-in read=johnny wait=awaiting_termination next=- steps=1 reason="security is a kind and not a height: kind guard, so a security READ by johnny is attached to this unit at any height, at any floor and after any attempt -- johnny is reserved for reads and for the STOP a read can call, and the WORK goes to the rung the evidence supports; the attempt on johnny timed out (timeout) and is not known to have terminated: its expiry is UNKNOWN, so this is a WAIT on the same rung and NOT permission to retry -- establish termination first" ask=bus ms=-

$ nova-decide help --hours 6 --asked-all-friends
HELP answer=ask-glenn reason="6.0 h on the same problem; landing has not moved in 6.0 h; the friends have been asked and it is still open"

$ nova-decide log --log ./decide.jsonl --summary
LOG kind=rebase decisions=1 escalations=0 successes=0 failures=0 start_rung=flash start_height=0 default_rung=flash regenerated=false floor=0.65 floor_from=built-in provider_rows=0 conf_min=- conf_max=- conf_p25=- below_floor=0 defeated=false hist=0.0-0.5:0,0.5-0.6:0,0.6-0.7:0,0.7-0.8:0,0.8-0.9:0,0.9-1.0:0 lat_n=0 median_ms=- p95_ms=-
LOG OK rows=1 kinds=1 escalations=0 defeated=0 coverage=0/1 lat_rules=0,-,-
```

## nova-review

### First run

`nova-review` builds an artifact from an existing nova-merge lane; it has no state-creating quickstart. Its first safe command only
identifies the binary.

Two parts of the line below belong to the run and not to the document, and both
are declared by `cmd/nova-review/firstrun_test.go`. The build triple
`<goos>/<goarch> go<version>` is whichever machine runs it -- the one pasted
here is the Mac it was recorded on (2026-09-19). The version word is whatever
the build stamped itself with: a build you make prints the stamp shown here,
and the unstamped binary `go test` builds prints `devel`. Everything else on
the line is compared.

```
$ nova-review version
nova-review v0.16.0-dev.c839379e.0.20260919144920-705dd1c92534 darwin/arm64 go1.27.1
```

A packet needs the lane, one selector, a reader and a new relative output
path. It refuses an existing output rather than replacing a packet, and it
refuses a supplied head that differs from the entry's current head. The packet
records the exact range and either includes its selected diff or says that the
byte budget omitted it with the command that prints it.

### mutate: the selected form, and the abstain

A lab with the #1828 shape: one fix in `sign/sign.go`, the test that detects it,
and a co-touched test in `shape/shape_test.go` that is correctly insensitive to
it. The default verdict is per FILE, so it says `FAIL` although the card's named
`TEST:` is red — which is what `--test` exists to answer (#1849).

```
$ nova-review mutate --repo . --base main --head card
MUTATE GREEN test=TestShapeOnly file=shape/shape_test.go: green with the change reverted; it proves nothing
MUTATE GREEN test=TestSignPositive file=sign/sign_test.go: green with the change reverted; it proves nothing
MUTATE 19947c2e reverted=1 red=1 green=2 FAIL

$ nova-review mutate --repo . --base main --head card --test TestSignZero
MUTATE 19947c2e reverted=1 red=1 green=2 test=TestSignZero PASS
```

The counts do not move: the co-touched units are still the evidence for how the
question was answered. What moves is which unit the verdict is about. A name that
cannot be resolved among the test units of the files this range changed is
refused, never answered for a different test:

```
$ nova-review mutate --repo . --base main --head card --test TestNoSuchThing
MUTATE REFUSED: the named test was not run: --test TestNoSuchThing names no test declared by a test file this range changed
```

A range that changes only test files — every `internal/docs` and `internal/ci`
doc-rule repair — abstains on stdout, exit 2. It is inability to prove the
control, never acceptance (#1850):

```
$ nova-review mutate --repo . --base card --head tests-only
MUTATE be009676 ABSTAIN reason=no-change-to-revert: every changed file is a test file, so there is no production hunk to revert and this control cannot be proved either way; choose the seed form's control or hold
```

## nova-merge

### First run

`nova-merge` keeps the evidence a stream lands on (`read`, `gate`, `classify`,
`batch`, `fold`); the lane-making verbs left with the per-PR lander role on
2026-09-24, so it has no state-creating quickstart. Its first safe command only
identifies the binary.

Three parts of the line below belong to the run and not to the document, and all
three are declared by `cmd/nova-merge/firstrun_test.go`: the build triple
`<goos>/<goarch> go<version>` is whichever machine runs it, the version word is
whatever the build stamped itself with (`devel` for the unstamped binary
`go test` builds), and `build=` is the sha256 of the binary's own file, twelve
hex, which differs for every build. Everything else on the line is compared.

```
$ nova-merge version
nova-merge devel darwin/arm64 go1.27.1 build=391dac11a7b7
```

## nova-wake

Fixture: `cmd/nova-wake/testdata/example-reports`.

### First run

```
$ nova-wake quickstart --state ./wake.state --reports ./reports
WAKE NOTE quickstart chose --baseline, --interval 5s and --max 5s, so a first run returns with the world listed once rather than blocking; --on-deadline report is the word it echoes back
WAKE at=2026-09-11T18:56:43Z as=- max=5s interval=5s on-deadline=report sources=reports state=./wake.state cold=false nova-bus=- pending=0
WAKE REPORT path=reports/first-job/RESULT.md lines=8 bytes=220 new
WAKE REPORT path=reports/second-job/RESULT.md lines=7 bytes=199 new
WAKE CHANGE after=0s polls=1 bus=0 entries=0 reports=2 lines=0 prs=0 runs=0 branches=0 locks=0 pending=0

$ nova-wake watch --state ./wake.state --max 5s --on-deadline report --interval 5s --reports ./reports
WAKE at=2026-09-11T18:56:43Z as=- max=5s interval=5s on-deadline=report sources=reports state=./wake.state cold=false nova-bus=- pending=0
WAKE QUIET after=5s polls=1 default=report sources-failing=0: deadline, default taken
```

## nova-sprint

Run by `cmd/nova-sprint/firstrun_test.go` in an empty directory, which must
still be empty afterwards: the wide table is read from Redis and written nowhere
(#3326), so none of these lines needs a server and none writes a file.

### First run

```text
$ nova-sprint table --once --fixture table.txt
! nova-sprint table: flag provided but not defined: -fixture; the wide table is read from Redis and written nowhere: --redis <addr> [--sprint <name>] (--once | --loop), or --check --redis <addr>; the whole sprint table is --layout live [--loop 1] [--out <file>]; run: nova-sprint help

$ nova-sprint table --once
! nova-sprint table: --redis <addr> is required; the wide table is read from Redis and written nowhere: --redis <addr> [--sprint <name>] (--once | --loop), or --check --redis <addr>; the whole sprint table is --layout live [--loop 1] [--out <file>]; run: nova-sprint help

$ nova-sprint table --check
! nova-sprint table: --check needs --redis <addr>, a throwaway server for the fixture keyspace; run: nova-sprint help
```

The first is the deleted file cut: `--fixture` and `--refresh` are unknown
flags, while `--out <file>` now publishes the wide table by atomic rename
(#3343) and `--layout live [--out <file>]` publishes the whole sprint table
(#3530). The second and third name the server the table is read
from. `TestTableWritesNoFile` renders from a throwaway server twice (a second
start) and checks the directory stays empty.

## nova-post

The gate is draft, show, approve, send: the tool prepares and renders, and Glenn's
bus receipt releases. A draft is rendered once, written under `--drafts` as
`<hash>.post` and `<hash>.meta`, and shown byte for byte; `send` refuses unless the
receipt exists in `--bus`, is Glenn's, names this hash and is under 24 hours old, and
it transmits the stored bytes only. The examples below name throwaway drafts, an
allowlist and a body of your own; a missing flag is one line on stderr at exit 2, and
a target the allowlist does not name is exit 1.
Fixture: a drafts directory you create, an allowlist with one
`channel<TAB>target` line, and a body file. Every test runs against fake
endpoints — an `httptest` server per channel, a fake mailer, an injected clock
— and the refusals below open no socket. A credential is read only from the
environment `nova-secrets exec` delivers, is never printed, and is never
measured.

The outward gate of [docs/SPEC-OUTBOUND.md](SPEC-OUTBOUND.md), on one of its four
channels: `ghost`. Every line below is local, and no socket, no credential and no
provider is touched: `draft` renders the payload and writes it under `./drafts`,
and `show` prints the exact payload bytes to stdout and its OK line to stderr
(the `! ` line). `send` releases only on a bus receipt from Glenn that names the
hash and is under 24 hours old, so it is not in this block: without a receipt it
refuses at exit 1. The fixture is exact, because the hash is: `./body.md` holds
the one line `A first post for the ghost channel.`, `./allowlist` holds the one
line `ghost<TAB>example.com`, and `./drafts` is an empty directory you create.
The hash is the SHA-256 of the payload the draft wrote, so
`shasum -a 256 ./drafts/<hash>.post` prints it back.
`cmd/nova-post/firstrun_test.go` writes that fixture and runs every `$` line.

### First run

```text
$ nova-post draft --channel ghost --target example.com --file ./body.md --drafts ./drafts --allowlist ./allowlist
POST DRAFT OK hash=b150f3e20ecedbf62ea231eca325d74de5dc1123d0e061c7453f524fb6c8315f channel=ghost target=example.com bytes=115 drafts=./drafts

$ nova-post show --draft b150f3e20ecedbf62ea231eca325d74de5dc1123d0e061c7453f524fb6c8315f --drafts ./drafts
{"posts":[{"title":"","html":"A first post for the ghost channel.\n","status":"published","tags":["example.com"]}]}
! POST SHOW OK hash=b150f3e20ecedbf62ea231eca325d74de5dc1123d0e061c7453f524fb6c8315f channel=ghost bytes=115 drafts=./drafts

$ nova-post version
nova-post devel linux/amd64 go1.26.5
```
