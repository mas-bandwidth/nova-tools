# nova-self-talk dogfood, 2026-10-06

A stranger pass over nova-self-talk at 8076dfdf (sprint/mechanical-2026-10-02),
the v1.1.0 cut base. Read only `nova-self-talk -h`, `nova-self-talk help`, each
verb's `<verb> -h`, and this tool's section in docs/CLI.md. Then used every verb
for real against a scratch directory under the job's sandbox:

    nova-self-talk version
    nova-self-talk shapes
    nova-self-talk example ./pages
    nova-self-talk example --dry-run ./pages
    nova-self-talk example --json ./pages
    nova-self-talk ./pages/journal.md
    nova-self-talk scan ./pages/journal.md
    nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md
    nova-self-talk --skip RULES.md ./pages/RULES.md ./pages/journal.md
    nova-self-talk --json ./pages/journal.md
    nova-self-talk --max 0 ./pages/RULES.md ./pages/journal.md
    nova-self-talk --max 1 ./pages/RULES.md ./pages/journal.md
    nova-self-talk -                    (stdin as -)
    nova-self-talk ./pages/scan        (a file named like a verb)

plus every refusal a cold first run hits. The tool does exactly what its help
promises: findings print to stderr in line order; the DATED count, the closing
count line, a SKIP line and an every-run NOTE print to stdout; exit 1 on
findings, 0 on green, 2 on refusal; an all-skipped run exits 0 with
`SELFTALK SKIP files=0`, never OK; `--json` emits the documented object
(`result`, `facts`, `items`, `more`, `notes`); the example verb is idempotent
and non-destructive with `--dry-run`; each refusal names the problem and ends
with a runnable next command. No wrong result, no data loss, no remedy-less
refusal, and no help that lied were seen in this run.

## Findings

1. `nova-self-talk --bogus --max abc ./pages/journal.md`
   Printed (first 3 lines):
   `nova-self-talk REFUSED: unknown flag -bogus; the flags are --skip, --rule-doc, --max, --json; run: nova-self-talk help`
   (one line, exit 2)
   Expected: name every invocation problem at once — the unknown flag -bogus AND
   the bad --max value abc — so a cold reader fixes the call in one turn instead
   of fixing -bogus and re-running straight into a second refusal for --max.
   Grade: NEXT

2. `nova-self-talk help frobnicate`
   Printed (first 3 lines):
   `nova-self-talk: flags sentences where a writer passes a standing verdict on themselves`
   (blank)
   `how it works: each named file (- is stdin) is read sentence by sentence, line numbers kept,`
   (exit 0, the whole general banner)
   Expected: one line saying frobnicate is not a verb and naming the verbs
   (scan, shapes, example, version, help), exiting 2; an exit-0 fallback is
   indistinguishable from a real answer to a cold AI.
   Grade: NEXT

3. `nova-self-talk help scan`
   Printed (first 3 lines):
   `usage: nova-self-talk scan [flags]`
   `from `nova-self-talk help`:`
   `  nova-self-talk scan [flags] <file>...        the same scan, named as a verb`
   (exit 0)
   Expected: the bare `usage:` line to show the required `<file>...` so the
   natural first bare run is not a guaranteed refusal; the full form appears only
   on the quoted line beneath. `help example` has the same gap for `<dir>`.
   Grade: NEXT

4. `nova-self-talk --max abc ./pages/journal.md`
   Printed (first 3 lines):
   `nova-self-talk REFUSED: invalid value "abc" for flag -max: parse error; run: nova-self-talk help`
   (one line, exit 2)
   Expected: a refusal in the tool's own voice, matching its sibling `--max -1`
   ("--max must be a line ceiling of zero or more (got -1); 0 means print them
   all"), so every --max domain error sounds like this tool rather than the flag
   library.
   Grade: NEXT

READ 8/10 — the `-h` banner answers what/why/how/use in one paste and every verb's `-h` exits 0 before reading any file, but it restates the output contract three times (the usage block, the two-class summary, the print-line inventory) and coins "INSTALLATION" without saying why that name.
USE 9/10 — every verb ran first try with no service and the example verb is a no-risk first run, but two refusals are off-voice (the raw flag-library `--max abc` line and `help <unknown>`'s silent exit 0) where a single remedy-shaped line would save a turn.

urgent=0 next=4
