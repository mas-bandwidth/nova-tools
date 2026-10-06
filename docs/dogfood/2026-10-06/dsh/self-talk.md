# nova-self-talk dogfood — dsh (zhi), 2026-10-06

Read as a stranger: only `nova-self-talk -h`, `nova-self-talk help`, `nova-self-talk help <verb>`
and the tool's pages under `docs/` (CLI.md#nova-self-talk, USAGE.md#nova-self-talk). Built on
the bench from the staged checkout at
6ec8bb02edc83283630f2e10e21bd035b3336f65 (`go build ./cmd/nova-self-talk`; `make build`
compiles the tree and stamps no version) and used as
`nova-self-talk devel linux/amd64 go1.26.6`: the banner's `example:` block lines in order in
a scratch dir, every verb once with its real flags, the refusals and the boundaries (`--max`
per class, `--max 0`, `--json`, stdin, an all-skipped run, a directory, dash-prefixed
filenames, flags after files, an unreadable file, a rerun of `example`) too, then real prose
(the repo's own README.md and docs/USAGE.md). About 26 minutes of use.

## Findings

1. `nova-self-talk help bogus` — a verb name `help` does not know is answered with silence:
   ```
   nova-self-talk: flags sentences where a writer passes a standing verdict on themselves

   how it works: each named file (- is stdin) is read sentence by sentence, line numbers kept,
   ```
   It prints the whole banner and exits 0, exactly as plain `help`; I expected the unknown
   name to be refused the way the tool refuses everything else unknown — `unknown flag -bogus;
   the flags are --skip, --rule-doc, --max, --json` and `it is not a verb either (the verbs
   are scan, shapes, example, version, help ...)` both name what was wrong and what there is —
   so a typo'd verb reads as help for it.
   Grade: NEXT.

2. `nova-self-talk version --json` refuses where the rest of the set renders:
   ```
   nova-self-talk version REFUSED: takes no flags and no arguments, got 1; run: nova-self-talk version -h
   ```
   I expected `--json` on every verb (scan, shapes and example all take it and render the same
   run one object); version's own help lists no flags, so the help does not lie, but a reader
   scripting the set's one shape hits the refusal.
   Grade: NEXT.

3. `nova-self-talk ./pages` — naming a directory gets the generic door:
   ```
   nova-self-talk REFUSED: cannot read "./pages": is a directory; run: nova-self-talk help
     NOTHING was scanned, which is not a green: a file this tool cannot read is not a clean one. Fix or drop the paths above and run again.
   ```
   I expected the remedy the no-files refusal gives a step away (`a shell glob is the usual
   first run (nova-self-talk memory/*.md)`); naming a directory is the natural first move
   after `example ./pages`, and the truest remedy here is `pages/*.md`, not the help door.
   Grade: NEXT.

4. `nova-self-talk --max 2 README.md docs/USAGE.md` (from the repo root) — the second class
   fires on documentation prose:
   ```
   SELFTALK FAIL files=2 claims=0 standing=0 installations=1 dated=0 shown=1
   SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning.
   SELFTALK FAIL docs/USAGE.md:189: FORECLOSURE match="There is no quickstart: nothing here": There is no quickstart: nothing here has a default to guess.
   ```
   The sentence is about tools, not the writer, and the NOTE predicts it exactly (a true
   positive on the grammar, a false one on the meaning); I expected as much, and record it
   because a reader pointing the tool at documents rather than journals meets it on the first
   page, and there is no licence for documentation prose the way `--rule-doc` licences rule
   documents.
   Grade: NEXT.

5. `nova-self-talk ./pages/journal.md` (stdout and stderr merged) — the closing count line
   shares the finding prefix:
   ```
   SELFTALK DATED n=1 files=1
   SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2
   SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning.
   ```
   I expected to tell the summary from a finding on sight; the count line (stdout) and the
   finding lines `SELFTALK FAIL <file>:<line>` (stderr) both open `SELFTALK FAIL`, so a merged
   transcript reads the summary as one more finding — the streams are split as documented, and
   a reader who merges them is left knowing the grammar or nothing.
   Grade: NEXT.

6. `go build -o ../bin/nova-self-talk ./cmd/nova-self-talk && ../bin/nova-self-talk version`
   — a plain build has no identity:
   ```
   nova-self-talk devel linux/amd64 go1.26.6
   ```
   I expected the build identity to name the source; the repo's own `make build` compiles the
   tree and stamps nothing, so a stranger's binary says `devel` and a report citing it cannot
   tie it to a commit (this one: 6ec8bb02edc83283630f2e10e21bd035b3336f65, said by me, not
   the tool).
   Grade: NEXT.

## What the tool got right

- The first run needs nothing: `example` writes two pages from the binary itself, a rerun
  keeps them and says so (`kept=RULES.md,journal.md`), and `--dry-run` writes nothing while
  printing the plan.
- The banner's `example:` block runs as printed: all three lines exit 1 with findings,
  exactly as the banner says ("that is the tool working").
- The stream split is as documented and survives `2>/dev/null`: findings and `MORE` on
  stderr; `DATED`, the closing count, `SKIP` and `NOTE` on stdout.
- Every refusal names every problem at once with a runnable remedy: no files (with the glob
  hint), an unknown flag (with the flag list), a bare word that is no file told the verbs AND
  the `./name` escape, flags after files, `--` for dash-prefixed names, and an unreadable
  file is never a clean one.
- `--max` bounds per class, its `MORE` line carries the true total and the widening flag,
  `--max 0` prints all, and the closing line keeps the totals whichever way the run went.
- The all-skipped run exits 0 with `SELFTALK SKIP files=0 skipped=1 reason=all-skipped`,
  never OK, and the docs name the caller's gate (`files>0`).
- `--json` prints the same run as one object — findings, the `RULEDOC` banner as an item, the
  notes — keeps the exit code, and renders refusals too (`status":"refused"`, `why`).
- A dated claim is counted (`DATED n=1`), never quoted, across all nine shapes I tried;
  `--skip` and `--rule-doc` names no named file has are said on NOTE lines.
- `shapes` prints the detector table (`rows=22`) with a `finds` and a `passes` sentence per
  row, and `help` / `help <verb>` / `<verb> -h` all exit 0: help is never a refusal.

READ 9/10 — the banner answers what it does, how it works, the first run, both classes, every
line's stream and the exit table before any use, and the honest limit prints on every run; the
silent unknown-verb `help` is all that keeps it off a 10.

USE 9/10 — a stranger runs the whole first run from an empty scratch dir with no store, every
verb with its real flags, and every refusal carries its remedy; the directory remedy, the
shared FAIL prefix and the `devel` build identity are the small stumbles.

urgent=0 next=6
