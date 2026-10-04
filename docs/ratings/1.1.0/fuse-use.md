# nova-fuse USE rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: 501cee820ed3
Score: 9/10

## Reasons
The tool is a gate and says so: `check` is the one verb that looks, its exit 0
means CLEAR, and every other exit is a refusal, so an AI cannot mistake a failed
look for permission. The banner answers all three questions in its first lines,
the `example:` block runs as printed end to end, and the six onboarding points
hold. Every refusal I provoked named the problem, the offending flag or
argument, and the next command, in one line, and it exited 2 while the answer
exited 0 or 1. `--dry-run` is offered on all four write verbs, prints
`dry_run=true`, writes nothing, and says what the real run would do. `status`
bounds its list with a `MORE` line whose total is never capped, and `--max 0`
widens it. A 10 would need: a machine-readable rendering, since `--json` is
refused by design and a caller must parse typed lines and read SPEC.md for the
grammar; a re-blow that names the quarantine it replaces instead of overwriting
its time and reason without a word; every independent problem in one refusal
when a flag is unknown; and the blown reason on stdout with the rest of the
result, not only on stderr.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse status --box ./b2.json --json` | every verb refuses `--json`; the one value has one rendering, so an AI parses typed lines by hand and must find SPEC.md for the grammar | render the same result value as JSON under `--json`, keeping the exit codes | M |
| 2 | `nova-fuse quarantine --box ./c.json s1 "second reason"` | a second quarantine of one surface prints OK with the new time and reason; the first reason is gone and the line never says it replaced one | name the replaced fuse and its old time in the line, or refuse the re-blow and point at `lift quarantine` | S |
| 3 | `nova-fuse check --bogus` | an unknown flag stops the parse, so the refusal never names the also-required `--box`; a cold reader needs a second run to learn both | after a parse failure, report each missing required flag in the same refusal | S |
| 4 | `nova-fuse check --box ./b2.json a-forum` | the blown `FUSE FAILED` line and its remedy go to stderr, while a clear `FUSE OK` goes to stdout; a caller reading stdout alone sees an empty result | print the blown line on stdout as the result and keep exit 1 | S |

## Good, keep
The exit-code contract is stated in the banner and repeated per verb, and it is
kept: 0 clear, 1 blown, 2 could-not-run. A refusal names the offending flag and
the next command in one line and exits 2, and a missing `--box` is refused,
never defaulted. `--dry-run` writes nothing and says so, and `status` keeps its
uncapped total under a bounded list.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a re-blow silently rewrites the fuse's time and reason | STILL THERE | `nova-fuse quarantine --box ./c.json s1 "second reason"` prints `QUARANTINE OK s1 since=2026-10-04T04:27:04Z: second reason`; `nova-fuse status --box ./c.json` shows only `second reason`, with no word that it replaced the first |
| the unknown-option refusal omits the offending flag | FIXED | `nova-fuse init --bogus` prints `nova-fuse init REFUSED: unknown flag --bogus; the flags of init are --box, --dry-run; run: nova-fuse help init` |
