# nova-self-talk USE rating, nova-tools 1.1.0

Rater: glm-5.3-flash
Build: bd7949b97aec
Score: 9.5/10

## Reasons
Cold start, no service needed: `nova-self-talk help` alone was enough to run every job
first try. The first run is `nova-self-talk example ./pages`, whose --dry-run prints
would-write= plus the exact next command, and whose real write lays down two pages that
its own copy-paste scans then exercise; a re-run over an existing directory writes
nothing and reports kept= instead, so the first write is a no-risk step. Scans behave
exactly as the help says: findings on stderr in line order, the count line, the DATED
count and an every-run NOTE on stdout, exit 1 on findings, exit 0 on a green, exit 2 on
a refusal, and an all-skipped run exits 0 with a SKIP line, never OK. --json on scan,
shapes and example prints the documented object (result, facts, items with file, line,
shape, match, text, more, notes) with nothing left to guess; --max truncation adds a
MORE line that names its own remedy (--max 0 prints every finding). All four provoked
refusals name the problem and end with a runnable next command: no files (explains the
shell glob and points at example), unknown flag (names -bogus and lists the four valid
flags), a word that is neither verb nor file (lists the verbs and says a file of that
name is ./frobnicate), a bad value (--max -1 gets a domain refusal in the tool's own
voice), and an unreadable path refuses with NOTHING was scanned, which is not a green.
A refusal after `./pages/journal.md --json` even answers as JSON, because --json was
asked for. What a 10 needs: a refusal naming every problem of one invocation at once
(the unknown-flag refusal stops at the first problem and swallows a bad --max on the
same line), `help <unknown-word>` saying so instead of silently returning the general
help with exit 0, a per-verb usage line that shows the required <file>..., and a
bad-integer --max refusal written in the tool's voice rather than the flag library's.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk --bogus --max abc ./pages/journal.md` | the refusal names only the first problem, unknown flag -bogus; the bad --max value abc is never mentioned, so an AI fixes one flag and re-runs straight into the second refusal | collect every invocation problem before refusing and print them in one REFUSED block | S |
| 2 | `nova-self-talk help frobnicate` | an unknown word after help silently prints the general help and exits 0; an AI cannot tell a fallback from an answer and may believe frobnicate is a verb | print one line saying frobnicate is not a verb (the verbs are scan, shapes, example, version, help) above the text, and exit 2 | S |
| 3 | `nova-self-talk help scan` | the usage line is `nova-self-talk scan [flags]` and omits the required <file>... (only the quoted top-level line shows it), so the natural first bare run fails, though the refusal then explains the glob and example fully | print `scan [flags] <file>...` plus a files-are-required line in the per-verb flags block | S |
| 4 | `nova-self-talk --max abc ./pages/journal.md` | the refusal reads like the flag library (invalid value "abc" for flag -max: parse error, single dash, bare parse error) while its sibling --max -1 refusal is written in the tool's voice | say --max must be a whole number of zero or more, got abc, in the same voice as the -1 check | S |

## Good, keep
- Every refusal ends in a runnable next command, and an unknown flag lists the valid four; the unreadable-file path adds NOTHING was scanned, which is not a green, so no exit 2 can masquerade as a pass.
- Findings go to stderr, counts and NOTEs to stdout, and the help documents the 2>/dev/null recipe; the every-run NOTE says a green clears known shapes only, with shapes as the way to list them.
- example --dry-run plus the non-destructive re-run (wrote=- kept=RULES.md,journal.md) make the first write a no-risk copy-paste path, with the next command printed both ways.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| raw Go flag errors (2026-10-02) | FIXED | `nova-self-talk --bogus ./pages/journal.md` prints nova-self-talk REFUSED: unknown flag -bogus; the flags are --skip, --rule-doc, --max, --json; run: nova-self-talk help |
| a green over a binary file (2026-10-02) | FIXED | `nova-self-talk $JOB/bin/nova-self-talk` exits 1 with FAIL lines (the binary's embedded text trips the shapes), never OK; an unreadable path exits 2 refusing |
| the unknown-option refusal omits the offending flag (2026-10-02) | FIXED | the same refusal names -bogus and lists the four valid flags |
