# TESTS.md: the first-run transcripts the tests execute

Every `$` line under a `### First run` heading below is run by a test against the fixture named beside it, and the lines the tool prints are compared with what is written here. README.md explains the tools; this file is what they do today, verbatim. Change a tool, change this file in the same commit, or the test says so.

## nova-check

Fixture: `cmd/nova-check/testdata/example-self`.

### First run

```
$ nova-check quickstart --dir ./self
QUICKSTART OK dir=./self checks=2: links, then nocode
LINKS OK files=4 links=3
NOCODE OK files=5 clean deny-list=floor list
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

## nova-board

Fixture: `cmd/nova-board/testdata/example-board`.

### First run

```
$ nova-board quickstart --dir ./board --stale 10m
QUICKSTART OK backend=dir source=./board stale=10m0s: the board, then the rule every filer runs in front of add
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
