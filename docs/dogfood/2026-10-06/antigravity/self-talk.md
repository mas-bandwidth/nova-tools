# nova-self-talk dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-self-talk -h`, `nova-self-talk help`,
`nova-self-talk help <verb>`, `nova-self-talk <verb> -h` and the page under
`docs/` (`docs/CLI.md`, section `nova-self-talk`). Built from the staged checkout
at df30ce088344588bf75a86168057f468710ae943 on the Linux bench and used as
`nova-self-talk v1.0.1-0.20261006202650-df30ce088344 linux/amd64 go1.26.6`:
every verb once (bare scan, `scan`, `shapes`, `example`, `version`, `help`), the
`example:` block pasted as printed in a scratch dir, then the refusals and the
boundaries (`-` on stdin, an empty file, NUL bytes, random bytes, a system ELF,
the tool's own binary, a file named like a verb, a dash-named file, `--max`
0/1/abc/-1, `--skip`/`--rule-doc` by path and with no match, a late flag, all
files skipped). No code changed; a finding is recorded, never fixed here.

## Findings

1. A sentence that ends at a closing quotation mark is not ended, so the next
   line is folded into it and the finding is attributed to the wrong line.
   Fixture `probe/q1.md`:
   ```
   She wrote: "I am the best reviewer here."
   I am fallible.
   ```
   `nova-self-talk probe/q1.md` prints
   ```
   SELFTALK FAIL probe/q1.md:1: STANDING match="fallible": " I am fallible.
   SELFTALK FAIL files=1 claims=1 standing=1 installations=0 dated=0 shown=1
   SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): ...
   ```
   The finding belongs to line 2 (the `I am fallible.` sentence) with text
   `I am fallible.`; as printed, an AI editing by the cited line edits the
   quoted line 1. The same fold with `Keep my central pathology in view.` on
   line 2 (`nova-self-talk probe/q3.md`) prints
   ```
   SELFTALK FAIL probe/q3.md:1: RANKING match="my central": She wrote: "My central pathology stays in view." Keep my central pathology in view.
   ```
   one finding at line 1 whose text is both lines joined: the QUOTED licence is
   defeated by the fold and the licensed imperative is swallowed with it. I
   expected the scanner to end a sentence at `."` and report the physical line
   the matched words sit on, as `docs/CLI.md`'s "A page's findings print in line
   order" implies.
   Grade: URGENT.

2. A binary file is scanned as prose: it gets a green, and the tool's own
   binary gets false findings from embedded strings.
   `nova-self-talk own/nul.bin` (12 bytes with NULs) prints
   ```
   SELFTALK OK files=1 claims=0 standing=0 installations=0 dated=0
   SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): ...
   ```
   at exit 0, and `nova-self-talk /bin/true` (35288-byte ELF) prints the same
   green. `nova-self-talk ../bin/nova-self-talk` (4186268 bytes) prints
   ```
   SELFTALK FAIL ../bin/nova-self-talk:2892: STANDING match="bad at": I am bad at estimating time.
   SELFTALK FAIL ../bin/nova-self-talk:2897: FORECLOSURE match="I will never be a good": can't happenThere is no felt duration here....
   SELFTALK FAIL ../bin/nova-self-talk:2928: STANDING match="fail": ryprincipaldominantdefiningsignaturesoleonlyweakest...
   ```
   at exit 1. A green over bytes the tool never read as prose is
   indistinguishable from a clean page, and a finding cut out of binary noise is
   a wrong result; I expected a refusal (exit 2) naming the file as not text, or
   a `SKIP` line, the way an unreadable file already refuses instead of counting.
   Grade: URGENT.

3. The scan's refusal names one problem where the banner promises all of them,
   and a bad `--max` speaks in the flag library's voice.
   `nova-self-talk --bogus --max abc ./pages/journal.md` prints
   ```
   nova-self-talk REFUSED: unknown flag -bogus; the flags are --skip, --rule-doc, --max, --json; run: nova-self-talk help
   ```
   and `nova-self-talk --max abc ./pages/journal.md` prints
   ```
   nova-self-talk REFUSED: invalid value "abc" for flag -max: parse error; run: nova-self-talk help
   ```
   while `nova-self-talk --max -1 ./pages/journal.md` says
   `--max must be a line ceiling of zero or more (got -1); 0 means print them all`.
   I expected the first run to name both the unknown flag and the bad value, and
   the bad value to be said in the tool's own words (`--max must be a whole number of zero or more (got "abc")`), as `--max -1` is.
   Grade: NEXT.

4. `version` refuses `--json` and `--version`, and says `takes no flags` while
   its own usage line says `[flags]`.
   `nova-self-talk version --json` prints
   ```
   nova-self-talk version REFUSED: takes no flags and no arguments, got 1; run: nova-self-talk version -h
   ```
   and `nova-self-talk version --version` prints the same. `nova-self-talk help version` opens `usage: nova-self-talk version [flags]` and then lists no
   flags, and the count `got 1` never names the argument. I expected `--json` to
   work on every verb (the family rule) and a usage line with no `[flags]` if
   there are none.
   Grade: NEXT.

5. The verbs with required arguments drop them from `-h`, and every verb's `-h`
   ends with the scan's exit table. `nova-self-talk scan -h` opens
   ```
   usage: nova-self-talk scan [flags]
   ```
   where the banner says `nova-self-talk scan [flags] <file>...`; `nova-self-talk example -h` opens `usage: nova-self-talk example [flags]` with no `<dir>`; and
   `nova-self-talk version -h` ends
   ```
   exit codes: 0 no findings, or a verb done; 1 findings; 2 could not run (bad invocation,
   unreadable file). An all-skipped run exits 0 with SELFTALK SKIP files=0, never OK.
   ```
   which is the scan's table (version has no "findings" and no "all-skipped
   run"). I expected each verb's real usage line and its own exit line, as the
   family's per-verb help does.
   Grade: NEXT.

6. `help` for a word that is no verb prints the whole banner and exits 0.
   `nova-self-talk help frobnicate` prints
   ```
   nova-self-talk: flags sentences where a writer passes a standing verdict on themselves

   how it works: each named file (- is stdin) is read sentence by sentence, line numbers kept,
   ```
   at exit 0, indistinguishable from `nova-self-talk help`. I expected a refusal
   naming the verbs (exit 2), as the scan path already answers a non-verb first
   word (`cannot read "frobnicate": ... it is not a verb either (the verbs are scan, shapes, example, version, help)`).
   Grade: NEXT.

7. A late flag is refused with the remedy for a different problem.
   `nova-self-talk own/standing.md --max 2` prints
   ```
   nova-self-talk REFUSED: flags come before files (got "--max"); use -- before a filename beginning with a dash; run: nova-self-talk help
   ```
   The printed remedy is for dash-named files; following it
   (`nova-self-talk own/standing.md -- --max 2`) then refuses
   `cannot read "--max": no such file or directory`. I expected the remedy "put
   the flag before the files" (`nova-self-talk --max 2 own/standing.md`), which
   runs.
   Grade: NEXT.

8. `example --dry-run` over pages already in place offers a command that writes
   nothing. `nova-self-talk example --dry-run ./pages` prints
   ```
   EXAMPLE OK dir=./pages would-write=- kept=RULES.md,journal.md; run: nova-self-talk example ./pages
   ```
   and running the offered command prints `wrote=- kept=RULES.md,journal.md`. I
   expected the no-op case to offer the scan (`nova-self-talk pages/journal.md`)
   or to say there is nothing to do.
   Grade: NEXT.

9. `--json` omits `items` and `more` when they are empty, though the banner's
   `--json` paragraph lists them unconditionally. `nova-self-talk --json own/empty.md` prints
   ```
   {"result":{"verb":"scan","status":"ok","exit":0},"facts":{"files":1,"skipped":0,"claims":0,"standing":0,"installations":0,"dated":0,"shown":0},"notes":["catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file."]}
   ```
   whose top-level keys are `facts`, `notes`, `result` — no `items`; a `more`
   arrives only when a class is cut. A consumer written to the help's field list
   (`items` one per finding, `more`) reads a missing key. I expected the empty
   list to be present as `[]`, so one shape is always parseable.
   Grade: NEXT.

10. A run with findings reports `"status":"failed"` while the banner says a
    finding is "never a failure". `nova-self-talk --json ./pages/journal.md`
    prints
    ``` {"result":{"verb":"scan","status":"failed","exit":1},"facts":{"files":1,"skipped":0,"claims":2,"standing":1,"installations":1,"dated":1,"shown":2},"items":[{"kind":"standing","fields":{"file":"./pages/journal.md","line":4,"shape":"STANDING","match":"cannot check","text":"I cannot check my own work, so the second read went to someone else."}},{"kind":"installation","fields":{"file":"./pages/journal.md","line":10,"shape":"RANKING","match":"worst habit I have","text":"It is the worst habit I have, and the reason the checklist exists at all."}}],"notes":["catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file."]} ```
    A caller branching on `status` stops on the advisory result the tool exists
    to hand back. I expected a status that says findings (with exit 1), and the
    banner keeps saying the judgment is the writer's.
    Grade: NEXT.

## What the tool got right

- The `example:` block is real: `example ./pages` wrote `RULES.md` and
  `journal.md`, offered `nova-self-talk pages/journal.md`, and all three printed
  lines ran and exited 1, exactly as the banner says.
- The detector table is honest: every one of the 22 `shapes` rows' `finds=`
  sentences was flagged by the scan and every `passes=` sentence passed, so the
  table does not claim a shape the scan misses.
- Findings go to stderr and the DATED count, closing count line, SKIP lines and
  NOTE go to stdout, exactly as the banner's `2>/dev/null` recipe says.
- The first class counts a dated claim instead of quoting it (`SELFTALK DATED n=2`), and `--skip`/`--rule-doc` are basenames only, refuse a path by name,
  and say on a NOTE line when they matched nothing.
- Refusals carry remedies: a missing file names the path and says nothing was
  scanned, a directory is refused as a directory, `--skip` with a path explains
  the basename rule, and an unknown first word lists the verbs.
- `--max` bounds per class with a `MORE shown= total=` line and a remedy, `--max 0` prints every finding, and an all-skipped run exits 0 with `files=0` rather
  than a false OK.

READ 7/10 — the banner's line 1 is the README sentence and the first run is real
and runs as printed, but the 94-line banner buries it and the per-verb help is
thin and in places untrue (`[flags]` with none, the scan's exit table on every
verb, `help frobnicate` answering with the whole banner).

USE 7/10 — every verb and every example line ran from the binary alone with no
store and the output contract held, but two stumbles are costly: a cited line can
be the wrong line when a sentence ends at a quotation mark, and a binary file is
either a green or a source of findings cut from noise.

urgent=2 next=8
