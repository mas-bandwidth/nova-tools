# nova-self-talk, used cold — 2026-10-06

Repo: mas-bandwidth/nova-tools at `sprint/mechanical-2026-10-02`
(34db18e07dff90aba3d2f8dd4b699e72e969bec8), binary
`nova-self-talk v1.0.1-0.20261007150632-34db18e07dff`.
Read first: `nova-self-talk`, `-h`, `help`, every `<verb> -h`, and the tool's
sections in docs/CLI.md and docs/USAGE.md. Used cold after that: every verb at
least once with its real flags on scratch markdown, the refusals too. No source
read before the report was written.

Each finding is the command as typed, the first lines it printed, what was
expected, and a grade.

1. **A FIFO or a device file is read, not refused: the scan never returns.**
   `mkfifo t/fifo.md; timeout 5 nova-self-talk t/fifo.md`
   printed: nothing at all; `timeout` killed the tool (exit 124). The same with
   `timeout 5 nova-self-talk /dev/zero`. With a writer on the FIFO the tool read
   it and flagged the sentence, so it is reading the FIFO, not refusing it.
   expected: `pkg/readregular` is the tree's one reader of a file a tool did
   not write — regular files only (links followed, a 16 MiB cap), with FIFOs,
   devices and sockets refused by name at exit 2 and a remedy. A FIFO in a shell
   glob hangs the tool with no deadline and no output; a device can stream
   without end. This is the one finding I would fix before v1.1.0.
   grade: URGENT

2. **No size cap: a large regular file is read and ground for minutes.**
   `head -c $((8*1048576)) /dev/zero | tr "\0" a > t/a8.md; time nova-self-talk t/a8.md`
   printed: `SELFTALK OK files=1 claims=0 standing=0 installations=0 dated=0`,
   `SELFTALK NOTE catches known SHAPES only ...`, `real 34.63 user 34.51`. A
   20 MiB file was still running after 30 s (`timeout 30`, exit 124).
   expected: a file over the tree's `readregular.DefaultMax` (16 MiB) is refused
   by name and cap at exit 2. The scan costs about 4.3 s of CPU per MiB even
   when it finds nothing, so the missing cap is minutes of CPU on one file — the
   same missing door as finding 1.
   grade: NEXT

3. **`version` refuses `--json`.**
   `nova-self-talk version --json`
   printed: `nova-self-talk version REFUSED: takes no flags and no arguments, got 1; run: nova-self-talk version -h` (exit 2).
   expected: the tree standard says every verb accepts `--json`. Failing that,
   `version -h` prints `usage: nova-self-talk version [flags]`, which offers
   flags the verb will refuse.
   grade: NEXT

4. **`help` with an unknown verb prints the whole banner and says nothing.**
   `nova-self-talk help nope`
   printed: `nova-self-talk: flags sentences where a writer passes a standing verdict on themselves`,
   blank, `how it works: each named file (- is stdin) is read sentence by sentence, line numbers kept,` (exit 0).
   expected: an unknown name is answered with the verbs there are and the
   nearest — at least a NOTE that `nope` is not one of `scan, shapes, example, version, help`. Silently printing the global banner reads as if `nope` were
   understood.
   grade: NEXT

5. **`--max` without a number, or with a typo, does not say what it wants.**
   `nova-self-talk --max x t/one.md`
   printed: `nova-self-talk REFUSED: invalid value "x" for flag -max: parse error; run: nova-self-talk help` (exit 2);
   `--max` alone prints `nova-self-talk REFUSED: flag needs an argument: -max; run: nova-self-talk help`.
   expected: the breadcrumb the same flag already carries for a negative number:
   `--max must be a line ceiling of zero or more (got -1); 0 means print them all`.
   A reader should not have to leave the refusal and open `help` to learn the
   unit and the zero case.
   grade: NEXT

6. **`--skip` silently overrides `--rule-doc` for the same basename.**
   `nova-self-talk --skip RULES.md --rule-doc RULES.md pages/RULES.md pages/journal.md`
   printed: `SELFTALK SKIP pages/RULES.md (--skip)`,
   `SELFTALK DATED n=1 files=1`,
   `SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2` (exit 1).
   expected: the rule-document banner prints, or a NOTE says the name was skipped
   so the banner never applied. The tool already notes a `--skip` or `--rule-doc`
   name that no named file has; the collision can be said in the same place.
   grade: NEXT

7. **The rule-document banner and its finding are on different streams.**
   `nova-self-talk --rule-doc RULES.md pages/RULES.md 2>/dev/null`
   printed: `SELFTALK RULEDOC pages/RULES.md: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule`,
   `SELFTALK FAIL files=1 claims=0 standing=0 installations=1 dated=0 shown=1`,
   `SELFTALK NOTE catches known SHAPES only ...`.
   expected: the banner is on stdout and the finding is on stderr. A reader told
   "findings print on stderr" who keeps stderr loses the one sentence that says
   the finding there is never a reason to soften a rule. Same stream, or the
   banner repeated with each finding it heads.
   grade: NEXT

8. **Merged streams put the closing count before the findings it counts.**
   `nova-self-talk t/many.md 2>&1 | cat`
   printed: `SELFTALK FAIL files=1 claims=0 standing=0 installations=4 dated=0 shown=4`,
   `SELFTALK NOTE catches known SHAPES only ...`,
   `SELFTALK FAIL t/many.md:1: RANKING match="I am the best": I am the best reviewer here.`
   On a terminal the four finding lines print before the count.
   expected: the help calls it "the closing count line"; under a pipe or a
   command substitution (how an AI reads it) stdout is flushed at exit after
   stderr and the count leads. Flush stdout as it writes so both readings agree.
   grade: NEXT

9. **One sentence, two findings, two counts.**
   `nova-self-talk --max 0 t/probe2.md`
   printed: `SELFTALK FAIL t/probe2.md:2: STANDING match="weakest": My recall is my weakest instrument.`,
   `SELFTALK FAIL t/probe2.md:2: VERDICT-IDIOM match="is my weakest": My recall is my weakest instrument.`,
   count line `SELFTALK FAIL files=1 claims=1 standing=1 installations=7 dated=0 shown=8`.
   expected: one finding for one sentence, or a NOTE that a sentence can be
   claimed by both classes. Here the first class reaches into a shape its own
   `shapes` table calls a self-superlative (RANKING), and the count line adds the
   sentence twice.
   grade: NEXT

10. **The same shape is first-class for `weakest` and not for `worst`.**
    `nova-self-talk --max 0 t/probe2.md`
    printed: `SELFTALK FAIL t/probe2.md:2: STANDING match="weakest": My recall is my weakest instrument.`
    and, for the next line, only
    `SELFTALK FAIL t/probe2.md:3: VERDICT-IDIOM match="is my worst": My central pathology is my worst trait.`
    expected: `my <noun> is my <superlative> <noun>` is one shape; `weakest` and
    `worst` are both in the STANDING word list and in the second class's
    descriptions, so two near-identical sentences should be treated the same. A
    reader cannot predict which one draws the extra finding.
    grade: NEXT

11. **`example` into a file prints internal child paths.**
    `nova-self-talk example t/one.md`
    printed: `nova-self-talk example REFUSED: cannot read "t/one.md/RULES.md" to compare: not a directory; run: nova-self-talk help example`,
    and the same for `t/one.md/journal.md` (exit 2).
    expected: name `t/one.md` as not a directory and say the argument wants a
    directory. The composed path inside a file is noise a reader has to undo.
    grade: NEXT

12. **A claim about a colleague is reported as the writer's STANDING claim.**
    `nova-self-talk --max 0 t/third.md`
    printed: `SELFTALK FAIL files=1 claims=1 standing=1 installations=0 dated=0 shown=1`,
    `SELFTALK NOTE catches known SHAPES only ...`,
    `SELFTALK FAIL t/third.md:2: STANDING match="bad at": My colleague is bad at reviewing.`
    expected: the class is "a first-person claim carrying a word of failure", and
    "My colleague is bad at reviewing" is a verdict on the colleague. The help
    lists `my <noun> is` as the shape, so the sentence matches the grammar as
    documented, but the reported finding is false on the meaning, and a page
    about other people collects them.
    grade: NEXT

13. **The `shapes` NOTE reads as a claim about the row output it sits under.**
    `nova-self-talk shapes`
    printed: the row `SHAPES ROW class=standing shape=STANDING ... finds="I am bad at estimating time." passes="I cannot merge without a read." pattern="..."`,
    then `SHAPES NOTE a standing or installation row reports its finds= sentence and not its passes= one; ...`.
    expected: every row prints both `finds=` and `passes=`, so "a row reports its
    finds= and not its passes=" reads as false; the NOTE means the scan reports
    the `finds=` sentence and passes the `passes=` one. Say "the scan", not "a
    row". (Checked: all 22 `finds=` sentences are flagged and all 22 `passes=`
    sentences are clean, so the behaviour is right and the sentence is not.)
    grade: NEXT

## Gate

The card's named test does not exist at this tip: `go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent` reports
`ok github.com/mas-bandwidth/nova-tools/internal/docs ... [no tests to run]`. The
real gate ran whole packages against this report's commit; its lines are in the
job's REPORT.md.

READ 9/10 — the banner answers what, how and first run; every verb's `-h`,
effect, flags and exit table are present and true, and the `example:` lines run
as printed; the point lost is finding 13's sentence and the `version [flags]`
usage line that promises flags `version` refuses.

USE 7/10 — on regular markdown it is exactly the advisory the help promises: dry
runs match the real path, `example` never overwrites other content, skips and
counts are honest; the points lost are finding 1 (a FIFO or a device in a glob
hangs it with no refusal and no deadline), finding 2 (no size cap, minutes of CPU
on one large file), and the double finding in 9.

urgent=1 next=12
