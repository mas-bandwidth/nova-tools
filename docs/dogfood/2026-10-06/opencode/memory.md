# Dogfood: nova-memory — 2026-10-06, opencode

One friend, one tool, cold. I read only `nova-memory -h`, `nova-memory help`,
every verb's `-h`, and the tool's page under `docs/` (`docs/SPEC.md`, the
`nova-memory — membership as a lookup, never a scan` section), then used every
verb at least once with its real flags against scratch corpora in the job
directory, the refusals included. The binary was built from this checkout at
d762f5454. No server, no network, no write path; every run read a scratch
corpus. Numbers below are my use, not a benchmark.

The no-defaults law, the one-line refusal grammar, the equal treatment of every
missing flag in one run, the calibration band, the class/name/type receipts and
the exit table all match the page and the help. The findings are where the two
renderings of one value disagree, and where a check that says it can fail does
not.

## Findings

1. `nova-memory verify --root ./corpus7 --links info --coverage 'notes/*.md:notes/*.md'`

       VERIFY OK gating=0 info=0 shown=0 coverage=0 frontmatter=0 links=info

   `corpus7/notes/alpha.md` holds "qqq wumblefrotz snazzle bimble." and
   `corpus7/notes/beta.md` holds "unrelated words here about nothing."; neither
   text names the other, so by the page's rule — every file matching `globA` is
   named, by stem, in some file matching `globB` — both are orphans and the
   check must fail. It passes because `Coverage` in
   `internal/memindex/verify.go` skips any A-side file that is also a B-side
   file (`if bSet[a] { continue // an index file need not index itself }`):
   with one glob for both sides every A file is skipped and the check can never
   fail. The control with disjoint sides fails as it should:

       $ nova-memory verify --root ./corpus7 --links info --coverage 'notes/alpha.md:notes/beta.md'
       VERIFY FAIL coverage notes/alpha.md: stem "alpha" appears in no file matching notes/beta.md
       VERIFY FAIL gating=1 shown=1 info=0 coverage=1 frontmatter=0 links=info

   Neither the help (`--coverage <A:B> ... every file matching glob A is named in some file matching glob B`) nor the page states the exemption, so the
   natural "every note is named somewhere in the notes tree" invocation is
   green over a real orphan. Grade: URGENT.

2. `nova-memory verify --root ./many --links gate --fail-max 1 --json`

       {"result":{"verb":"verify","status":"failed","exit":1},"facts":{"gating":25,"info":0,"coverage":0,"frontmatter":0,"links":"gate"},"items":[{"kind":"wikilink","fields":{"gating":true,"detail":"[[missing-1]] resolves to no file (e.g. from index.md)"}}],"more":[{"kind":"wikilink","shown":1,"total":25,"remedy":"--max <n> raises the ceiling, --max 0 lists all"}]}

   The same run without `--json` prints the remedy against the flag that
   exists:

       VERIFY MORE kind=wikilink shown=1 total=25 --fail-max <n> raises the ceiling, --fail-max 0 prints every finding

   `--max` is not a flag of `verify` (nor of `eval`, whose JSON `more` carries
   the same string); pasting it is refused:

       $ nova-memory verify --root ./many --links gate --max 5
       VERIFY REFUSED: unknown flag --max; the flags of verify are --coverage, --exclude, --exempt, --fail-max, --frontmatter, --json, --links, --root; run: nova-memory verify -h

   The two renderings of one result disagree, and the JSON remedy names a flag
   the tool does not have. Grade: URGENT.

3. `nova-memory verify --root ./corpus10 --links info --coverage 'notes/my file.md:notes/index.md'`

       VERIFY FAIL coverage notes/my file.md: stem "my file" appears in no file matching notes/index.md
       VERIFY FAIL backlink notes/index.md links my%20file.md which does not exist (resolved notes/my%20file.md)
       VERIFY FAIL gating=2 shown=2 info=0 coverage=2 frontmatter=0 links=info

   `corpus10/notes/my file.md` exists and `corpus10/notes/index.md` names it
   with the URL-encoded form `[spaced](my%20file.md)`, which is the standard
   CommonMark way to write a destination holding a blank between words (the
   angle-bracket form `[spaced](<my file.md>)` resolves and passes). The link half of `Coverage`
   resolves the target literally, so a link to a file that exists is reported
   dangling and the named file is reported unnamed in the same run. Expected:
   `%20` decoded before resolution (or the page saying it is not). Grade: NEXT.

4. `nova-memory stats --root ./corpus --exclude ./notes`

       STATS OK schema=nova-memory/2 files=6 chunks=6 bytes=602 vocab=58 avg-terms=15.0 build=659.209µs
       STATS OK class=. chunks=1
       STATS OK class=journal chunks=1

   Expected `files=3` (the notes tree gone), which is what the bare form
   prints — `--exclude notes` gives `files=3 chunks=3`. `--exclude ./notes` and
   `--exclude notes/` silently exclude nothing, and an `--exclude` that matches
   nothing (`--exclude no-such-dir`) is accepted silently, while a
   `--coverage` or `--frontmatter` glob that matches nothing is refused as "a
   broken check, not a pass". The help says "path or glob to skip" and does not
   say the path is matched without normalisation, so a reader's ordinary
   relative path quietly widens the scope it meant to narrow. Grade: NEXT.

5. `nova-memory stats --root ./corpus --root ./corpus`

       STATS OK schema=nova-memory/2 files=12 chunks=12 bytes=1204 vocab=58 avg-terms=15.0 build=640.375µs
       STATS OK class=. chunks=2
       STATS OK class=journal chunks=2

   The same tree named twice is counted twice (6 files become 12) and a query
   over overlapping roots returns the same memory twice in one ranking:

       $ nova-memory search --root ./corpus --root ./corpus/notes --channels bm25 --k 5 lantern
       SEARCH OK hits=5 k=5 channels=bm25 files=9 chunks=9: query="lantern"
       SEARCH HIT rank=1 score=0.54 ... root=./corpus: notes/index-notes.md:1 "[Lantern care](lantern.md) keeps the glazing clean."
       SEARCH HIT rank=2 score=0.54 ... root=./corpus/notes: index-notes.md:1 "[Lantern care](lantern.md) keeps the glazing clean."

   Expected one receipt per memory, or a refusal; `boot` refuses a duplicate pin
   entry for exactly this reason ("double-counted bytes are a lie"), and
   `verify` refuses two `--root`s, so `--root` is the one place a duplicate is
   silent. Grade: NEXT.

6. `nova-memory boot --root ./corpus --pin pin.txt --pin pin2.txt`

       BOOT OK files=1 bytes=165

   `pin.txt` names two memories and `pin2.txt` names one; expected three files
   loaded (or a refusal naming the repeated flag), since the page says a boot
   that silently skipped a named memory is "the exact failure this verb exists
   to remove". The second pin silently wins and the first is never read; a
   duplicated entry inside one pin file is refused, but a duplicated `--pin` is
   not. `--root` is collapsed the same way. Grade: NEXT.

7. `nova-memory stats --root ./gitcorpus/.git`

       STATS OK schema=nova-memory/2 files=1 chunks=1 bytes=43 vocab=7 avg-terms=7.0 build=240.875µs
       STATS OK class=. chunks=1

   The page states "`.git` is never a corpus and is always skipped"; it is
   skipped when it sits under a named root, but naming it as the root indexes
   it. Expected a refusal or the skip the page promises. Grade: NEXT.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.142s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	18.058s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.009s [no tests to run]

Run on a Linux bench with the tree synced there and `origin/dev` fetched; a
stale `origin/dev` makes the ledger-ratchet class test read the whole sprint as
growth (the fetch is an environment fix, not a code change). The named test
does not exist at this tip — no `TestDocsTreeIsConsistent` is defined — so the
run answers ok with no tests to run. No code was changed in this card: the
only added file is this report.

READ 8/10 — the banner, the verb helps and the page state the no-defaults law,
the refusal grammar, the calibration band and the exit table precisely and
mostly truly, and every refusal I met named its remedy; minus two for the
coverage exemption being invisible and the JSON remedy naming a flag that does
not exist.

USE 7/10 — every verb ran first try for real against a scratch corpus, the
refusals are unambiguous and the receipts are rich, but a coverage check can
pass over a real orphan and the JSON remedy sends a reader to an unknown flag.

urgent=2 next=5
