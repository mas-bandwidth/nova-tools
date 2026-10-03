# nova-tokens USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 264f9c0135c7
Score: 8/10

## Reasons
The tool executes rapidly and predictably against local synthetic transcript and rule fixtures. The onboarding example commands in help run cleanly without requiring network access or external database services. Refusal behavior for missing required arguments, unknown verbs, and malformed parameter values is exemplary: every missing flag is reported simultaneously in a single invocation with explicit descriptions of expected shapes and actionable remedies. Exit codes reliably isolate syntax and prerequisite failures (exit 2) from execution assertions (exit 1) and success (exit 0).

A score of 10 would require implementing --json across all verbs, quoting multi-token values such as subject in command receipts, ensuring REPORT OK is never printed on non-zero exit codes, adding default calendar gap assertions to check, and supporting offline simulation for Redis-dependent verbs.

Verbs ledger and report --redis require a running Redis instance and were evaluated through their help documentation and flag specifications rather than live store connections. All other verbs were exercised directly in the scratch directory.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` | REPORT OK status token is printed on stderr even when the command exits 1 due to unreadable inputs | print REPORT FAIL on non-zero exits to prevent misleading status token parsing | M |
| 2 | `nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` | subject field contains unquoted spaces breaking standard key-value token parsing | wrap multi-token subject value in quotes using oneline.Quote | S |
| 3 | `nova-tokens check --json` | command verbs lack structured json output support contradicting shared tool standard | implement --json emission across fold, check, sum, and report | L |
| 4 | `nova-tokens check --out ./out` | check allows unrecorded date gaps between first and last days without asserting a failure | make date gap detection the default behavior and provide an explicit allow flag | S |
| 5 | `nova-tokens fold` | command requires four separate arguments without a single-command dry-run convenience | add a dry-run flag or synthetic sample preset for immediate onboarding | M |
| 6 | `nova-tokens ledger -h` | ledger and report --redis require live redis infrastructure with no offline simulation mode | provide an in-memory test mode or mock store flag for offline verification | M |

## Good, keep
Help usage block provides a copy-pasteable transcript setup and six runnable commands that succeed out of the box.
Refusal handlers name every omitted flag simultaneously with clear descriptions and remedy invocations.
Clean separation of input, output, cache_write, cache_read, and reasoning token dimensions throughout report and sum.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| REPORT OK with exit 1 | STILL THERE | `nova-tokens report` exits 1 after printing REPORT OK when unreadable sources occur |
| subject= unquoted with spaces | STILL THERE | `nova-tokens report` emits unquoted subject=tokens 2026-09-11 at=... build=... |
| --json numbers as strings | CHANGED | `nova-tokens check --json` refuses flag provided but not defined: -json |
| the unknown-option refusal omits the offending flag | FIXED | `nova-tokens fold --unknown-flag` reports flag provided but not defined: -unknown-flag |
