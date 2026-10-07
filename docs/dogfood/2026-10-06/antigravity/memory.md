# nova-memory dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-memory -h`, `nova-memory help`, every verb's `-h`
and `help <verb>`, and the tool's pages under `docs/` (`docs/CLI.md`'s
`## nova-memory` and `docs/SPEC.md`'s `## nova-memory — membership as a lookup, never a scan`). Built from the staged checkout at
7b132f109d92d8281b85f9c3ded24dddce2073ba and used as
`nova-memory v1.0.1-0.20261007013005-7b132f109d92 linux/amd64 go1.26.6` on the
Linux bench: an invented corpus of eight markdown files in four classes
(`notebook/`, `log/`, root files, a dotfile), a draft, a gold set, pins, and
the refusals. Every verb ran at least once with its real flags; nothing was
written (the corpus hashes before and after are identical) and repeated runs
printed identical bytes.

## Findings

1. `nova-memory verify --root ./corpus --links info --frontmatter 'notebook/*.md' --fail-max 1 --json`

   The one JSON line elides the findings and closes
   ```
   ... "more":[{"kind":"frontmatter","shown":1,"total":3,"remedy":"--max <n> raises the ceiling, --max 0 lists all"}]}
   ```
   I expected the remedy to name `--fail-max`, the flag the tool actually has:
   the typed rendering of the same elision says `--fail-max <n> raises the ceiling, --fail-max 0 prints every finding`, and the help lists `--fail-max`
   in every verb that has it. `--max` does not exist anywhere:
   `nova-memory search --root ./corpus --channels bm25 --k 3 --max 2 harbor`
   prints `SEARCH REFUSED: unknown flag --max; the flags of search are --channels, --exclude, --json, --k, --root; run: nova-memory search -h`.
   `eval`'s JSON has the same `--max` in its `MORE` remedy, so an AI consumer
   that reads the JSON rendering — the one the tool tells it to use — is handed
   a remedy that is itself refused. This is the two-renderings-must-not-drift
   rule broken in the direction a machine hits first.
   Grade: URGENT.

2. `nova-memory search --root ./corpus --channels bm25 --k 3 harbor lens cloth`

   ```
   SEARCH OK hits=3 k=3 channels=bm25 files=7 chunks=7: query="harbor lens cloth"
   SEARCH CAL score=1.67 score-channel=bm25 probe=unrelated-control
   SEARCH HIT rank=1 score=2.60 score-channel=bm25 fused=0.01667 class=notebook name=harbor-lights type=measured root=./corpus: notebook/harbor.md:5 "The harbor light uses a Fresnel lens ..."
   ```
   `docs/SPEC.md`'s grammar for this line is `SEARCH OK query=<q> hits=<n> k=<n> channels=<list> files=<n> chunks=<n>` — the query first and unquoted — but the
   binary puts the query last, quoted, after the `: ` that closes the typed
   fields, which is what the same page's prose says and what `docs/CLI.md`'s
   transcript shows. The grammar block a reader copies from is the one page
   line that is wrong.
   Grade: NEXT.

3. `nova-memory search --root ./corpus --channels bm25 --channels trigram --k 3 harbor`

   ```
   SEARCH OK hits=3 k=3 channels=trigram files=10 chunks=10: query="harbor"
   SEARCH CAL score=0.07 score-channel=trigram probe=unrelated-control
   SEARCH HIT rank=1 score=0.20 score-channel=trigram fused=0.01667 class=. name=- type=- root=./corpus: .hidden.md:1 "hidden dir note harbor"
   ```
   I expected a repeated `--channels` to be refused, or to mean the two lists
   together; the second flag silently replaces the first, so the run is `trigram`
   alone while the caller typed both. `docs/SPEC.md` refuses an empty entry in
   `--channels` for exactly this reason — "silently running fewer channels than
   asked reports a number under a name that no longer describes it" — and a
   repeated flag has that same effect without the refusal.
   Grade: NEXT.

4. `nova-memory quickstart --root ./corpus --words harbor tender --draft draft.md`

   ```
   QUICKSTART REFUSED: unexpected argument "tender"; the words for the search go after --words; run: nova-memory help
   ```
   I expected the remedy to say that `--words` takes one word and must be
   repeated, `--words harbor --words tender`: the words already stood after
   `--words`, so "go after --words" tells the reader to repeat the call that just
   failed. The usage says `[--words <w>]...`, but the refusal does not.
   Grade: NEXT.

5. `nova-memory check --root ./corpus --channels bm25 --k 3 missing-draft.md`

   ```
   MEMORY REFUSED: open missing-draft.md: no such file or directory; run: nova-memory help
   ```
   I expected the refusal to name the input and what it wants — a readable
   markdown file — as every other refusal in this tool does (compare `--root ./no-such-dir is not a readable directory`). The same raw `open ...: no such file or directory` with only `run: nova-memory help` is what `eval <gold>`
   and `boot --pin <file>` print for their missing files, so three verbs hand
   back a Go error string instead of a remedy.
   Grade: NEXT.

6. `nova-memory frobnicate`

   ```
   MEMORY REFUSED: unknown verb "frobnicate"; the verbs are quickstart, stats, search, check, verify, eval, boot, version; run: nova-memory help
   ```
   The dispatch refusals lead with `MEMORY` (and `version --json` answers with
   `VERSION REFUSED: takes no flags and no arguments, got 1`), not with the tool
   name the house one-line grammar uses (`<tool>[ <verb>] REFUSED`). A script or
   reader that greps `nova-memory REFUSED` for a failed invocation finds
   nothing, and the same run's `run:` remedy does name `nova-memory`, so the two
   halves of one line disagree about whose error it is.
   Grade: NEXT.

7. `nova-memory quickstart --root ./corpus --json`

   The one JSON object opens
   ```
   {"result":{"verb":"quickstart","status":"ok","exit":0},"facts":{"root":"./corpus","steps":3,"channels":"bm25","k":"3/2","words-source":"corpus-top-terms","candidate":"corpus-first-paragraph","words":"harbor tender lens","demo":"broken-frontmatter.md:4","done":3},"items":[ ... ]}
   ```
   `facts.k` is the string `"3/2"`, while `search --json`, `check --json` and
   `eval --json` all print `k` as a number. A consumer of the one shape has to
   special-case the first-run envelope, and `"3/2"` names the two different
   budgets without saying which step each belongs to.
   Grade: NEXT.

8. `nova-memory help`

   ```
   nova-memory: search your own markdown notes, and check a draft against what they already say

   how it works: each run reads the --root directories and builds its index in
   memory (bm25 words, trigrams); nothing is written. search prints the k best
   passages with file:line and the quoted text; check names the notes a draft
   repeats; verify gates links and frontmatter.
   ```
   I expected the summary of the one gating verb to name its third gate:
   `--coverage` is a failing check (a coverage run prints `VERIFY FAIL gating=2`
   and exits 1) and is the gate the tool's own first-run example uses
   (`verify --links info --coverage ...`). The banner instead names only links
   and frontmatter, so a reader skimming `help` does not learn that `verify`
   checks coverage at all.
   Grade: NEXT.

## What the tool got right

- `help`, `-h`, every `<verb> -h` and every `help <verb>` answer on stdout at
  exit 0; a bare command names the door in one line at exit 2.
- The no-defaults law holds: missing `--root`, `--channels`, `--k`, `--links`
  and `--floor` are each one line plus a note saying what the flag wants, and
  `--channels sema`, `--k 0`, `--floor 0`, `--floor 1.5` and a non-integer `--k`
  are each refused with the value's unit.
- The `setup:`/`example:` block is real: the four example lines run as printed,
  and `quickstart` on a fresh tree works with nothing but a directory of `.md`
  files.
- Every verb is an inspection. Nothing was written: the corpus's md5s are
  identical before and after the whole pass, and repeated `search` and `eval`
  runs print identical bytes.
- Output is bounded with its totals: `--fail-max 1` prints one finding, one
  `MORE kind=frontmatter shown=1 total=3` line and the uncapped
  `gating=3` summary; `search --k 100` returned the corpus's 6 hits, not 100.
- The documented refusals all fire: empty corpus, a draft with no paragraph of
  three terms, a 9 MiB file over the file cap, malformed and empty gold files,
  a `[[wikilink]]` to nothing, an exempt listing prefix, missing and
  non-canonical and duplicate pin entries.

READ 8/10 — the banner answers what it does, how it works and how to start, the
flags say what they want, and the exit table is explicit; the JSON remedy that
names a flag the tool refuses, the one wrong grammar block, and the summary that
drops `--coverage` are what keep it off a 9 or 10.

USE 8/10 — every verb ran from a plain directory with no store, the refusals
name a next step, and nothing was written; a repeated `--channels` that quietly
changes the answer, a `--words` remedy that repeats the failed call, and raw Go
`open` errors in three verbs are the stumbles.

urgent=1 next=7
