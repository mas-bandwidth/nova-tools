# nova-memory dogfood — antigravity, 2026-10-06

Read as a stranger: only `nova-memory -h`, `nova-memory help`, `nova-memory
<verb> -h`, and the tool's page under `docs/` (docs/CLI.md § nova-memory; the
gold-set form came from the example file that page names,
`cmd/nova-memory/testdata/example-gold.tsv`). Built from the staged checkout
at cb5fb8d4c3290f710d22a21a86aa8d229e4db905 and used as `nova-memory devel
darwin/arm64 go1.26.6` (cross-compiled on the repo's Linux bench, then run
where the scratch store lives). Every verb ran at least once with its real flags against a scratch
store of nine garden-record markdown files — dated logs, notes with frontmatter,
a listing index, a reference page, plus a second root of two files for
multi-root — and once against this repo's own `docs/` tree (229 files,
6009 chunks, 226 ms build). The refusals and boundaries ran too: no args,
unknown verb, two missing required flags in one run, a bogus channel, `--k 0`,
missing draft and pin files, a pin naming a missing memory, a non-canonical
pin path, floors out of range, a 8,880,055-byte markdown file against the
8 MiB cap, and `--fail-max` with 25 planted broken wikilinks.

## Findings

1. `eval` accepts a gold row with a stray second tab and silently measures a
   row that can never hit — URGENT

   Command as typed:

   ```
   nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.6 gold-mangled2.tsv
   ```

   (`gold-mangled2.tsv` holds the same six rows as `gold.tsv`; only row 1
   differs — `when did the first frost arrive<TAB>this year<TAB>log/2026-09-28.md`,
   two tabs, where `gold.tsv` has one.) What it printed (first 3 lines):

   ```
   EVAL MISS query=when\x20did\x20the\x20first\x20frost\x20arrive expected=this\x20year\x09log/2026-09-28.md
   EVAL MISS query=what\x20killed\x20the\x20greenhouse\x20plants\x20in\x20september expected=log/2026-09-28.md
   EVAL MISS query=what\x20mulch\x20does\x20to\x20tomato\x20yield expected=notes/tomatoes.md
   ```

   with the summary `EVAL FAIL recall@3=0.500 below floor 0.600 (3/6, misses=3
   shown=3, mrr=0.500, channels=bm25)`, exit 1 — while the same six rows with
   the tab fixed print `EVAL OK recall@3=0.667 floor=0.600 rows=6 hits=4`,
   exit 0. The stray tab was folded into the expectation
   (`expected=this\x20year\x09log/2026-09-28.md`), a substring no path can
   contain, so the row can never hit and dragged recall down invisibly — the
   exact failure the example gold file says refusing no-expectation rows exists
   to prevent, and `eval` does refuse a row with no tab at all: `EVAL
   REFUSED: gold-tabless.tsv line 1 has no TAB (format:
   query<TAB>expected[,expected])`. I expected a row that violates that
   stated format to be refused the same way, not to flip the verdict on a
   measurement.
   Grade: URGENT.

2. a two-channel run prints one CAL line, which is neither channel's alone —
   NEXT

   Command as typed:

   ```
   nova-memory search --root ./corpus --channels bm25,trigram --k 2 tomatos blght
   ```

   What it printed (first 3 lines):

   ```
   SEARCH OK hits=2 k=2 channels=bm25,trigram files=9 chunks=16: query="tomatos blght"
   SEARCH CAL score=2.05 score-channel=bm25 probe=unrelated-control
   SEARCH HIT rank=1 score=0.03 score-channel=trigram fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:3 "- [Tomato care](tomatoes.md) — mulch, side shoots, blight, compost tea\n- [Compost](compost.md) — turning, layers, th…"
   ```

   Both receipts are scored by trigram (0.03), but the only CAL line names
   bm25, and that number is not the bm25-only run's CAL either: the same
   corpus, query and k under `--channels bm25` alone prints `SEARCH CAL
   score=2.11 score-channel=bm25`, and under `--channels trigram` alone
   prints `SEARCH CAL score=0.08 score-channel=trigram` — a band the 0.03
   receipts fall below. The help says a hit's score "compares only with
   scores of that channel and with the CAL line", and the docs page says a HIT
   means something only when its score is clearly above CAL; in this run no
   printed CAL can be compared against either channel's receipts. I expected
   one CAL line per channel that scores receipts, each equal to that
   channel's single-channel CAL.
   Grade: NEXT.

3. `--exclude` matches paths relative to the root, says so nowhere, and a
   no-match exclusion is silent — NEXT

   Command as typed (the cap refusal's own remedy, applied the natural way):

   ```
   nova-memory stats --root ./corpus-big --exclude 'corpus-big/huge.md'
   ```

   What it printed (the whole output is one line):

   ```
   STATS REFUSED: building the index over ./corpus-big: huge.md is 8880055 bytes, over the 8388608-byte file cap; exclude it with --exclude; run: nova-memory help
   ```

   identical to the same run with no `--exclude` at all: the glob is matched
   against the path relative to `--root` (`huge.md` excludes the file),
   while `corpus-big/huge.md`, `./corpus-big/huge.md` and `**/huge.md` match
   nothing, and nothing in the output says the exclusion matched no file. The
   `-h` says only "path or glob to skip". I expected either the help to state
   the base the glob is matched against, or a run whose exclusion matched
   nothing to say so.
   Grade: NEXT.

4. `**/glob` does not match files at the root — NEXT

   Command as typed:

   ```
   nova-memory stats --root ./corpus --exclude '**/*.md'
   ```

   What it printed (first 3 lines):

   ```
   STATS OK schema=nova-memory/2 files=1 chunks=3 bytes=587 vocab=65 avg-terms=32.7 build=286.875µs
   STATS OK class=. chunks=3
   exit=0
   ```

   Excluding `**/*.md` left `HANDBOOK.md` — a file at the root of the corpus —
   still indexed (files=1, class=. chunks=3). Every glob dialect I know
   (gitignore, doublestar) matches `**/` against zero directories as well; a
   stranger excluding "all markdown" silently keeps root-level files in the
   index, and the same asymmetry is what kept `**/huge.md` from excluding a
   root-level `huge.md` in finding 3. I expected `**/*.md` to exclude every
   `.md` file under the root, or the help to name the dialect it implements.
   Grade: NEXT.

5. `EVAL MISS` lines escape spaces and tabs into one unreadable token — NEXT

   Command as typed (any run with misses):

   ```
   nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.6 gold-mangled2.tsv
   ```

   What it printed (first 3 lines):

   ```
   EVAL MISS query=what\x20killed\x20the\x20greenhouse\x20plants\x20in\x20september expected=log/2026-09-28.md
   EVAL MISS query=what\x20mulch\x20does\x20to\x20tomato\x20yield expected=notes/tomatoes.md
   EVAL FAIL recall@3=0.500 below floor 0.600 (3/6, misses=3 shown=3, mrr=0.500, channels=bm25)
   ```

   Every other line of this tool quotes natural text (`query="frost week
   compost"`, `MEMORY CAND n=1: "Water in the evening..."`), but the lines a
   reader must study to grow the gold set — the stated remedy for a miss —
   render the query and the expectation as escaped token soup. I expected
   `query="what killed the greenhouse plants in september"`.
   Grade: NEXT.

6. the listing chunk outranks the entries it lists — NEXT

   Command as typed:

   ```
   nova-memory search --root ./corpus --channels bm25 --k 3 frost tomatoes mulch
   ```

   What it printed (first 3 lines):

   ```
   SEARCH OK hits=3 k=3 channels=bm25 files=9 chunks=15: query="frost tomatoes mulch"
   SEARCH CAL score=2.37 score-channel=bm25 probe=unrelated-control
   SEARCH HIT rank=1 score=2.91 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:3 "- [Tomato care](tomatoes.md) — mulch, side shoots, blight, compost tea\n- [Compost](compost.md) — turning, layers, th…"
   ```

   For every topical query I ran over this corpus the rank-1 receipt was the
   notes index — a table of contents — rather than the note it lists, and the
   docs page's own example shows the same shape. For work retrieval a pointer
   is not the passage, and nothing in the help suggests `--exclude
   'notes/index*'` as the remedy for a search that wants entries, not
   listings. I expected either listings to rank below the entries they list or
   the help to name the choice.
   Grade: NEXT.

7. `verify` reports an excluded file as "does not exist" — NEXT

   Command as typed:

   ```
   nova-memory verify --root ./corpus --links gate --exclude 'reference/*' --coverage 'notes/*.md:notes/index-notes.md'
   ```

   What it printed (first 3 lines):

   ```
   VERIFY FAIL backlink notes/index-notes.md links ../reference/frost-dates.md which does not exist (resolved reference/frost-dates.md)
   VERIFY FAIL wikilink [[frost-dates]] resolves to no file (e.g. from log/2026-09-21.md, notes/tomatoes.md, notes/tomatoes.md)
   VERIFY FAIL gating=2 shown=2 info=0 coverage=1 frontmatter=0 links=gate
   ```

   `reference/frost-dates.md` exists; this run excluded it, and the finding
   says it "does not exist". A stranger reading the line checks the
   filesystem, finds the file, and is confused about their own corpus; "is
   not in this run (excluded or missing)" would be true. I expected the
   finding to distinguish excluded from missing.
   Grade: NEXT.

## What the tool got right

- The refusals teach: every one names the remedy, a run short two flags
  prints two sentences in one run exactly as `-h` promises, and the tabless
  gold row is refused with the format printed in the refusal.
- The caps protect and say so: the 8,880,055-byte file was refused with its
  byte count and the 8,388,608-byte limit, and `--exclude` is the remedy
  once the right glob form is found (finding 3).
- `quickstart` is a true first run: every line it printed was a command I
  could copy and edit, and it named its choices (channels, k) as choices.
- `eval` measured honestly: bm25 0.667, bm25+trigram 0.667 with worse MRR
  (0.444 vs 0.556), trigram 0.500 — the help's claim that a second channel can
  measure worse is real, and below-floor runs fail at exit 1 with misses
  listed.
- `boot` loaded exactly the pinned files (`BOOT OK files=5 bytes=2425`) and
  refused a pin naming a missing memory with the entry named.
- A search that finds nothing is still exit 0 and says why ("every query
  term is out of vocabulary for this corpus").
- Multi-root indexing works: receipts name their root, and the second root's
  files ranked where they belonged once the query shared vocabulary.

Not run: the 64 MiB corpus cap and the 1,000,000-term vocabulary cap (only
the 8 MiB file cap was exercised), and boot's pin order beyond the counts
(the receipt names neither the files nor their order).

READ 9/10 — the banner answers what it does and how the CAL line is read
before either is needed, every verb's `-h` carries its flags and exit codes,
and the refusals print the format they enforce; only `--exclude`'s unstated
base and the gold form living in an example file instead of `eval -h` keep it
off a 10.

USE 8/10 — a stranger can run all seven verbs from help alone on a scratch
store and every refusal names the next step; the stray-tab trap (finding 1)
silently flipped a measurement to FAIL, and two-channel receipts (finding 2)
cannot be judged against any printed CAL, which is where the two points went.

urgent=1 next=6
