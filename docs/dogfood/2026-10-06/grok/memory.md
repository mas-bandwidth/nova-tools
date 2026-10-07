# Dogfood: nova-memory — 2026-10-06, grok

Read cold as a stranger: only `nova-memory -h`, `nova-memory help`, every
verb's `-h`, and the page under `docs/` (`docs/CLI.md`'s `## nova-memory`
section and `docs/SPEC.md`'s `nova-memory — membership as a lookup, never a
scan`). Built from the checkout at e8f70f600 as
`nova-memory v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6`, and
used over scratch corpora in the job directory: every verb at least once with
its real flags against a store-free corpus, the refusals too. Nothing was
written by the tool and no server was started. Numbers below are my use, not a
benchmark.

The banner, the no-defaults law, the one-line refusal grammar, the
all-problems-at-once rule, the exit table and the class/name/type receipts are
all true to the page and the help: the `example:` block runs as printed, the
fixture transcript matches `docs/CLI.md` line for line, an unknown verb and an
unknown flag both name the nearest spelling, and every refusal I met named a
remedy. The findings are where a check that says it can fail cannot, where the
two renderings of one value disagree, and where a number labeled with a channel
is not that channel's number.

## Findings

1. `nova-memory verify --root ./corpus4 --links info --coverage 'notes/*.md:notes/*.md'`

       VERIFY OK gating=0 info=0 shown=0 coverage=0 frontmatter=0 links=info
       exit=0

   `corpus4/notes/alpha.md` and `corpus4/notes/beta.md` each hold one note and
   neither names the other, so by the help's rule — "every file matching glob A
   is named in some file matching glob B" — both are unnamed and the check must
   fail. It passes because a file that appears on both sides of the pair is
   skipped as its own index; with `A:B` naming one glob, every A file is a B
   file and the check can never fail. The disjoint control fails as it should:

       $ nova-memory verify --root ./corpus4 --links info --coverage 'notes/alpha.md:notes/beta.md'
       VERIFY FAIL coverage notes/alpha.md: stem "alpha" appears in no file matching notes/beta.md
       VERIFY FAIL gating=1 shown=1 info=0 coverage=1 frontmatter=0 links=info

   Neither the help (`--coverage <value>  A:B glob pair, repeatable`) nor the
   page's verb section states the exemption, so the natural "every note is named
   somewhere in the notes tree" invocation is green over real orphans. Grade:
   URGENT.

2. `nova-memory verify --root ./many --links gate --fail-max 1 --json`

       {"result":{"verb":"verify","status":"failed","exit":1},"facts":{"gating":25,"info":0,"coverage":0,"frontmatter":0,"links":"gate"},"items":[{"kind":"wikilink","fields":{"gating":true,"detail":"[[missing-1]] resolves to no file (e.g. from index.md)"}}],"more":[{"kind":"wikilink","shown":1,"total":25,"remedy":"--max <n> raises the ceiling, --max 0 lists all"}]}
       exit=1

   The same run without `--json` prints the remedy against the flag that exists:

       VERIFY MORE kind=wikilink shown=1 total=25 --fail-max <n> raises the ceiling, --fail-max 0 prints every finding

   `--max` is not a flag of `verify` (nor of `eval`, whose JSON `more` carries
   the same string); pasting the JSON remedy is refused:

       $ nova-memory verify --root ./many --links gate --max 5
       VERIFY REFUSED: unknown flag --max; the flags of verify are --coverage, --exclude, --exempt, --fail-max, --frontmatter, --json, --links, --root; run: nova-memory verify -h

   The two renderings of one result disagree, and the JSON remedy sends a reader
   to a flag the tool does not have. Grade: URGENT.

3. `nova-memory search --root ./corpus --channels bm25,trigram --k 3 glazing lantern`

       SEARCH OK hits=3 k=3 channels=bm25,trigram files=5 chunks=5: query="glazing lantern"
       SEARCH CAL score=0.12 score-channel=bm25 probe=unrelated-control
       SEARCH HIT rank=1 score=1.11 score-channel=bm25 fused=0.03306 class=journal name=- type=- root=./corpus: journal/1974-03-11.md:1 "Onshore gale most of the day. Washed the glazing at first light. See\n[[lantern-care]] and [[storm-glass]]."

   The same corpus and query with `--channels bm25` alone print:

       SEARCH CAL score=1.66 score-channel=bm25 probe=unrelated-control
       SEARCH HIT rank=1 score=1.21 score-channel=bm25 ... notes/lantern.md:5 ...
       SEARCH HIT rank=2 score=1.11 score-channel=bm25 ... journal/1974-03-11.md:1 ...

   Adding trigram cannot change a bm25 score: `journal/1974-03-11.md` is 1.11 in
   both runs. But the line the help tells the reader to compare that score
   against moves from 1.66 to 0.12 while still naming `score-channel=bm25`, so
   the same bm25 hit is below the band in one run and above it in the other. The
   help says "a hit's score= at or below it is no better than noise when the
   hit's score-channel= names the same channel"; with two channels that rule
   inverts on the same numbers. Grade: URGENT.

4. `nova-memory check --root ./corpus --channels bm25 --k 2 ./fm-draft2.md`

       MEMORY OK candidates=1 source=./fm-draft2.md k=2 channels=bm25 files=5 chunks=5
       MEMORY CAL score=1.66 score-channel=bm25 probe=unrelated-control
       MEMORY CAND n=1: "---\nname: jetty\n---\nhere are some unrelated body words entirely"

   `fm-draft2.md` carries the frontmatter `name: jetty` over a body with no
   corpus term; the identical file without the frontmatter prints `MEMORY MISS`.
   The page says the frontmatter block "is metadata, not body text ... it is
   never indexed and no receipt quotes it", and corpus frontmatter is in fact
   kept out of hits (the receipt for `notes/lantern.md` starts at its body line
   5, not at its `---` block). The `check` candidate is the exception: its
   `name:` is scored like prose, so a draft with frontmatter returns hits its
   body never asked for. Grade: NEXT.

5. `nova-memory stats --root ./corpus --exclude ./notes`

       STATS OK schema=nova-memory/2 files=5 chunks=5 bytes=690 vocab=73 avg-terms=19.4 build=644.699µs
       STATS OK class=. chunks=1
       STATS OK class=journal chunks=1

   `--exclude notes` gives the expected `files=2`; the ordinary relative
   spelling `./notes` excludes nothing, and an `--exclude` that matches nothing
   (`--exclude no-such-dir`) is accepted silently at `files=5` too, while a
   `--coverage` or `--frontmatter` glob that matches nothing is refused as "a
   broken check, not a pass". The help says "path or glob to skip" and does not
   say the path is matched without normalisation, so a reader's own spelling
   quietly widens the scope they meant to narrow. Grade: NEXT.

6. `nova-memory stats --root ./corpus --root ./corpus`

       STATS OK schema=nova-memory/2 files=10 chunks=10 bytes=1380 vocab=73 avg-terms=19.4 build=767.562µs
       STATS OK class=. chunks=2
       STATS OK class=journal chunks=2

   The same tree named twice is counted twice, and a query over overlapping
   roots returns the same memory twice in one ranking:

       $ nova-memory search --root ./corpus --root ./corpus/notes --channels bm25 --k 5 lantern
       SEARCH OK hits=5 k=5 channels=bm25 files=8 chunks=8: query="lantern"
       SEARCH HIT rank=3 score=0.46 ... root=./corpus: notes/lantern.md:5 ...
       SEARCH HIT rank=5 score=0.46 ... root=./corpus/notes: lantern.md:5 ...

   Expected one receipt per memory, or a refusal; `boot` refuses a duplicate pin
   entry for exactly this reason ("double-counted bytes are a lie") and `verify`
   refuses a second `--root`, so `--root` is the one place a duplicate is
   silent. Grade: NEXT.

7. `nova-memory boot --root ./corpus --pin ./pin.txt --pin ./pin3.txt`

       BOOT OK files=1 bytes=116
       exit=0

   `pin.txt` names two memories and `pin3.txt` one, so the second flag silently
   wins and the first is never read. The same last-wins silence is on `--k` and
   `--channels`:

       $ nova-memory search --root ./corpus --channels bm25 --k 2 --k 3 glazing
       SEARCH OK hits=3 k=3 channels=bm25 ...
       $ nova-memory search --root ./corpus --channels bm25 --channels trigram --k 2 glazing
       SEARCH OK hits=2 k=2 channels=trigram ...

   Expected a refusal naming the repeated flag, since a duplicated entry inside
   one pin file IS refused and `--channels` refuses a stray comma rather than
   quietly running fewer channels than asked. Grade: NEXT.

8. `nova-memory verify --root ./corpus --links info`

       VERIFY REFUSED: no gating check requested (no --coverage, no --frontmatter, --links=info) — a run that cannot fail is not a verification; run: nova-memory help
       exit=2

   The synopsis the help prints and `docs/CLI.md` both bracket `--coverage` and
   `--frontmatter` as optional and mark only `--links` required, so this reads
   as the minimal info run; it exits 2. Expected either the flags to say one
   gating selector is required, or the report to print and exit 0. The refusal
   names the next step only as `nova-memory help`, not the command that would
   work. Grade: NEXT.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.913s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	11.525s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.009s [no tests to run]

Run on the Linux bench with the tree synced there and the private cache
exported. The named test is not defined at this tip, so its run answers ok with
no tests to run; the whole docs and ci packages pass.

No code was changed in this card: the only added file is this report.

READ 7/10 — the banner, the verb helps and the page state the no-defaults law,
the refusal grammar, the calibration band and the exit table precisely, and
every refusal I met named a remedy; minus three for the coverage exemption
being invisible, the JSON remedy naming a flag that does not exist, and the
multi-channel calibration band's label not matching its number.

USE 6/10 — every verb ran first try for real against a scratch corpus, the
receipts are rich and the refusals unambiguous, but a coverage gate passes over
real orphans, the JSON remedy sends a reader to an unknown flag, the
calibration band flips the same bm25 hit's verdict with the channel list, and a
draft's frontmatter is scored as body text.

urgent=3 next=5
