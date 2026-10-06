# nova-memory dogfood, 2026-10-06, opencode (flash)

A stranger's use of `nova-memory` v1.0.1-0.20261006184440-8076dfdfcd85 from its
own `-h`, `help <verb>`, and `docs/CLI.md`/`docs/SPEC.md`, against a scratch
store of frontmatter notes, a dated log, wikilinks and a pin, plus the shipped
fixture corpus `cmd/nova-memory/testdata/corpus`. Every verb ran with its real
flags; refusals ran too. No code was changed.

1. `nova-memory search --root ./cmd/nova-memory/testdata/corpus --channels bm25 --k 3 glazing minutes pressure`
   printed:
   ```
   SEARCH OK hits=3 k=3 channels=bm25 files=6 chunks=20: query="glazing minutes pressure"
   SEARCH CAL score=4.05 score-channel=bm25 probe=unrelated-control
   SEARCH HIT rank=1 score=3.48 score-channel=bm25 fused=0.01667 class=notes name=lantern-care type=measured root=./cmd/nova-memory/testdata/corpus: notes/lantern.md:13 "..."
   ```
   expected: the banner's rule — "a hit's score= at or below it is no better than
   noise when the hit's score-channel= names the same channel" — calls this run's
   own rank-1 answer noise (3.48 <= 4.05), and the banner's embedded example does
   the same in words (`CAL score=1.46` beside `HIT rank=1 score=0.99`). An AI
   applying the rule discards the correct hit the tool just printed; a fused
   two-channel run can put a hit below CAL at rank 1 too (scratch store: CAL
   2.84, rank 1 score=2.81). Grade: URGENT.

2. `nova-memory verify --root ./store --root ./store --links gate`
   printed:
   ```
   VERIFY REFUSED: --root names exactly one tree for verification, but 2 were given; run: nova-memory help
   ```
   expected: `nova-memory verify -h` says `--root <value>  corpus root directory,
   repeatable (required)` and the banner says `--root` is "Repeatable
   (--root <dir> --root <dir> ...)" without excepting verify. The help lies about
   the flag; a script that merges two trees is refused where the help promised a
   merge. Grade: URGENT.

3. `nova-memory boot --root ./store --root ./store/notes --pin ./store/pin.txt`
   printed:
   ```
   BOOT OK files=2 bytes=473
   ```
   expected: the second `--root` is silently ignored (the pin resolves only under
   the first), so a session loads less than the flags it passed claim — the exact
   silent short-load `boot` exists to remove. It should refuse a second root as
   `verify` does, or name the root it used. Grade: URGENT.

4. `nova-memory stats --root ./store --exclude log/`
   printed:
   ```
   STATS OK schema=nova-memory/2 files=7 chunks=10 bytes=1178 vocab=111 avg-terms=19.0 build=403.912µs
   STATS OK class=log chunks=2
   ```
   expected: `--exclude log/` (a trailing slash, the shape a caller writes for a
   directory) excludes nothing and says nothing; `--exclude log` works. No NOTE
   reports that a pattern matched zero files, so a typo silently widens the
   corpus. Grade: NEXT.

5. `nova-memory boot --root ./store --pin ./store/pin.txt`
   printed:
   ```
   BOOT OK files=2 bytes=473
   ```
   expected: the two loaded memories are not named, on the line or in `--json`
   (`facts` is only `files`/`bytes`), so the reader must reread the pin to see
   what loaded. One `BOOT LOAD` line per file and the same items in JSON. Grade:
   NEXT.

6. `nova-memory help eval`
   printed:
   ```
   usage: nova-memory eval [flags]
   from `nova-memory help`:
     nova-memory eval   --root <dir>... --channels <list> --k <n> --floor <f> ...
   ```
   expected: `help eval` states the gold file's columns (`query<TAB>expected
   [,expected]`), which `#` and blank lines are ignored, and that a header row is
   not special; the format appears only by failing a row. Grade: NEXT.

7. `nova-memory eval --root ./store --channels bm25 --k 3 --floor 0.5 ./header-gold.tsv` (file: `query<TAB>expected_file`)
   printed:
   ```
   EVAL MISS query=query expected=expected_file
   EVAL FAIL recall@3=0.000 below floor 0.500 (0/1, misses=1 shown=1, mrr=0.000, channels=bm25)
   ```
   expected: a header row copied from the documented form is scored as a query
   and drags recall down, rather than being refused or skipped with its line
   number. Grade: NEXT.

8. `nova-memory eval --root ./store --channels bm25 --k 3 --floor 0.5 ./store/gold.tsv`
   printed:
   ```
   EVAL MISS query=tide\x20tables\x20jetty expected=tides.md
   EVAL OK recall@3=0.667 floor=0.500 rows=3 hits=2 misses=1 shown=1 mrr=0.444 channels=bm25
   ```
   expected: the same field `query=` is rendered `query="lantern glazing cloths"`
   by `search` and `query=tide\x20tables\x20jetty` by `eval`; one noun, two
   quotings. Grade: NEXT.

9. `nova-memory check --root ./store --channels bm25 --k 2 ./store/draft.md`
   printed:
   ```
   MEMORY OK candidates=1 source=./store/draft.md k=2 channels=bm25 files=7 chunks=10
   MEMORY CAL score=2.84 score-channel=bm25 probe=unrelated-control
   MEMORY HIT cand=1 rank=1 score=17.86 ...
   ```
   expected: every other verb leads its lines with its own name (SEARCH, STATS,
   VERIFY, EVAL, BOOT, QUICKSTART); `check` leads with `MEMORY`, so an AI keying
   on the verb name finds nothing to key on. Grade: NEXT.

10. `nova-memory check --root ./store --channels bm25 --k 2 ./nope.md`
    printed:
    ```
    MEMORY REFUSED: open ./nope.md: no such file or directory; run: nova-memory help
    ```
    expected: the refusal is a raw OS error and the remedy is the whole banner;
    it should say what the input wants (an existing candidate file, or `-` for
    stdin), as the missing-input refusal does. Grade: NEXT.

11. `nova-memory search --root ./store --channels bm25 --k 2 lantern glazing --json`
    printed:
    ```
    ... "snippet":"- [lantern-care](lantern.md) — the glazing, the brass, and the two cloths\n- [tide-tables](tides.md) — the jetty's ri…"
    ```
    expected: the hit's text is cut mid-sentence with no flag that widens it, and
    `--json` carries the same cut; the `file:line` tail lets the reader open the
    file, but the JSON is not self-contained. Grade: NEXT.

12. `nova-memory verify --root ./store --links gate --json`
    printed:
    ```
    {"result":{"verb":"verify","status":"failed","exit":1},"facts":{"gating":2,"info":0,"coverage":0,"frontmatter":0,"links":"gate"},"items":[...]}
    ```
    expected: `eval --json` on a failing floor carries
    `"why":["recall@3=0.667 is below the floor 0.900"]`; `verify --json` carries
    no reason for its failed status, so the envelope alone does not say why. Grade:
    NEXT.

13. `nova-memory verify` (no flags)
    printed:
    ```
    VERIFY REFUSED: --links is required; refusing to guess; run: nova-memory help
      --links gate makes unresolved wikilinks fail; --links info reports them without failing.
    VERIFY REFUSED: --root is required; refusing to guess; run: nova-memory help
    ```
    expected: the refusal knows the verb and every missing flag; the remedy should
    be the paste that fixes it (`run: nova-memory verify --root <dir> --links
    gate`), not the banner every refusal already points at. Grade: NEXT.

## Gate

`go test -count=1 -timeout 600s ./internal/docs ./internal/ci`
```
ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.288s
ok  	github.com/mas-bandwidth/nova-tools/internal/ci	15.585s
```
The card's named test `TestDocsTreeIsConsistent` does not exist in this tree;
the new `docs/dogfood/` directory did require a catalog row (`internal/docs/catalog.go`)
and `make map`, which the gate above holds.

READ 7/10
The help is a real door — a runnable `example:` block, every verb answers `-h` at exit 0, refusals name every missing flag at once with what each wants — but the banner's own CAL rule condemns the example printed beside it, and `--root` is documented as repeatable while `verify` refuses it and `boot` silently ignores it, so a stranger cannot trust the two sentences the tool leads with.

USE 6/10
Every verb is store-free and ran on a scratch store in one session with no setup and no writes, and the refusals are the best I met in this tree — but `boot` reports a byte total and names no memory, `--exclude log/` silently excludes nothing, a gold header row is scored as a query, and hit text is unrecoverable from JSON, so the tool bounds reading without always letting a reader trust the bound.

urgent=3 next=10
