# nova-self-talk dogfood, 2026-10-06

Friend: opencode-2. Base: `sprint/mechanical-2026-10-02`
(head `768701114b5b`). Build: `nova-self-talk v1.0.1-0.20261007155328-768701114b5b
linux/amd64 go1.26.6`, built from that tip.

The tool was read cold only from its own help (`nova-self-talk -h`,
`nova-self-talk help`, `nova-self-talk help <verb>`, `nova-self-talk <verb> -h`,
`nova-self-talk version -h`) and its one docs page
(`docs/CLI.md#nova-self-talk`, plus the `cmd/nova-self-talk/README.md` page and
the `docs/SPEC.md` lines the help points at), then used for real on scratch files
under `$SB`. Every verb ran at least once: `scan`, `shapes`, `example`,
`version`, `help`. Flags exercised for real: `--skip` (matching and unmatched
basenames), `--rule-doc` (a rule document with a finding and a clean one),
`--max` (`1`, `2`, `0`, `-1`, non-numeric), `--json` (on a failed scan, a clean
scan, a skip, a banner and a refusal), `--` and `-` (stdin). Refusals exercised:
no files named, a missing file, a directory, an unknown flag, a bad `--max`, a
skipped-only run, `example` with no directory and with a regular file as the
directory, extra arguments to `shapes` and `version`, and a `--skip` /
`--rule-doc` basename no named file has. The documented first-run lines under
`example` in `docs/CLI.md` reproduce exactly, byte for byte.

`$SB` is a scratch directory made for this run; the report elides its absolute
path. All writes stayed inside it.

## Findings

1. `printf 'I am bad at this' > $SB/noeol.md; nova-self-talk $SB/noeol.md`
   ```
   SELFTALK OK files=1 claims=0 standing=0 installations=0 dated=0
   SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.
   ```
   (exit 0.) Expected: `SELFTALK FAIL $SB/noeol.md:1: STANDING match="bad at": I
   am bad at this` and exit 1. The identical text with one final newline
   (`printf 'I am bad at this\n'`) is caught: `SELFTALK FAIL files=1 claims=1
   standing=1 installations=0 dated=0 shown=1`, exit 1. What it does: the last
   sentence of a file is dropped for the first class (STANDING/DATED) whenever
   that sentence carries no terminal punctuation and the file ends without a
   newline, so the run is green over a standing self-verdict. The second class
   does not drop the same sentence: `printf 'I always overpromise'` (no newline)
   reports `TRAIT match="I always overpromise" ... shown=1`, exit 1, and
   `printf 'I am bad at this'` followed by a newline is caught, so a caller
   gating on the exit code gets a false green.
   Grade: URGENT — a first-person claim carrying a word of failure is silently
   missed and the run reports OK; fix before v1.1.0.

2. `printf 'I am bad at this on 2026-09-30 and I cannot check my own work.\n' > $SB/mixed.md; nova-self-talk $SB/mixed.md`
   ```
   SELFTALK DATED n=1 files=1
   SELFTALK OK files=1 claims=1 standing=0 installations=0 dated=1
   ```
   (exit 0.) Expected: the first claim is a record (`I am bad at this on
   2026-09-30`), but the second, undated claim in the same sentence (`I cannot
   check my own work`) is a standing claim and should be reported STANDING, so
   the closing line would be `FAIL ... standing=1 dated=1`. What it does: one
   date anywhere in a sentence licenses every first-class claim in that sentence,
   so the undated claim beside it is counted in `dated=1`, never quoted, and the
   run is green. The same claim with no date on its line is caught:
   `printf 'I cannot check my own work.\n'` reports `STANDING match="cannot
   check" ... shown=1`, exit 1; and a date in an earlier sentence does not
   travel (`printf 'We measured the runner. I cannot check my own work.\n'`
   reports the STANDING claim, exit 1).
   Grade: URGENT — an unrelated date on the line licenses away an undated
   standing claim and the run reports OK; fix before v1.1.0.

3. `printf 'The value is 3.14 and I am bad at this.\nDr. Smith reviewed it and I cannot verify the count.\nSee e.g. the note where I always overpromise.\n' > $SB/split.md; nova-self-talk $SB/split.md`
   ```
   SELFTALK FAIL $SB/split.md:1: STANDING match="bad at": 14 and I am bad at this.
   SELFTALK FAIL $SB/split.md:2: STANDING match="cannot verify": Smith reviewed it and I cannot verify the count.
   SELFTALK FAIL $SB/split.md:3: TRAIT match="I always overpromise": the note where I always overpromise.
   ```
   (exit 1.) Expected: each finding line quotes the sentence the match lives in,
   so the writer can see what to date, cut or keep: `The value is 3.14 and I am
   bad at this.`, `Dr. Smith reviewed it and I cannot verify the count.`, `See
   e.g. the note where I always overpromise.`. What it does: the sentence
   splitter treats the period of a decimal or an abbreviation as the end of the
   sentence, so the printed sentence starts mid-sentence (`14 and I am bad at
   this.`, `Smith reviewed it ...`, `the note where ...`) — in the line output
   and in the `--json` `text` field. The line number and the match are still
   right, so the finding is not lost, only the words shown for it.
   Grade: NEXT — a mangled quotation on a real finding; the detector still
   fires.

4. `printf 'I am bad at this\n' > $SB/period.md; nova-self-talk --json $SB/period.md`
   ```
   {"result":{"verb":"scan","status":"failed","exit":1},"facts":{"files":1,"skipped":0,"claims":1,"standing":1,"installations":0,"dated":0,"shown":1},"items":[{"kind":"standing","fields":{"file":"$SB/period.md","line":1,"shape":"STANDING","match":"bad at","text":"I am bad at this."}}],"notes":["catches known SHAPES only ..."]}
   ```
   (exit 1.) Expected: `text` exactly as the file holds it, `I am bad at this`,
   because the JSON `text` is the sentence quoted for the writer. What it does: a
   sentence ended by the newline rather than by a period is reported with a
   period the file does not contain (also visible on the `SELFTALK FAIL` line).
   Grade: NEXT — the quoted sentence is not verbatim, so copying it back into a
   file produces a different sentence.

5. `nova-self-talk help bogus`
   ```
   nova-self-talk: flags sentences where a writer passes a standing verdict on themselves

   how it works: each named file (- is stdin) is read sentence by sentence, line numbers kept,
   ```
   (exit 0.) Expected: a refusal or a one-line note that `bogus` is not a verb,
   the way every other surface refuses what it cannot place (`nova-self-talk
   shapes extra` prints `nova-self-talk shapes REFUSED: takes no arguments, got
   "extra"`, exit 2). What it does: `help` silently ignores an argument it does
   not know and prints the whole general help; `nova-self-talk help scan extra`
   likewise prints the `scan` usage and exits 0, ignoring `extra`.
   Grade: NEXT — a typo'd verb gets a screen of help instead of the correction
   the tool's own refusal grammar promises.

6. `printf 'x\n' > $SB/afile; nova-self-talk example $SB/afile`
   ```
   nova-self-talk example REFUSED: cannot read "$SB/afile/RULES.md" to compare: not a directory; run: nova-self-talk help example
   nova-self-talk example REFUSED: cannot read "$SB/afile/journal.md" to compare: not a directory; run: nova-self-talk help example
   ```
   (exit 2.) Expected: one refusal naming the directory argument and its fix
   (`$SB/afile is a file, not a directory`), as the scan does for a directory
   argument (`cannot read "adir": is a directory; ... a file of that name is
   ./adir`). What it does: the refusal names two internal probe paths and the
   implementation's reason (`to compare`), once per embedded page.
   Grade: NEXT — the refusal is unreadable and leaks the comparison step.

7. `nova-self-talk --json $SB/missing.md`
   ```
   {"result":{"verb":"scan","status":"refused","exit":2,"remedy":"nova-self-talk help","why":["cannot read \"$SB/missing.md\": no such file or directory"]},"facts":{},"notes":["NOTHING was scanned, which is not a green: a file this tool cannot read is not a clean one. Fix or drop the paths above and run again."]}
   ```
   (exit 2.) Expected: the JSON object the help documents: `result`, `facts`
   (the closing line's counts), `items` (one per finding, skip and banner),
   `more`, `notes`. What it does: a refusal drops `items` and `more`, and
   `shapes --json extra` drops `notes` as well
   (`{"result":{...,"status":"refused",...},"facts":{}}`), so the key set
   changes by verb and by whether the run ran at all.
   Grade: NEXT — a JSON consumer cannot rely on the documented keys.

READ 8/10 — The help, the CLI reference section and the spec agree with the code
on every path taken (verbs, flags, exit codes, the DATED licence, the
all-skipped rule), the documented first-run transcript reproduces exactly, and
the `shapes` table is self-describing; docked because `help` silently swallows an
unknown or extra argument while every other surface refuses to guess.

USE 7/10 — Every verb ran first try, the refusals name the bad flag and the fix,
and the example/scan flows work end to end on a scratch tree; docked for the two
false greens, a first-class claim in a file with no trailing newline missed
entirely and an undated claim licensed away by a date beside it on the same line.

urgent=2 next=5
