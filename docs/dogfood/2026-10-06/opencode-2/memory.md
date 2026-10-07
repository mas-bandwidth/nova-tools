# nova-memory dogfood — opencode-2, 2026-10-06

Read cold, as a stranger: only `nova-memory -h`, `nova-memory help`, each verb's `-h`, and the tool's two pages under `docs/` (`docs/CLI.md`'s `## nova-memory` and `docs/SPEC.md`'s `## nova-memory — membership as a lookup, never a scan`). The binary was built in the staged checkout at 7acb90e18a764f0e728cd5ed701196a34405a824 with `go build -o $JOB/bin/nova-memory ./cmd/nova-memory` (never the installed binary) and used as `nova-memory v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6` on a Linux bench (`<bench>`): an invented orchard ledger of markdown notes in three classes (`orchard/`, `field/`, root files) with frontmatter, relative links, a `[[wikilink]]` to nothing, a note in a second root, a note whose opening `---` block is a thematic break, a dotfile, a long paragraph, a 10 MiB file over the cap, a thin note with no indexable paragraph, an empty note, a draft, a gold set and pin files, with a made-up actor `boss`. Every verb ran at least once with its real flags against the scratch corpus, the refusals too; the corpus md5 manifest is identical before and after the whole pass (the only new file was the runner's own stdin candidate), so nothing was written.

## Findings

1. `nova-memory verify --root ./corpus --links info --coverage 'orchard/*.md:orchard/*.md'`
   Printed:
   ```
   VERIFY INFO wikilink: [[bloom-watch]] resolves to no file (e.g. from field/2025-04-02.md)
   VERIFY INFO wikilink: [[missing-thing]] resolves to no file (e.g. from orchard/index.md)
   VERIFY OK gating=0 info=2 shown=2 coverage=0 frontmatter=0 links=info
   ```
   I expected the run to fail: `orchard/isolated.md` matches glob A (`orchard/*.md`) and its stem `isolated` is named in no file matching glob B (`orchard/*.md`), which is what the flag help (`every file matching glob A is named in some file matching glob B`) and the page both say. The control with a disjoint B side fails — `VERIFY FAIL coverage orchard/isolated.md: stem "isolated" appears in no file matching orchard/index.md` — so the check only works because an A-side file that is also a B-side file is skipped: with one glob for both sides every A file is skipped and `coverage` can never fail.
   Grade: URGENT (a green coverage check over a real orphan, the wrong result the verb exists to catch)

2. `nova-memory verify --root ./corpus --links info --frontmatter 'orchard/*.md' --fail-max 1 --json`
   Printed:
   ```
   {"result":{"verb":"verify","status":"failed","exit":1},"facts":{"gating":3,"info":2,"coverage":0,"frontmatter":3,"links":"info"},"items":[{"kind":"wikilink","fields":{"gating":false,"detail":"[[bloom-watch]] resolves to no file (e.g. from field/2025-04-02.md)"}},{"kind":"frontmatter","fields":{"gating":true,"detail":"orchard/bees.md: no name: in frontmatter"}}],"more":[{"kind":"wikilink","shown":1,"total":2,"remedy":"--max <n> raises the ceiling, --max 0 lists all"},{"kind":"frontmatter","shown":1,"total":3,"remedy":"--max <n> raises the ceiling, --max 0 lists all"}]}
   (one line printed)
   ```
   I expected the `more` entries' `remedy` to name `--fail-max`, the flag the tool has: the typed rendering of the same elision says `--fail-max <n> raises the ceiling, --fail-max 0 prints every finding`. `--max` is not a flag of `verify` or `eval` — `nova-memory verify --root ./corpus --links info --coverage 'orchard/*.md:orchard/*.md' --max 5` prints `VERIFY REFUSED: unknown flag --max; the flags of verify are --coverage, --exclude, --exempt, --fail-max, --frontmatter, --json, --links, --root` — and `eval`'s JSON `more` carries the same `--max` string. A machine consumer reading the JSON rendering is handed a remedy that is itself refused.
   Grade: URGENT (the two renderings of one result disagree and the machine rendering names a flag that does not exist)

3. `nova-memory verify --root ./corpus --root ./cairn --links gate`
   Printed:
   ```
   VERIFY REFUSED: --root names exactly one tree for verification, but 2 were given; run: nova-memory help
   (one line printed)
   ```
   I expected `verify` to accept the second `--root` as its own help says it does: `verify -h` lists `--root <value>  corpus root directory, repeatable (required)`. The page says the opposite (verify takes exactly one root, because its coverage and frontmatter globs and link resolution walk one tree), so the help line is the half that is wrong; the refusal also does not say to drop the extra root.
   Grade: NEXT (unclear help: one -h flag line promises repeatable while the verb and the page allow one root)

4. `nova-memory stats --root ./corpus --exclude ./orchard`
   Printed:
   ```
   STATS OK schema=nova-memory/2 files=14 chunks=21 bytes=24952 vocab=191 avg-terms=160.6 build=3.592946ms
   STATS OK class=. chunks=9
   STATS OK class=field chunks=4
   ```
   I expected `files=9 chunks=13`, which `--exclude orchard` prints. `--exclude ./orchard` and `--exclude orchard/` silently exclude nothing, and an `--exclude` that matches nothing (`--exclude no-such-dir`) is accepted silently while a `--coverage` or `--frontmatter` glob that matches nothing is refused as `a broken check, not a pass`. The help says `path or glob to skip` and never says the path is matched without normalisation, so a reader's ordinary relative path quietly leaves the scope it meant to narrow.
   Grade: NEXT (friction: a normalised and an unnormalised path are treated as different exclusions)

5. `nova-memory stats --root ./corpus --root ./corpus`
   Printed:
   ```
   STATS OK schema=nova-memory/2 files=28 chunks=42 bytes=49904 vocab=191 avg-terms=160.6 build=6.763193ms
   STATS OK class=. chunks=18
   STATS OK class=field chunks=8
   ```
   I expected one receipt per memory, or a refusal. The same tree named twice is counted twice (14 files become 28, 21 chunks become 42) and the `SEARCH OK` line over the same doubled roots reports `files=28 chunks=42` while returning only the 4 distinct hits, so the totals describe an index the reader did not ask for. `verify` refuses two `--root`s and `boot` refuses a duplicate entry for exactly this reason (`double-counted bytes are a lie`), so `--root` is the one place a duplicate is silent.
   Grade: NEXT (friction: a repeated --root silently doubles the measured corpus)

6. `nova-memory boot --root ./corpus --pin ./corpus/pin.txt --pin ./corpus/pin2.txt`
   Printed:
   ```
   BOOT OK files=2 bytes=485
   (one line printed)
   ```
   I expected five files (three from `pin.txt`, two from `pin2.txt`), or a refusal naming the repeated flag. The second `--pin` silently wins and the first is never read. A duplicated entry inside one pin file is refused (`BOOT REFUSED: pin entry "GUIDE.md" appears twice; double-counted bytes are a lie`), but a duplicated `--pin` is not, and the page says a boot that silently skipped a named memory is the exact failure this verb exists to remove.
   Grade: NEXT (friction: a repeated --pin silently drops a whole pin file)

7. `nova-memory frobnicate`
   Printed:
   ```
   MEMORY REFUSED: unknown verb "frobnicate"; the verbs are quickstart, stats, search, check, verify, eval, boot, version; run: nova-memory help
   (one line printed)
   ```
   I expected the one-line refusal grammar to lead with the tool name, `nova-memory REFUSED`, the way the same line's remedy does (`run: nova-memory help`). The dispatch refusals lead with `MEMORY` (and `version --json` answers `VERSION REFUSED: takes no flags and no arguments, got 1`), so a script or reader that greps `nova-memory REFUSED` for a failed invocation finds nothing; the bare command is the same.
   Grade: NEXT (unclear help: the dispatch refusal names a tool the house grammar never prints)

8. `nova-memory check --root ./corpus --channels bm25 --k 3 missing.md`
   Printed:
   ```
   MEMORY REFUSED: open missing.md: no such file or directory; run: nova-memory help
   (one line printed)
   ```
   I expected the refusal to name the input and what it wants — a readable markdown file — as every other refusal in this tool does (`--root ./no-such-dir is not a readable directory`). The same raw `open ...: no such file or directory` with only `run: nova-memory help` is what `eval <gold>` and `boot --pin <file>` print for their missing files, so three verbs hand back a Go error string instead of a remedy.
   Grade: NEXT (friction: three verbs answer a missing input with a raw Go error and no breadcrumb)

9. `nova-memory search --root ./corpus --channels bm25 --channels trigram --k 3 dormant`
   Printed:
   ```
   SEARCH OK hits=3 k=3 channels=trigram files=14 chunks=21: query="dormant"
   SEARCH CAL score=0.10 score-channel=trigram probe=unrelated-control
   SEARCH HIT rank=1 score=0.05 score-channel=trigram fused=0.01667 class=. name=- type=- root=./corpus: draft.md:1 "The dormant oil goes on at green tip, before the scab season starts, and the north block is left until the soil drains."
   ```
   I expected a repeated `--channels` to be refused, or to mean both lists together. The second flag silently replaces the first, so the run is `trigram` alone while the caller typed `bm25` too. The page refuses an empty entry in `--channels` for the same reason (`silently running fewer channels than asked reports a number under a name that no longer describes it`), and a repeated flag has that same effect without the refusal.
   Grade: NEXT (friction: a repeated --channels silently runs a different retrieval than the caller named)

10. `nova-memory stats --root ./gitcorpus/.git`
   Printed:
   ```
   STATS OK schema=nova-memory/2 files=1 chunks=2 bytes=153 vocab=21 avg-terms=11.5 build=372.684µs
   STATS OK class=. chunks=2
   ```
   I expected a refusal, or the skip the page promises — `.git is never a corpus and is always skipped`. It is skipped when it sits under a named root (`stats --root ./gitcorpus` refuses with `no markdown files found`), but naming it as the root indexes it, so the promise holds only in one direction.
   Grade: NEXT (friction: naming .git as the root indexes the one tree the page says is never a corpus)

11. `nova-memory search --root ./corpus --channels bm25 --k 3 dormant oil apple scab`
   Printed:
   ```
   SEARCH OK hits=3 k=3 channels=bm25 files=14 chunks=21: query="dormant oil apple scab"
   SEARCH CAL score=3.69 score-channel=bm25 probe=unrelated-control
   SEARCH HIT rank=1 score=10.55 score-channel=bm25 fused=0.01667 class=orchard name=spray-log type=measured root=./corpus: orchard/spray.md:8 "Dormant oil sprayed on the apple rows on 2 April, at the first sign of green tip; scab lesions were counted after the ra…"
   ```
   I expected the grammar block in `docs/SPEC.md` to match the binary. It reads `SEARCH OK query=<q> hits=<n> k=<n> channels=<list> files=<n> chunks=<n>`, but the binary — and the transcript in `docs/CLI.md` — print `hits=` first and the caller's query last, quoted, after the `: ` that closes the typed fields. The grammar a reader copies from is the one page line that is wrong.
   Grade: NEXT (unclear help: one spec grammar block puts the fields in an order the binary never prints)

12. `nova-memory quickstart --root ./corpus --words dormant scab`
   Printed:
   ```
   QUICKSTART REFUSED: unexpected argument "scab"; the words for the search go after --words; run: nova-memory help
   (one line printed)
   ```
   I expected the remedy to say that `--words` takes one word and must be repeated (`--words dormant --words scab`): the words already stood after `--words`, so `the words for the search go after --words` tells the reader to repeat the call that just failed. The usage says `[--words <w>]...`, but the refusal does not.
   Grade: NEXT (unclear help: a refusal tells the reader to do the thing that just failed)

## What held

All eight verbs ran at least once with their real flags, and none had to be left unrun. `version` printed the one identity line and `--version` the same. `stats` measured the corpus and refused a file root, a file over the 8 MiB cap by name, and a corpus with no indexable paragraph. `search` returned k receipts with the live calibration band, a `MISS` that says in words why nothing matched, and a `--whole` paragraph, and refused a missing flag, a zero and a non-integer `--k`, an unknown channel, an empty channel entry, a missing query and an unreadable root. `check` read a file and stdin, handed back receipts and never judged. `quickstart` ran its three steps, printing each command line it ran, and stopped at the step that could not run. `verify` refused a non-gate `--links`, a coverage side that matched nothing, a selector left with no files by `--exclude`, and a run with no gating check at all, and capped each finding kind separately while printing the uncapped totals. `eval` refused a floor of 0 and of 1.5 and a run with no gold file, then passed at the floor and failed below it as promised. `boot` refused every bad pin the page names (missing, non-canonical, absolute, escaping `--root`, duplicated inside one file, empty, a directory, and a pin naming nothing) and loaded exactly the named files.

READ 7/10 — the banner, the verb helps and the two pages state the no-defaults law, the refusal grammar, the calibration band and the exit table precisely and mostly truly, and every refusal I met named a remedy; the coverage exemption the page never states, the JSON remedy that names a flag the tool refuses, and `verify`'s own repeatable `--root` line keep it off a higher score.
USE 7/10 — every verb ran first try for real against a scratch corpus, the receipts are rich and the refusals are unambiguous, but a coverage check can pass over a real orphan, a repeated flag silently drops work, and the JSON remedy sends a reader to an unknown flag.
urgent=2 next=10
