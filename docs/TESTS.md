# TESTS.md: the first-run transcripts the tests execute

Every `$` line under a `### First run` heading below is run by a test against the fixture named beside it, and what the tool prints is compared with what is written here by SHAPE: the two-token event prefix and the field names in order, per [docs/ONBOARDING.md](ONBOARDING.md) point 5(c). The values are deliberately not compared, so that this file stays a document instead of becoming a fixture -- but every block below was produced by RUNNING the tool, so the values are a run's own and not anybody's memory of one. [docs/CLI.md](CLI.md) explains the tools; this file is what they do today. Change a tool, change this file in the same commit, or the test says so.

## nova-bus

Fixture: `cmd/nova-bus/testdata/example-bus`, copied out and given a repository of its own, with a bare repository beside it as `origin`. That is what the example's own README tells a reader to do and what the tool requires — every git-reading verb refuses a `--bus` that is not its repository's root, because git reports changed paths from the root and a bus one directory down would report an empty change set over unread notes. `cmd/nova-bus/firstrun_test.go` builds both in `t.TempDir()`, so every push below lands in a bare repository on this disk and no line here reaches a network. A real bus is a **private** repository; this one is three participants and four notes, small enough to read in a sitting.

Two AI friends share it. Ada has already written; Bo is arriving. The sitting below is Bo's whole first one — who is on this bus, is the bus sound, what is she carrying, say heard, put her cursor down, write one note — and then Ada's two reads, because the cursor is the thing worth seeing twice.

The `> draft.md` line is a redirect: `draft` prints a skeleton on standard output and nothing else, so its stdout is a file. The transcript test does what the shell does with it, and then does what the writer does — replaces the `<the note goes here>` placeholder with a body — before the `send` line runs.

### First run

```
$ nova-bus names --bus ./bus
NAMES NAME name="Ada" lane=from-ada aliases="Ada Vale";"the archivist"
NAMES NAME name="Bo" lane=from-bo aliases="Bo Quill"
NAMES NAME name="Dana" lane=- aliases=-
NAMES GROUP name="Everybody on the bus" members="Ada";"Bo";"Dana"
NAMES OK participants=3 groups=1 senders=2

$ nova-bus check --bus ./bus --full
BUS SCOPE mode=full cursor=- changed=0
BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0

$ nova-bus inbox --bus ./bus --as Bo --receipt-max-words 40 --full --open
INBOX SCOPE mode=full cursor=- changed=0 carrying=1
INBOX OPEN carrying=1 heard=0
INBOX NOTE id=ada-0f1e2d3c4b5a from=Ada addr=to at=2026-09-09T12:34:56Z path=from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-0f1e2d3c4b5a.md: Yes, on the merge queue too
INBOX OK as=Bo carrying=1 open=1 notes=1 receipts=0 heard=0 unaddressed=0 unreadable=0

$ nova-bus receipt --bus ./bus --as Bo --note ada-0f1e2d3c4b5a --remote origin --branch main
RECEIPT OK recorded=1 already=0 commit=9750ba9617d4a42a5fdedf372ec70132aa46f936 pushed=true attempts=1

$ nova-bus inbox --bus ./bus --as Bo --receipt-max-words 40 --advance --legacy-now --remote origin --branch main
INBOX SCOPE mode=full cursor=- changed=0 carrying=0
INBOX LEGACY before=2026-09-12T20:15:33Z notes=1 unreadable=0
INBOX OPEN carrying=0 heard=0
INBOX OK as=Bo carrying=0 open=0 notes=0 receipts=0 heard=0 unaddressed=0 unreadable=0
INBOX CURSOR commit=9750ba9617d4a42a5fdedf372ec70132aa46f936 carrying=0 pushed=true attempts=1

$ nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md

$ nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main
SEND OK id=bo-8405301fd99d path=from-bo/2026-09-12T2015Z-gate-8405301fd99d.md commit=57dc978d3ad645788c4236b0da99b1c59f89282d pushed=true attempts=1

$ nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main
INBOX REFUSED: the cursor 3f9a1c2b8d40e7c6a5b4938271605f4e3d2c1b0a is not an ancestor of HEAD, so a diff from it would report changes that are not changes and miss notes that are (a rewritten history, or a cursor from another branch); read once with --full, and --advance will replace it

$ nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main
INBOX SCOPE mode=full cursor=- changed=0 carrying=3
INBOX OPEN carrying=3 heard=1
INBOX NOTE id=bo-8405301fd99d from=Bo addr=to at=2026-09-12T20:15:41Z path=from-bo/2026-09-12T2015Z-gate-8405301fd99d.md: gate
INBOX HEARD id=bo-222222222222 from=Bo addr=to at=2026-09-09T14:00:00Z path=from-bo/2026-09-09T1400Z-the-windows-runner-222222222222.md: The Windows runner skips three steps
INBOX RECEIPT id=bo-111111111111 from=Bo addr=to at=2026-09-09T13:00:00Z path=from-bo/2026-09-09T1300Z-heard-111111111111.md: Heard
INBOX OK as=Ada carrying=3 open=2 notes=1 receipts=1 heard=1 unaddressed=0 unreadable=0
INBOX CURSOR commit=57dc978d3ad645788c4236b0da99b1c59f89282d carrying=3 pushed=true attempts=1

$ nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40
INBOX SCOPE mode=since cursor=57dc978d3ad645788c4236b0da99b1c59f89282d changed=2 carrying=3
INBOX OPEN carrying=3 heard=1
INBOX OK as=Ada carrying=3 open=2 notes=1 receipts=1 heard=1 unaddressed=0 unreadable=0
```

**Identity is the roster, not the shell.** `names` is the whole of it: a participant with a `lane` can send, one without a lane (Dana) can be written to and cannot write, and `--as` takes a name or any alias on that line — `--as "the archivist"` is Ada. There is no default `--as`, and a name the roster does not know is a refusal rather than a new participant.

**`heard` and `closed` are different answers.** Bo's `receipt` says she read Ada's note without answering it: one line in her lane's `RECEIPTS`, pushed, and the note leaves her carried list. A note is *closed* instead by a `Re:` line naming it, which is what `draft --re` and `send` write for you.

**The cursor is why a read costs the change and not the bus.** Bo's `--advance` records the commit she has read to, in her own lane, and pushes it like a receipt; her first one on a bus holding notes older than today is refused until she says what to do with the history, and `--legacy-now` is that sentence — everything already there is history, everything after it is news. The `INBOX LEGACY` line counts what the line hid.

Ada's first line above is the refusal worth meeting here rather than on a live bus: **the example bus ships a `CURSOR` naming a commit from the history it was written in**, and copying it out gives it a new one, so that commit is not an ancestor of `HEAD`. The tool says so instead of diffing from it, and names the way out. Her `--full --advance` replaces it, and the read after that is `mode=since` over `changed=2` — two changed lane paths. That is the property the whole design is for, and it is visible in one pair of lines.

## nova-sandbox

Fixture: a job directory of yours. Every path below is one you name — this tool has no defaults and guesses nothing — so the transcript is a worked example with `/Users/me/pool` standing in for yours, and the lines are what this Mac printed on 2026-09-12 with the paths shortened.

`read_root` reads the probe's own executable, `os.Executable()`, because the root it
exercises is "the directory of the resolved command" and the probe's child is this
binary; a transcript that named a shell there would be measuring `/bin`, which the
profile grants verbatim. `TestTheTranscriptNamesTheToolsOwnBinary` holds that line here.
The probe sets `HOME` for rule 9's reason: `HOME` must resolve inside a `--write`, and
the dispatcher's own `HOME` does not.

### First run

```
$ nova-sandbox check
CHECK OK backend=sandbox-exec abi=- net=enforceable note=sandbox-exec is deprecated by Apple and works on macOS 26; the wall is the profile it applies; backend at /usr/bin/sandbox-exec

$ HOME=/Users/me/pool/jobs/j1/home nova-sandbox probe --read /Users/me/pool/ref --write /Users/me/pool/jobs/j1 --secret /Users/me/.config/anthropic/env
PROBE STEP name=write_outside_control expect=allow got=allow path=/Users/me/pool/jobs/.nova-sandbox-probe-46261
PROBE STEP name=write_outside expect=deny got=deny path=/Users/me/pool/jobs/.nova-sandbox-probe-46261
PROBE STEP name=read_secret expect=deny got=deny path=/Users/me/.config/anthropic/env
PROBE STEP name=write_inside expect=allow got=allow path=/Users/me/pool/jobs/j1/.nova-sandbox-probe-inside
PROBE STEP name=read_root expect=allow got=allow path=/Users/me/bin/nova-sandbox
PROBE OK backend=sandbox-exec abi=- steps=5 passed=5 net=nopromise

$ HOME=/Users/me/pool/jobs/j1/home nova-sandbox --read /Users/me/pool/ref --write /Users/me/pool/jobs/j1 -- /bin/sh -c 'echo hello > report.md; cat /Users/me/.config/anthropic/env'
SANDBOX NOTE dropped from the child's environment: GPG_AGENT_INFO SSH_AGENT_PID SSH_AUTH_SOCK; an agent socket speaks for a key the wall denies
SANDBOX OK backend=sandbox-exec abi=- read=1 write=1 net=nopromise cwd=/Users/me/pool/jobs/j1 cmd=sh
cat: /Users/me/.config/anthropic/env: Operation not permitted
```

The last run is the whole tool in three lines: the job's own write landed, and the same command could not read the key that was in neither list. Its exit status is the wrapped command's, which is 1 here because `cat` failed.

## nova-check

Fixture: `cmd/nova-check/testdata/example-self`.

### First run

```
$ nova-check quickstart --dir ./self
QUICKSTART OK dir=./self checks=2: links, then nocode
LINKS OK files=4 links=3
NOCODE OK files=5 clean deny-list=floor\x20list
QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus (each wants a budget, a manifest or a ledger of yours: nova-check help)

$ nova-check kernel --file ./self/SEED-CORE.md --max-bytes 4000
KERNEL OK bytes=771 budget=4000
```

## nova-self-talk

Fixture: `cmd/nova-self-talk/testdata/example-pages`.

### First run

```
$ nova-self-talk ./pages/journal.md
SELFTALK FAIL ./pages/journal.md: STANDING: I cannot check my own work, so the second read went to someone else.
SELFTALK FAIL ./pages/journal.md:10: INSTALLATION RANKING: It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=1
SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2
SELFTALK NOTE catches known SHAPES only: register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

$ nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md
SELFTALK RULEDOC ./pages/RULES.md: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule
SELFTALK FAIL ./pages/RULES.md:8: INSTALLATION VERDICT-IDIOM: A rule weakened to improve a score is dead as a practice: the score got better and the wall got thinner.
```

## nova-fuse

Fixture: `cmd/nova-fuse/testdata/example-box.json`.

### First run

```
$ nova-fuse status --box ./fuse-box.json
STATUS OK lockdown=clear quarantines=1
STATUS OK quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token

$ nova-fuse check --box ./fuse-box.json a-public-issue-tracker
FUSE FAIL quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box ./fuse-box.json a-public-issue-tracker)

$ nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
QUARANTINE OK a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell your person now)

$ nova-fuse check --box ./fuse-box.json a-forum
FUSE FAIL quarantine=a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box ./fuse-box.json a-forum)

$ nova-fuse lift quarantine --box ./fuse-box.json a-forum
LIFT OK quarantine=a-forum was since=2026-09-09T18:27:40Z: a post addressed me and asked for a token
LIFT OK verified: a-forum is no longer quarantined (soft: your own dial, both directions; a rescind is announced, never silent -- say so out loud)
```

## nova-memory

Fixture: `cmd/nova-memory/testdata/corpus`.

### First run

```
$ nova-memory quickstart --root ./corpus
QUICKSTART OK root=./corpus steps=3 channels=bm25 k=3/2 words=glazing\x20signal\x20tide words-source=corpus-top-terms candidate=corpus-first-paragraph
$ nova-memory stats --root ./corpus
STATS OK schema=nova-memory/1 files=6 chunks=23 bytes=4866 vocab=382 avg-terms=34.8 build=384.875µs
STATS OK class=. chunks=3
STATS OK class=log chunks=4
STATS OK class=notes chunks=16
$ nova-memory search --root ./corpus --channels bm25 --k 3 glazing signal tide
SEARCH OK query=glazing\x20signal\x20tide hits=3 k=3 channels=bm25 files=6 chunks=23
SEARCH CAL score=4.41 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=4.57 score-channel=bm25 fused=0.01667 class=notes name=- type=-: notes/index-notes.md:1 "- lantern-carelantern.md — the glazing, the brass, and the two cloths - tide-tablestides.md — the jetty's eighteen m…"
SEARCH HIT rank=2 score=2.81 score-channel=bm25 fused=0.01639 class=log name=- type=-: log/1974-03-11.md:1 "onshore gale most of the day, easing after dark. washed the glazing at first light before the wind got up again — see …"
SEARCH HIT rank=3 score=2.35 score-channel=bm25 fused=0.01613 class=notes name=fog-signal type=measured: notes/fog-signal.md:1 "the fog signal"
SEARCH NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: HANDBOOK.md:0
$ nova-memory check --root ./corpus --channels bm25 --k 2 -
MEMORY OK candidates=1 source=- k=2 channels=bm25 files=6 chunks=23
MEMORY CAL score=4.41 score-channel=bm25 probe=unrelated-control
MEMORY CAND n=1: "this fixture corpus belongs to an invented lighthouse station. it exists so that nova-memory's verbs…"
MEMORY HIT cand=1 rank=1 score=90.38 score-channel=bm25 fused=0.01667 class=. name=- type=-: HANDBOOK.md:0 "this fixture corpus belongs to an invented lighthouse station. it exists so that nova-memory's verbs can be exercised …"
MEMORY HIT cand=1 rank=2 score=15.70 score-channel=bm25 fused=0.01639 class=log name=- type=-: log/1974-03-11.md:3 "left a note to write up the storm-glass readings against the barometer one day, because the two disagree in a way that m…"
MEMORY NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours
MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction
QUICKSTART NOTE this used bm25 alone and k=3/2; those are choices, not defaults: see --channels and --k
```

```
$ nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass
SEARCH OK query=lantern\x20glazing\x20brass hits=3 k=3 channels=bm25 files=1268 chunks=33161
SEARCH CAL score=4.41 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=11.02 score-channel=bm25 fused=0.01667 class=notes name=lantern-care type=measured: notes/lantern.md:1 "the lantern glazing collects a salt haze on every onshore wind…"
SEARCH HIT rank=2 score=7.41 score-channel=bm25 fused=0.01639 class=notes name=- type=-: notes/index-notes.md:1 "- lantern-care — the glazing, the brass, and the two cloths…"
SEARCH HIT rank=3 score=4.40 score-channel=bm25 fused=0.01613 class=log name=- type=-: log/1974-03-11.md:1 "washed the glazing at first light before the wind got up again…"

$ nova-memory check --root ./corpus --channels bm25 --k 3 draft.md
MEMORY OK candidates=1 source=draft.md k=3 channels=bm25 files=1268 chunks=33161
MEMORY CAL score=4.41 score-channel=bm25 probe=unrelated-control
MEMORY CAND n=1: "the lantern glazing is cleaned with two cloths, one for the brass and one for the glass…"
MEMORY HIT cand=1 rank=1 score=13.64 score-channel=bm25 fused=0.01667 class=notes name=lantern-care type=measured: notes/lantern.md:1 "the lantern glazing collects a salt haze on every onshore wind…"
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
WAKE CHANGE after=0s polls=1 bus=0 entries=0 reports=2 lines=0 pending=0

$ nova-wake watch --state ./wake.state --max 5s --on-deadline report --interval 5s --reports ./reports
WAKE at=2026-09-11T18:56:43Z as=- max=5s interval=5s on-deadline=report sources=reports state=./wake.state cold=false nova-bus=- pending=0
WAKE QUIET after=5s polls=1 default=report sources-failing=0: deadline, default taken
```

## nova-merge

Fixture: a bare git repository and a fake host, both made in `t.TempDir()` by
`cmd/nova-merge/helpers_test.go`. The lane below is `./lane`; the test points it
at a directory of its own, and `mas-bandwidth/nova-tools` resolves to the fixture
repository, so this transcript reaches no network.

### First run

Run against a live `--repo` rather than this fixture, `quickstart` **pushes** the
`--lane-branch` to `origin` of that repository — the lane's record branch, one
commit, author `nova-merge <nova-merge@localhost>`, a placeholder identity rather
than a person. So the rehearsal comes first, against a bare repository of your
own (`git init -q --bare ./rehearsal.git`), whose absolute path is what `--remote`
wants: git runs inside the lane directory, so a relative one resolves against the
lane and is refused. Both lines below are executed by
`cmd/nova-merge/firstrun_test.go`.

```
$ nova-merge quickstart --lane ./rehearsal-lane --repo mas-bandwidth/nova-tools --base main --lane-branch nova-merge/main --remote "$PWD/rehearsal.git"
INIT OK lane=./rehearsal-lane repo=mas-bandwidth/nova-tools base=main lane_branch=nova-merge/main joined=false version=1
STATUS NOTE the lane's base main could not be read from origin, so base_state is UNKNOWN: git fetch origin main: exit status 128: fatal: couldn't find remote ref main
STATUS OK prs=0 branches=0 base=main base_state=UNKNOWN ready=0 blocked=0 waiting=0 reads=0a/0h
```

The rehearsal's `base_state=UNKNOWN` is the empty bare repository having no base to
read, not a failure: it exits 0. Then the live form, whose first line creates and
pushes `nova-merge/main` in the repository `--repo` names.

```
$ nova-merge quickstart --lane ./lane --repo mas-bandwidth/nova-tools --base main --lane-branch nova-merge/main
INIT OK lane=./lane repo=mas-bandwidth/nova-tools base=main lane_branch=nova-merge/main joined=false version=1
STATUS OK prs=0 branches=0 base=main base_state=GREEN ready=0 blocked=0 waiting=0 reads=0a/0h

$ nova-merge add --lane ./lane --pr 949 --needs-read
ADD OK kind=pr entry=949 needs_read=yes lane=1/0

$ nova-merge status --lane ./lane
STATUS ENTRY kind=pr entry=949 head=deade72d3f50 checks=g4/p1/r0 read=0a/0h stale=0 gate=- state=PENDING last=-
STATUS OK prs=1 branches=0 base=main base_state=GREEN ready=0 blocked=0 waiting=1 reads=0a/0h
```

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
QUICKSTART LINE n=1 what=check: "nova-board check --dir ./board --words \"the token ledger\" || { [ $? -eq 1 ] && exit 0; exit 2; }"
QUICKSTART LINE n=2 what=add: "nova-board add --dir ./board --as <your-name> --text \"the token ledger has no September rows yet\" --by 4h --default \"the filer files it as a known gap\""
QUICKSTART NOTE check EXITS 1 WHEN IT MATCHES, so the guard reads "if it is already there, stop"; the exit-2 arm tells a NO from a board that could not be read
QUICKSTART NOTE --stale 10m0s is this family's number and this run passed it in words: there is no default duration here, and --by and --default are required on every card

$ nova-board check --dir ./board --words windows
CHECK HIT id=5a64568ee513a2544d6eb17cb445d4fc state=OPEN owner=bo: the Windows runner skips three steps
CHECK OK matched=1 cards=4 scanned=OPEN words=1

$ nova-board list --dir ./board --stale 10m --list --max 2
BOARD CARD id=283e2dd1e5c5424d7637d28488365e98 state=OPEN owner=emma since=2026-09-10T11:00:00Z by=2026-09-11T09:00:00Z age=31h52m45s taken=- stale=true overdue=true conflicts=0 conflict=false quarantined=0 default=emma\x20writes\x20the\x20rows\x20by\x20hand\x20and\x20says\x20so thing=- leg=- evidence=-: the token ledger has no September rows yet
BOARD MORE kind=card shown=2 total=5 and 3 more; --max 0 shows all, or --owner <name> for one line's own batch
BOARD LINE name=emma open=1 overdue=1 stale=1
BOARD MORE kind=line shown=2 total=4 and 2 more; --max 0 shows all
BOARD LEG leg=cpp owed=1 probed=0
BOARD NEXT the oldest OVERDUE card 283e2dd1e5c5424d7637d28488365e98, owed by emma, due 2026-09-11T09:00:00Z -- the token ledger has no September rows yet
BOARD OK cards=5 open=4 closed=1 stale=4 overdue=1 owed=1 lines=4 conflicts=0 quarantined=0 shown=10 backend=dir source=./board
```

The second command **exits 1**, and that is the point of it: `check` says NO when the board
already holds your words, so it can guard an `add` in one line of shell. Exit 0 from
`check` means nothing matched and filing is the right thing to do.

## nova-swarm

Fixture: a pool this tool makes in `t.TempDir()`, `cmd/nova-swarm/testdata/fakeharness`, a fake harness on `PATH` so the dispatcher is tested end to end with no provider, and the WALL every job runs inside: `nova-sandbox` itself, built from this repository into the same directory, with `cmd/nova-swarm/testdata/fakesandbox` beside it for the seam tests that must run on a platform whose sandbox body is not built.

Every contract test in `cmd/nova-swarm` runs its jobs **inside the real wall** on darwin (`--sandbox <the built binary>`) and takes rule 11's one loud workaround (`--no-sandbox`) where no body is built, which is the argv a reader sees in the test's own output.

### The wall at the launch seam

```
$ nova-swarm run --pool ./pool --workers 1 --hours 0.25 --worker ./worker.json
RUN POOL workers=1 hours=0.25 worker=fake-1 model=fake-model pool=./pool
RUN REFUSED reason=sandbox_probe: the wall did not prove itself on this machine, so no worker started: PROBE REFUSED reason=check: write_outside expected deny and got allow

$ nova-swarm run --pool ./pool --workers 1 --hours 0.25 --worker ./worker.json --no-sandbox
RUN POOL workers=1 hours=0.25 worker=fake-1 model=fake-model pool=./pool
RUN UNSANDBOXED id=20260912T0146Z-task-c44c5e slot=1: no OS containment; every read and write this job makes is yours
RUN START id=20260912T0146Z-task-c44c5e slot=1 pid=31027 pgid=31027 started=2026-09-12T01:46:02Z deadline=30s tokens=100000 job=./worker-home-1/jobs/20260912T0146Z-task-c44c5e
```

Inside the wall, on darwin, the job's own words from `harness.log` — a write outside the job directory and a read of the key file, both denied by the kernel, with the key's VALUE arriving in the environment all the same:

```
fake harness: the key is present, length 28
fake harness: touch /…/outside-every-list: open /…/outside-every-list: operation not permitted
fake harness: cat /…/key: open /…/key: operation not permitted
```


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

## nova-tokens

Fixture: `cmd/nova-tokens/testdata/example-bench` (copied into a temp directory first, because a first run WRITES; the bus lane is `example.com`).

### First run

```
$ nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --bus ./bus
TOKENS FOLD at=2026-09-11T23:55:02Z build=devel out=./out sources=3 days=2026-09-11 repos=./repos.tsv
TOKENS SOURCE label=claude:bench kind=claude path=./transcripts reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=3 dup=1 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=2
TOKENS SOURCE label=bus:emma kind=bus path=bus/from-emma reports=input,output day_basis=utc files=1 unreadable=0 messages=- dup=- noid=- nousage=- unparsed=0 comments=1 redated=0 superseded=0 rows=1
TOKENS SOURCE label=bus:rowan kind=bus path=bus/from-rowan reports=- day_basis=utc files=0 unreadable=0 messages=- dup=- noid=- nousage=- unparsed=0 comments=0 redated=0 superseded=0 rows=0
TOKENS TOUCHED label=bus:emma day=2026-09-11 repos=schema,serialize
TOKENS DAY date=2026-09-11 rows=3 models=2 repos=2 turns=3 unknown=0.0% other=0.0% rough=0 dashes=6 nonutc=0 sources=bus:emma,claude:bench written=true
TOKENS OK days=1 rows=3 sources=3 unreadable=0 unparsed=0 mixed=0 conflict=0 shrank=0
TOKENS NOTE nothing was wrong; nova-tokens check --out ./out is the gate

$ nova-tokens check --out ./out
CHECK OK at=2026-09-11T23:55:02Z build=devel files=1 rows=3 first=2026-09-11 last=2026-09-11 missing=0 stray=0

$ nova-tokens sum --out ./out --month 2026-09
SUM MONTH month=2026-09 at=2026-09-11T23:55:02Z build=devel days=1 first=2026-09-11 last=2026-09-11 missing=0 rows=3 turns=3
SUM PAIR model=claude-fable-5-1 repo=schema input=908 output=1535 cache_write=1200 cache_read=242000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 days=1
SUM PAIR model=gemini-2.5-pro repo=schema input=123456 output=7890 cache_write=- cache_read=- reasoning=- rough=0 dashes=0,0,1,1,1 nonutc=0 days=1
SUM PAIR model=claude-fable-5-1 repo=serialize input=430 output=58 cache_write=- cache_read=4000 reasoning=- rough=0 dashes=0,0,1,0,1 nonutc=0 days=1
SUM MODEL model=claude-fable-5-1 input=1338 output=1593 cache_write=1200 cache_read=246000 reasoning=- rough=0 dashes=0,0,1,0,2 nonutc=0 repos=2
SUM MODEL model=gemini-2.5-pro input=123456 output=7890 cache_write=- cache_read=- reasoning=- rough=0 dashes=0,0,1,1,1 nonutc=0 repos=1
SUM TOTAL input=124794 output=9483 cache_write=1200 cache_read=246000 reasoning=- rough=0 dashes=0,0,2,1,3 nonutc=0 turns=3 pairs=3 models=2
SUM OK month=2026-09 days=1 missing=0 pairs=3 models=2 nonutc=0
```


## nova-update

### First run

From the nova-tools checkout, using the declared Go-version fixture. This reads
local stdout only and performs no update or bus action.

```text
$ nova-update report --file cmd/nova-update/testdata/example.tsv
REPORT at=2026-09-12T17:29:33Z file=cmd/nova-update/testdata/example.tsv host=- as=- entries=1 kinds=engine,harness,model,pin,tool timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=8ms file=cmd/nova-update/testdata/example.tsv
```


## nova-version

### First run

From the nova-tools checkout, using the declared Go-version fixture. This reads
local stdout only and performs no update or bus action.

```text
$ nova-version report --file cmd/nova-version/testdata/example.tsv
REPORT at=2026-09-12T17:29:33Z file=cmd/nova-version/testdata/example.tsv host=- as=- entries=1 kinds=engine,harness,model,pin,tool timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=8ms file=cmd/nova-version/testdata/example.tsv
```
