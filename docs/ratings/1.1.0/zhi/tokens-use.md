# nova-tokens USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 7.5/10

## Reasons

The first successful run was the included fixture: `nova-tokens sources --repos ./cmd/nova-tokens/testdata/example-bench/repos.tsv --all --claude trial=./cmd/nova-tokens/testdata/example-bench/transcripts` exits 0 and reports one source, three messages, two rows, and the unattributed count. A second, different job ran end to end: `fold` wrote one day file, and `sum --month 2026-09` printed the per-pair, per-model and total rows with dashes for missing counts. The report local mode printed the note body and `REPORT OK who=zhi day=2026-09-11 rows=7`.

The four refusals: a missing --out prints `TOKENS REFUSED: --out is required ...; run: nova-tokens help` and also names the missing source flag in the same run, so all problems are named at once; a bad --month says `SUM REFUSED: --month is not a month: notamonth; it wants one month as YYYY-MM; run: nova-tokens help`; an unknown verb says `unknown subcommand "frumble"; run: nova-tokens help` but does not list the verbs; an unknown flag on sum prints the raw Go flag error `flag provided but not defined: -bogus` rather than the tool's own refusal grammar.

What costs the score: the raw Go flag errors for unknown flags, the unknown-verb refusal without the verb list, and no --json on the read verbs, so a program must parse the text tables.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens sum --out ../scratch/days --month 2026-09 --bogus` | prints the raw Go flag error instead of the tool's own refusal grammar | rewrite the flag package's error as a TOKENS REFUSED with the flag list | S |
| 2 | `nova-tokens frumble` | says unknown subcommand and the next command, but does not list the verbs | list the verbs in the unknown-verb refusal | S |
| 3 | `nova-tokens sum --out ../scratch/days --month 2026-09 --json` | no --json on sum, so a program must parse the text table | add --json to sum and check | S |

## Good, keep

The fold/sum path that writes one day file per day and sums it without guessing. The multi-problem refusal on fold that names every missing requirement in one run. The check verb that acts as the gate rather than a report.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| REPORT OK with exit 1 | CHANGED | `nova-tokens report --who zhi --day 2026-09-11 ...` with rows exits 0 and prints REPORT OK |
| subject= unquoted with spaces | STILL THERE | `REPORT OK ... subject=tokens 2026-09-11 at=2026-10-03T23:09:35Z ...` prints the subject unquoted |
| the unknown-option refusal omits the offending flag | CHANGED | `nova-tokens sum --bogus` now names `-bogus`, though in the raw Go error |
