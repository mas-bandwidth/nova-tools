# nova-memory

## What it is

nova-memory: search your own markdown notes, and check a draft against what they already say

## Why use it

Find a note without rereading everything.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-memory@latest
nova-memory version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-memory).

Fixture: `cmd/nova-memory/testdata/corpus`.

```text
$ nova-memory quickstart --root ./corpus
QUICKSTART RUN root=./corpus steps=3 channels=bm25 k=3/2 words-source=corpus-top-terms candidate=corpus-first-paragraph words="glazing minutes pressure"
$ nova-memory stats --root ./corpus
STATS OK schema=nova-memory/2 files=6 chunks=20 bytes=4866 vocab=380 avg-terms=39.3 build=822.917µs
STATS OK class=. chunks=3
STATS OK class=log chunks=4
STATS OK class=notes chunks=13
$ nova-memory search --root ./corpus --channels bm25 --k 3 glazing minutes pressure
SEARCH OK hits=3 k=3 channels=bm25 files=6 chunks=20: query="glazing minutes pressure"
SEARCH CAL score=4.05 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=3.48 score-channel=bm25 fused=0.01667 class=notes name=lantern-care type=measured root=./corpus: notes/lantern.md:13 "Measured over one winter: glazing washed weekly held its polish; glazing\nwashed monthly needed grinding twice. The weekl…"
SEARCH HIT rank=2 score=3.12 score-channel=bm25 fused=0.01639 class=notes name=- type=- root=./corpus: notes/index-notes.md:8 "- [lantern-care](lantern.md) — the glazing, the brass, and the two cloths\n- [tide-tables](tides.md) — the jetty's ei…"
SEARCH HIT rank=3 score=2.97 score-channel=bm25 fused=0.01613 class=notes name=fog-signal type=measured root=./corpus: notes/fog-signal.md:8 "The diaphone runs on compressed air, and the compressor needs eleven minutes\nto bring the receiver to working pressure f…"
SEARCH NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: HANDBOOK.md:3
$ nova-memory check --root ./corpus --channels bm25 --k 2 -
MEMORY OK candidates=1 source=- k=2 channels=bm25 files=6 chunks=20
MEMORY CAL score=4.05 score-channel=bm25 probe=unrelated-control
MEMORY CAND n=1: "This fixture corpus belongs to an invented lighthouse station. It exists so\nthat nova-memory's verbs…"
MEMORY HIT cand=1 rank=1 score=90.20 score-channel=bm25 fused=0.01667 class=. name=- type=- root=./corpus: HANDBOOK.md:3 "This fixture corpus belongs to an invented lighthouse station. It exists so\nthat nova-memory's verbs can be exercised …"
MEMORY HIT cand=1 rank=2 score=15.01 score-channel=bm25 fused=0.01639 class=log name=- type=- root=./corpus: log/1974-03-11.md:11 "Left a note to write up the [[storm-glass]] readings against the barometer\none day, because the two disagree in a way th…"
MEMORY NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours
MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction
QUICKSTART OK done=3
QUICKSTART NOTE this used bm25 alone and k=3/2; those are choices, not defaults: see --channels and --k
```

## Verbs

The [nova-memory section of the command reference](../../docs/CLI.md#nova-memory) documents every verb's flags, effect and exit codes.

- `version`
- `quickstart`
- `stats`
- `search`
- `check`
- `verify`
- `eval`
- `boot`

## Spec

The contract is [docs/SPEC.md](../../docs/SPEC.md).
