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

## nova-swarm

Fixture: a pool this tool makes in `t.TempDir()`, `cmd/nova-swarm/testdata/fakeharness`, a fake harness on `PATH` so the dispatcher is tested end to end with no provider, and the WALL every job runs inside: `nova-sandbox` itself, built from this repository into the same directory, with `cmd/nova-swarm/testdata/fakesandbox` beside it for the seam tests that must run on a platform whose sandbox body is not built.

Every contract test in `cmd/nova-swarm` runs its jobs **inside the real wall** on darwin (`--sandbox <the built binary>`) and takes this tool's one loud workaround (`--no-sandbox`, SPEC-SWARM; `nova-sandbox` has no such flag) where no body is built, which is the argv a reader sees in the test's own output.

Platform: recorded on a machine whose wall probe fails (containment broken) — the `RUN REFUSED` and `RUN UNSANDBOXED` runs below are that machine's, and it is the last machine a friend should be reading a quickstart on; a bench whose wall proves itself (Linux landlock, say) prints `RUN POOL`, `RUN OK` and `RUN NOTE` instead.

### The wall at the launch seam

```
$ nova-swarm run --pool ./pool --workers 1 --hours 0.25 --worker ./worker.json
RUN POOL workers=1 hours=0.25 worker=fake-1 model=fake-model auto_retry=true pool=./pool
RUN REFUSED reason=sandbox_probe: the wall did not prove itself on this machine, so no worker started: PROBE REFUSED reason=check: write_outside expected deny and got allow

$ nova-swarm run --pool ./pool --workers 1 --hours 0.25 --worker ./worker.json --no-sandbox
RUN POOL workers=1 hours=0.25 worker=fake-1 model=fake-model auto_retry=true pool=./pool
RUN UNSANDBOXED id=20260912T0146Z-task-c44c5e slot=1: no OS containment; every read and write this job makes is yours
RUN START id=20260912T0146Z-task-c44c5e slot=1 pid=31027 pgid=31027 started=2026-09-12T01:46:02Z deadline=30s tokens=100000 job=./worker-home-1/jobs/20260912T0146Z-task-c44c5e
```

Inside the wall, on darwin, the job's own words from `harness.log` — a write outside the job directory and a read of the key file, both denied by the kernel, with the key's VALUE arriving in the environment all the same:

```
fake harness: the key is present, length 28
fake harness: touch /…/outside-every-list: open /…/outside-every-list: operation not permitted
fake harness: cat /…/key: open /…/key: operation not permitted
```


### The budget word on the native route

Every `nova-swarm native` launch carries `--tokens <n>` or `--tokens unmetered`
(SPEC-SWARM rule 13d, issue #1545). Recorded against the same fake harness, with the paths
abridged:

```
$ nova-swarm native --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
nova-swarm native: --tokens is required; it wants a token budget for this job, or the word `unmetered` when this provider has no live accounting and the deadline is the only stop; refusing to guess

$ nova-swarm native --tokens 0 --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
nova-swarm native: --tokens is a budget and is at least 1, got 0; `unmetered` is how a caller says there is no accounting

$ nova-swarm native --tokens unmetered --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
! NATIVE NOTE: no harness store: looked at ./root/slot-1/data/opencode/opencode.db and ./root/slot-1/data/.local/share/opencode/opencode.db
NATIVE OK label=card job=./root/slot-1/jobs/card tmp=./root/slot-1/tmp/card rc=0 wall=0.18s sandbox=none-by-flag card_sha256=ab6468b200da0b3d5a0175e863d1b1cf772f010abdf4fe70b3ea39926f3fc826 binary_sha256=13c788f4813d7d81152460f6a16d45123314342ce0269f3fb43e6c8438192852 config=68719609 harness=ok budget=unmetered usage=none reason=no-store path=./root/slot-1/data/opencode/opencode.db
```

Both refusals exit 2 and make no directory: `<slot>/jobs`, `<slot>/data` and `<slot>/tmp`
do not exist afterwards. `budget=` follows `harness=` on every `NATIVE OK` line.

### A budget nothing can observe, refused before anything is made

A budget wants a source this tool can read (rule 13d). The source is the worker
description's `usage`, and `opencode` — read with `sqlite3` — when there is no `--worker`:

```
$ nova-swarm native --tokens 100000 --worker ./usage-none.json --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
! NATIVE REFUSED: a numeric --tokens wants a usage source this tool can read, and the worker description says `usage: none`, which reports nothing; a budget nothing can observe is a promise the tool cannot keep, so this launch is refused rather than run under a cap that would never fire. Give the description `usage: opencode`, or launch with --tokens unmetered and no max_turns or max_cache_read

$ PATH=./empty nova-swarm native --tokens 100000 --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
! NATIVE REFUSED: a numeric --tokens is read from the harness's own database with `sqlite3 -readonly`, and sqlite3 is on no PATH entry of this bench; a budget nothing can observe is a promise the tool cannot keep, so this launch is refused rather than run under a cap that would never fire. Install sqlite3 on this bench, or launch with --tokens unmetered and no max_turns or max_cache_read
```

The card's own budget is read from the same source, so it meets the same refusal whatever
`--tokens` says — `unmetered` included:

```
$ nova-swarm native --tokens unmetered --worker ./usage-none-max-turns.json --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
! NATIVE REFUSED: this worker description's max_turns wants a usage source this tool can read, and the worker description says `usage: none`, which reports nothing; a budget nothing can observe is a promise the tool cannot keep, so this launch is refused rather than run under a cap that would never fire. Give the description `usage: opencode`, or launch with --tokens unmetered and no max_turns or max_cache_read
```

`--tokens unmetered` with no such description runs under both conditions, as it does today.
After every refusal above, `<slot>` is empty: nothing was made.

### What the line reports against the number

The fake harness writes a **real sqlite database** in the harness's own shape
(`FAKE-USAGE-DB`, the five token counts in rule 12's order then `usd`, with `-` for a type
the provider did not report). Under `--tokens 50000`, the `NATIVE OK` line's `budget=`:

```
# a harness that reported nothing
harness=ok budget=-/50000
# only tokens_in
harness=ok budget=900+/50000
# every column a reported zero
harness=ok budget=0/50000
# tokens_in 100, tokens_out 50, cache_write 9000, cache_read 90000, reasoning 7
harness=ok budget=157/50000
```

The last is the whole of the sum rule: `tokens_in + tokens_out + reasoning` is 157, and the
99,000 of cache stands in the usage row and never in the budget. A reported `0` is a
measurement and prints `0/50000` — never `unmetered`. `--tokens unmetered` prints the word
whatever the harness reported.

### The stop

A card that publishes a report, spends past `--tokens 100000` and then declines the
terminate, under `--deadline 120s` so that the budget is what ends it:

```
$ nova-swarm native --tokens 100000 --usage-interval 1s … --deadline 120s
NATIVE OK label=card job=./root/slot-1/jobs/card tmp=./root/slot-1/tmp/card rc=-1 wall=5.05s sandbox=none-by-flag card_sha256=8e1f… binary_sha256=ad88… config=ffdf555f harness=ok budget=100000/100000 stopped=tokens
```

Exit 1. The launch's own row carries `end=budget` and a dash for `rc`, while the line prints
`rc=-1`:

```
$ cut -f1-8 ./root/slot-1/jobs/card/usage.tsv
job	attempt	started	ended	end	rc	provider	model
card	1	2026-09-19T13:16:05Z	2026-09-19T13:16:10Z	budget	-	fake	fake-model
```

And what the card published is kept byte for byte — the tool writes nothing into it:

```
$ grep -c PROMPT-DEFECT ./root/slot-1/jobs/card/RESULT.md
0
$ grep "findings:" ./root/slot-1/jobs/card/RESULT.md
findings: 2
```

### The sample interval's floor and ceiling

```
$ nova-swarm native --tokens unmetered --usage-interval 900ms … --deadline 30s
! nova-swarm native: --usage-interval is at least 1s, got 900ms; three failed reads in a row end a card budget-unverifiable, and under a second that is a moment's bad luck rather than a source that has stopped answering

$ nova-swarm native --tokens unmetered --usage-interval 30s … --deadline 30s
! nova-swarm native: --usage-interval is shorter than --deadline, got 30s against a deadline of 30s; at or past the deadline no sample would ever run and the budget could not fire
```

`1s` exactly is accepted — the floor is inclusive — and the ceiling is exclusive.

### First run

```
$ nova-swarm quickstart --pool ./pool
QUICKSTART OK pool=./pool pending=0 next=add,run,triage
QUICKSTART NOTE a task is a file: nova-swarm add --pool ./pool --task <file> --files <n> --tokens <n>
QUICKSTART NOTE a worker description says whose model runs: nova-swarm run --pool ./pool --workers <n> --hours <h> --worker <file>
QUICKSTART NOTE the conditions are worth more than the model: nova-swarm template --name read-pr

$ nova-swarm status --pool ./pool --max 20
STATUS OK pending=0 running=0 done=0 failed=0 slots=0/0 quarantined=0
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
