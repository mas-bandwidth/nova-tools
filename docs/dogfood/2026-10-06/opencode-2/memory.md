# nova-memory dogfood — opencode-2 (zhi), 2026-10-06

Read cold, as a stranger: only `nova-memory -h`, `nova-memory help`, every
verb's `-h` and `help <verb>`, and the tool's pages under `docs/`
(`docs/CLI.md`'s `## nova-memory` and `docs/SPEC.md`'s `## nova-memory —
membership as a lookup, never a scan`). Built from the staged checkout at
abb9bfecc72930ef36ce116ac2f652b0e85ad044 and used as
`nova-memory v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6` on a
Linux bench: an invented corpus of markdown notes in three classes
(`notes/`, `log/`, root files) with frontmatter, links, a broken frontmatter
block, a dotfile, a second root, a draft, a gold set and pin files. Every verb
ran at least once with its real flags, the refusals too; no file was changed
(the corpus md5s before and after the whole pass are identical) and repeated
runs printed identical bytes.

## Findings

1. `nova-memory verify --root ./scratch/corpus --links info --coverage 'notes/*.md:notes/*.md'`

   ```
   VERIFY INFO wikilink: [[storm-glass]] resolves to no file (e.g. from log/1974-03-11.md)
   VERIFY OK gating=0 info=1 shown=1 coverage=0 frontmatter=0 links=info
   ```

   `corpus/notes/orphan.md` and `corpus/notes/broken.md` are named in no file
   under `notes/`, so by the documented rule — "every file matching glob A is
   named in some file matching glob B" — both are orphans and the check must
   fail. It passes because a file that is on both the A and the B side is
   skipped on the A side; with one glob for both sides every A file is skipped
   and `coverage` can never fail. The control with disjoint sides fails as it
   should:

   ```
   $ nova-memory verify --root ./scratch/corpus --links info --coverage 'notes/orphan.md:notes/index-notes.md'
   VERIFY INFO wikilink: [[storm-glass]] resolves to no file (e.g. from log/1974-03-11.md)
   VERIFY FAIL coverage notes/orphan.md: stem "orphan" appears in no file matching notes/index-notes.md
   VERIFY FAIL gating=1 shown=1 info=1 coverage=1 frontmatter=0 links=info
   ```

   Neither `verify -h` (`--coverage … every file matching glob A is named in
   some file matching glob B`) nor the page states the self-exemption, so the
   natural "every note is named somewhere in the notes tree" invocation is
   green over a real orphan. Grade: URGENT.

2. `nova-memory verify --root ./scratch/many --links gate --fail-max 1 --json`

   ```
   {"result":{"verb":"verify","status":"failed","exit":1},"facts":{"gating":25,"info":0,"coverage":0,"frontmatter":0,"links":"gate"},"items":[{"kind":"wikilink","fields":{"gating":true,"detail":"[[missing-1]] resolves to no file (e.g. from note1.md)"}}],"more":[{"kind":"wikilink","shown":1,"total":25,"remedy":"--max <n> raises the ceiling, --max 0 lists all"}]}
   ```

   The same run without `--json` prints the remedy against the flag that
   exists:

   ```
   VERIFY FAIL wikilink [[missing-1]] resolves to no file (e.g. from note1.md)
   VERIFY MORE kind=wikilink shown=1 total=25 --fail-max <n> raises the ceiling, --fail-max 0 prints every finding
   VERIFY FAIL gating=25 shown=1 info=0 coverage=0 frontmatter=0 links=gate
   ```

   `--max` is not a flag of `verify` nor of `eval` (whose JSON `more` carries
   the same string); pasting it is refused:

   ```
   $ nova-memory verify --root ./scratch/many --links gate --fail-max 1 --max 5
   VERIFY REFUSED: unknown flag --max; the flags of verify are --coverage, --exclude, --exempt, --fail-max, --frontmatter, --json, --links, --root; run: nova-memory verify -h
   ```

   The two renderings of one result disagree, and the JSON remedy — the one a
   machine consumer reads — names a flag the tool does not have. Grade:
   URGENT.

3. `nova-memory stats --root ./scratch/corpus --exclude ./notes`

   ```
   STATS OK schema=nova-memory/2 files=8 chunks=12 bytes=1394 vocab=137 avg-terms=17.3 build=883.164µs
   STATS OK class=. chunks=3
   STATS OK class=log chunks=2
   ```

   Expected `files=3`, which is what `--exclude notes` prints; `--exclude
   ./notes` and `--exclude notes/` silently exclude nothing, and an
   `--exclude` that matches nothing (`--exclude no-such-dir`) is accepted
   silently while a `--coverage` or `--frontmatter` glob that matches nothing
   is refused as "a broken check, not a pass". The help says "path or glob to
   skip" and never says the path is matched without normalisation, so a
   reader's ordinary relative path quietly widens the scope it meant to
   narrow. Grade: NEXT.

4. `nova-memory stats --root ./scratch/corpus --root ./scratch/corpus`

   ```
   STATS OK schema=nova-memory/2 files=16 chunks=24 bytes=2788 vocab=137 avg-terms=17.3 build=1.643736ms
   ```

   The same tree named twice is counted twice, and a query over overlapping
   roots returns the same memory twice in one ranking:

   ```
   $ nova-memory search --root ./scratch/corpus --root ./scratch/corpus/notes --channels bm25 --k 5 lantern
   SEARCH OK hits=5 k=5 channels=bm25 files=13 chunks=19: query="lantern"
   SEARCH HIT rank=2 score=1.13 … root=./scratch/corpus: notes/index-notes.md:4 "…"
   SEARCH HIT rank=3 score=1.13 … root=./scratch/corpus/notes: index-notes.md:4 "…"
   ```

   Expected one receipt per memory, or a refusal: `boot` refuses a duplicate
   pin entry for exactly this reason ("double-counted bytes are a lie"), and
   `verify` refuses two `--root`s, so `--root` is the one place a duplicate is
   silent. Grade: NEXT.

5. `nova-memory search --root ./scratch/corpus --channels bm25 --channels trigram --k 3 harbor`

   ```
   SEARCH OK hits=2 k=3 channels=trigram files=8 chunks=12: query="harbor"
   SEARCH CAL score=0.12 score-channel=trigram probe=unrelated-control
   SEARCH HIT rank=1 score=0.08 score-channel=trigram fused=0.01667 class=. name=- type=- root=./scratch/corpus: .hidden.md:1 "hidden note harbor: the inner beacon is lit by hand."
   ```

   I expected a repeated `--channels` to be refused or to mean both lists; the
   second flag silently replaces the first, so the run is `trigram` alone
   while the caller typed both. The page refuses an empty entry in `--channels`
   for exactly this reason — "silently running fewer channels than asked
   reports a number under a name that no longer describes it" — and a repeated
   flag has that same effect without the refusal. Grade: NEXT.

6. `nova-memory boot --root ./scratch/corpus --pin ./scratch/pin.txt --pin ./scratch/pin2.txt`

   ```
   BOOT OK files=1 bytes=125
   ```

   `pin.txt` names three memories and `pin2.txt` names one; expected four
   files, or a refusal naming the repeated flag, since the page says a boot
   that silently skipped a named memory is "the exact failure this verb exists
   to remove". The second pin wins and the first is never read; a duplicated
   entry inside one pin file is refused ("double-counted bytes are a lie") but
   a duplicated `--pin` is not. Grade: NEXT.

7. `nova-memory verify --root ./scratch/corpus10 --links info --coverage 'notes/my file.md:notes/index.md'`

   ```
   VERIFY FAIL coverage notes/my file.md: stem "my file" appears in no file matching notes/index.md
   VERIFY FAIL backlink notes/index.md links my%20file.md which does not exist (resolved notes/my%20file.md)
   VERIFY FAIL gating=2 shown=2 info=0 coverage=2 frontmatter=0 links=info
   ```

   `corpus10/notes/my file.md` exists and `corpus10/notes/index.md` names it
   with the URL-encoded form `[spaced](my%20file.md)`, the standard CommonMark
   way to write a destination holding a blank (the angle-bracket form
   `[spaced](<my file.md>)` resolves and passes). The link half resolves the
   target literally, so a link to a file that exists is reported dangling and
   the named file is reported unnamed in the same run. Expected `%20` decoded
   before resolution, or the page saying it is not. Grade: NEXT.

8. `nova-memory check --root ./scratch/corpus --channels bm25 --k 2 ./nothing.md`

   ```
   MEMORY REFUSED: open ./nothing.md: no such file or directory; run: nova-memory help
   ```

   `eval ./scratch/nope.tsv` and `boot --root ./scratch/corpus --pin
   ./scratch/does-not-exist.txt` print the same raw Go `open …: no such file
   or directory` with only `run: nova-memory help`, while every other refusal
   in the tool names the input and what it wants (compare `--root
   ./no-such-dir is not a readable directory`). Expected a remedy naming the
   input and its role — a readable markdown file, a readable gold file, a
   readable pin file. Grade: NEXT.

9. `nova-memory stats --root ./scratch/gitcorpus/.git`

   ```
   STATS OK schema=nova-memory/2 files=1 chunks=1 bytes=74 vocab=11 avg-terms=11.0 build=210.409µs
   STATS OK class=. chunks=1
   ```

   The page states "`.git` is never a corpus and is always skipped"; it is
   skipped when it sits under a named root (`stats --root ./scratch/gitcorpus`
   reports `files=1`, the visible note), but naming `.git` as the root indexes
   the markdown inside it. Expected the skip the page promises, or a refusal.
   Grade: NEXT.

10. `nova-memory search --root ./scratch/corpus --channels bm25 --k 3 lantern glazing brass`

    ```
    SEARCH OK hits=3 k=3 channels=bm25 files=8 chunks=12: query="lantern glazing brass"
    SEARCH CAL score=4.45 score-channel=bm25 probe=unrelated-control
    SEARCH HIT rank=1 score=3.80 score-channel=bm25 fused=0.01667 class=notes name=notes-index type=- root=./scratch/corpus: notes/index-notes.md:4 "…"
    ```

    `docs/SPEC.md`'s grammar for this line is `SEARCH OK query=<q> hits=<n>
    k=<n> channels=<list> files=<n> chunks=<n>` — the query first and unquoted
    — but the binary puts the query last, quoted, after the `: ` that closes
    the typed fields, which is what the same page's prose says and what
    `docs/CLI.md`'s transcript shows. The grammar block a reader copies from
    is the one page line that is wrong; the same page also calls the receipt
    address "the `file:para` address" while the binary and the block print
    `file:line`. Grade: NEXT.

11. `nova-memory quickstart --root ./scratch/corpus --json`

    ```
    {"result":{"verb":"quickstart","status":"ok","exit":0},"facts":{"root":"./scratch/corpus","steps":3,"channels":"bm25","k":"3/2","words-source":"corpus-top-terms","candidate":"corpus-first-paragraph","words":"glazing lantern note","demo":".hidden.md:1","done":3},"items":[ … ]}
    ```

    `facts.k` is the string `"3/2"`, while `search --json`, `check --json` and
    `eval --json` all print `k` as a number. A consumer of the one shape has
    to special-case the first-run envelope, and `"3/2"` names the two
    different budgets without saying which step each belongs to. Grade: NEXT.

## What the tool got right

- `help`, `-h`, every `<verb> -h` and every `help <verb>` answer on stdout at
  exit 0; a bare command names the door in one line at exit 2.
- The no-defaults law holds: missing `--root`, `--channels`, `--k`, `--links`,
  `--floor` and `--pin` are each one line plus a note saying what the flag
  wants; `--channels sema` names the two channels there are, a bad `--k`,
  `--floor 0` and `--floor 1.5` are each refused with the value's unit, and
  `--esclude` is answered with `did you mean --exclude?`.
- One run reports every independent problem, and a refusal names the input and
  the remedy.
- `verify` refuses a run that cannot fail (`--links info` with no
  `--coverage`/`--frontmatter`), refuses a coverage or frontmatter glob that
  matches nothing, and bounds each kind with its own `MORE` line and an
  uncapped total (`--fail-max 1` shows one of 25; `--fail-max 0` shows all).
- `eval` misses only, caps with `--fail-max`, passes a floor exactly equal to
  the measured recall, and refuses malformed, empty or zero-row gold files
  rather than skipping a row.
- `boot` checks the pin, not the directory: canonical paths, no escapes, no
  duplicates inside one pin, present and non-empty regular files, and the byte
  total.
- `quickstart` prints the argv that actually ran above each step, and its
  self-check on `/dev/null` ends the demonstration at the failing step with
  exit 2 instead of printing the closing note.
- Every verb is an inspection: the corpus's md5s are identical before and
  after the whole pass, and repeated runs print identical bytes except the
  labelled `build=` duration.

READ 7/10 — the banner, the verb helps and the page state the no-defaults law,
the refusal grammar, the calibration band and the exit table precisely and
mostly truly, and every refusal I met named its remedy; minus three for the
JSON remedy that names a flag the tool refuses, the `SEARCH OK` grammar block
that contradicts both the binary and `docs/CLI.md`, and the raw `open` errors
in three verbs.

USE 7/10 — every verb ran first try for real with nothing written, the receipts
are rich and the bounded `MORE` lines behave; minus three because a coverage
check can pass over a real orphan, a repeated `--pin` silently drops pinned
memories, and `--exclude ./notes` silently excludes nothing.

urgent=2 next=9
