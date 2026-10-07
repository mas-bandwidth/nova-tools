# Dogfood: nova-memory — 2026-10-06, dsh

One friend, one tool, cold. I read only `nova-memory -h`, `nova-memory help`,
every verb's `-h`, and nova-memory's page under docs/ (the `## nova-memory`
section of `docs/CLI.md`), then used every verb at least once with its real
flags against a scratch corpus on a Linux bench, the refusals too. The binary
was built from this checkout at e8f70f600 (`nova-memory
v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6`) and run over ssh
in scratch directories under the job: an invented six-file corpus with
frontmatter, classes, wikilinks and a listing index; a second root; an empty
directory; a directory of twenty-five unnoted files; a 9 MiB file for the size
cap; pin files (good, a missing entry, empty, non-canonical, `..`, absolute, a
directory, a large file); a gold set with a missing row and one with no TAB;
and drafts (a file, stdin, multi-paragraph, empty, stopwords). No server, no
real network, and the tool wrote nothing outside the scratch trees. No code
changed.

## Findings

1. `nova-memory verify --root ./scratch/many --links info --frontmatter '*.md' --fail-max 3 --json`

       {"result":{"verb":"verify","status":"failed","exit":1},"facts":{"gating":25,"info":0,"coverage":0,"frontmatter":25,"links":"info"},"items":[...],"more":[{"kind":"frontmatter","shown":3,"total":25,"remedy":"--max <n> raises the ceiling, --max 0 lists all"}]}

   One line; the three repeated items are elided here as `[...]`, the `more`
   object is verbatim. The line rendering of the same run says `--fail-max <n>
   raises the ceiling, --fail-max 0 prints every finding`, and `eval`'s JSON
   `more` object has the same `"remedy":"--max <n> raises the ceiling, --max 0
   lists all"`. I expected one flag name in both renderings: `--fail-max`.
   The JSON remedy instead names `--max`, which is not a flag of this tool,
   and following it is refused: `nova-memory verify --root ./scratch/many
   --links info --frontmatter '*.md' --max 5` prints `VERIFY REFUSED: unknown
   flag --max; the flags of verify are --coverage, --exclude, --exempt,
   --fail-max, --frontmatter, --json, --links, --root; run: nova-memory verify
   -h`. A remedy a reader cannot run is help that lies. **URGENT.**

2. `nova-memory eval --root ./scratch/corpus --channels bm25 --k 1 --floor 0.001 --fail-max 2 ./scratch/gold-miss.tsv`

       EVAL MISS query=unrelated\x20one expected=notes/tides.md
       EVAL MISS query=unrelated\x20two expected=HANDBOOK.md
       EVAL MORE kind=miss shown=2 total=4 --fail-max <n> raises the ceiling, --fail-max 0 prints every finding

   I expected the query printed as a readable field, the way `SEARCH OK ...
   query="glazing minutes pressure"` prints it. Instead every gap between two
   words is escaped as `\x20`, so a reader has to decode the query and compare
   it by eye. The
   JSON rendering of the same value is clean (`"query":"unrelated one"`), so
   the two renderings disagree on readability. **NEXT.**

3. `nova-memory check --root ./scratch/corpus --channels bm25 --k 2 ./scratch/nope.md`

       MEMORY REFUSED: open ./scratch/nope.md: no such file or directory; run: nova-memory help

   I expected the tool's own refusal grammar, naming what the argument wants (a
   markdown draft file that exists and is readable) and carrying a hint line
   like `--root`'s `is not a readable directory`. Instead it forwards the raw
   `open` error. `eval ./scratch/no-gold.tsv` prints `EVAL REFUSED: open
   ./scratch/no-gold.tsv: no such file or directory; run: nova-memory help`,
   and `boot --pin ./scratch/corpus/notes` prints `BOOT REFUSED: read
   ./scratch/corpus/notes: is a directory; run: nova-memory help`. Three file
   arguments, three raw errors. **NEXT.**

4. `nova-memory verify --root ./scratch/corpus --links info`

       VERIFY REFUSED: no gating check requested (no --coverage, no --frontmatter, --links=info) — a run that cannot fail is not a verification; run: nova-memory help

   I expected the run the help describes for `--links info`: the verb's own
   help says `--links info` reports unresolved wikilinks "without failing", and
   `docs/CLI.md` lists `--coverage` and `--frontmatter` as optional extras, so
   a reader has no way to learn that `info` alone is refused. Either the run
   should report the info-level wikilink findings, or `verify -h` and the docs
   page should say a gating check is required. The refusal itself is clear and
   one turn. **NEXT.**

5. `nova-memory search --root ./scratch/corpus --channels bm25 --k 3 glazing`

       SEARCH OK hits=3 k=3 channels=bm25 files=6 chunks=7: query="glazing"
       SEARCH CAL score=3.58 score-channel=bm25 probe=unrelated-control
       SEARCH HIT rank=1 score=0.75 score-channel=bm25 fused=0.01667 class=notes name=lantern-care type=measured root=./scratch/corpus: notes/lantern.md:7 "Measured over one winter: glazing washed weekly held its polish; glazing washed monthly needed grinding twice. Brass pol…"

   All three hits score 0.75, 0.62 and 0.59 against CAL 3.58, and the help
   says a hit whose score is at or below CAL "is no better than noise". The
   tool's own shipped fixture first run is the same shape (rank 1 3.48, CAL
   4.05, and all three fixture hits below CAL). I expected a line saying no hit
   is above the calibration line — a MISS on this evidence — instead of
   `SEARCH OK hits=3` with no remark, because a reader who trusts the OK word
   reads noise as evidence. **NEXT.**

6. `nova-memory search --root ./scratch/corpus --channels bm25 --channels trigram --k 3 glazing`

       SEARCH OK hits=3 k=3 channels=trigram files=6 chunks=7: query="glazing"
       SEARCH CAL score=0.10 score-channel=trigram probe=unrelated-control
       SEARCH HIT rank=1 score=0.06 score-channel=trigram fused=0.01667 class=notes name=- type=- root=./scratch/corpus: notes/index-notes.md:3 "- [lantern-care](lantern.md) — the glazing, the brass, the cloths\n- [tide-tables](tides.md) — the jetty and the tide…"

   I expected a flag named twice to be refused, or for the help to say the last
   one wins. Instead `--channels` silently became `trigram`, `--k 3 --k 5`
   silently became 5, `verify --links gate --links info` silently ran as `info`
   (so the gate did not gate), `eval --floor 0.5 --floor 0.9` silently used
   0.9, and `boot --root ./scratch --root ./scratch/corpus --pin ...` silently
   checked the last root only. Every summary line does name the value used, but
   nothing says a flag was given twice. **NEXT.**

7. `nova-memory search --root repo/cmd/nova-memory/testdata/corpus --channels trigram --k 3 unrelated-control`

       SEARCH OK hits=3 k=3 channels=trigram files=6 chunks=20: query="unrelated-control"
       SEARCH CAL score=0.11 score-channel=trigram probe=unrelated-control
       SEARCH HIT rank=1 score=0.02 score-channel=trigram fused=0.01667 class=. name=- type=- root=repo/cmd/nova-memory/testdata/corpus: HANDBOOK.md:10 "The station keeps three kinds of record, and the top-level directory is the\nclass. Root files like this one are the stan…"

   The same query under `--channels bm25` prints `SEARCH MISS every query term
   is out of vocabulary for this corpus`; the trigram channel instead returns
   three spurious character matches, every score below CAL, with no MISS. I
   expected either the MISS or a note that every hit is below the calibration
   line. **NEXT.**

READ 8/10 — the banner names the nouns, the first run is a copyable transcript
that runs as printed, the docs page explains CAL and every flag, and refusals
list every missing flag in one turn; marks off because `verify --links info`
alone is refused without the help saying a gating check is required, and the
docs page's verb block omits `version` while the binary serves it.

USE 7/10 — every verb ran for real on scratch corpora and refusals were one
turn and complete, but the JSON MORE remedy names a flag that does not exist,
search can print `OK hits=N` when every hit is at or below its own noise line,
and a repeated single-value flag silently takes the last.

urgent=1 next=6
