# nova-tokens USE rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash (OpenRouter)

Build: 0c5803c2de40

Score: 6.5/10

## Reasons

This rates the USE of nova-tokens at 0c5803c2de40 (full SHA 0c5803c2de406c1b0b2b0841f579c9bf73406b1c).

This is a strong command-line reader: the `help` banner is a model of the form, the
refusals name every independent problem in one run with what each input wants and a
`run:` breadcrumb, listings are bounded with `MORE shown=<n> total=<n>` and the exact
command that widens them, and a count a source never gave prints as `-` rather than a
zero that would sum into a false month. The advertised `example:` block runs as printed
and every line exits 0. On the owner's question, an AI can already do useful work with
it cold.

What holds the score down is machine ergonomics. No verb has `--json`: every verb prints
one shape, key=value separated by blanks, so a caller hand-parses, and one field
(`subject=`) carries its own blanks and its own ` at=` and ` build=` pairs. The writing
verbs (`fold`, `report`, `session`, `ledger`) have no `--dry-run`, so an AI cannot see
the plan before a write; `fold` writes day files on the first call. No writing verb takes
`--op`, so a retry after a timeout cannot be made idempotent, and none takes `--actor`.
An unknown flag falls out of the flag parser as `flag provided but not defined` rather
than the tool's own refusal grammar, naming the flag but not the nearest valid one. The
store-backed verbs (`ledger`, `report --redis`) could not be exercised here at all.

A 10 would need: a shared `--json` rendering of the same result value on every verb; a
`--dry-run` on every writing verb that prints the plan from the same path and writes
nothing; an `--op` id and an `--actor` on writes; the unknown-flag path through the
refusal printer with a nearest-name hint; and a quoted `subject=`. The help, example and
refusal work is already at that level.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens sum --out jobA/outA --month 2026-09 --json` | Refused: `flag provided but not defined: -json`. No verb of this tool accepts `--json`; a caller gets only key=value lines and must hand-parse them. The standard wants one value with two renderings. | Add the shared JSON encoder over the one result value and accept `--json` on every verb. | M |
| 2 | `nova-tokens fold --out jobA/outA --day 2026-09-10 --repos jobA/repos.tsv --claude bench=jobA/transcripts --dry-run` | Refused: `-dry-run` is not defined. `fold`, `report`, `session` and `ledger` write, and none prints a plan first; `fold` writes day files on its first call. | Give every writing verb a `--dry-run` that prints the plan from the same code path and writes nothing. | M |
| 3 | `nova-tokens report --who example --day 2026-09-12 --repos jobB/rep/repos.tsv --claude bench=jobB/rep --op fixed-1` | Refused: `-op` is not defined; `--actor` likewise. A timed-out write cannot be retried with the same id, so a caller cannot tell a replay from a second write. | Accept `--op <id>` on writes and return the recorded result for a repeat; accept `--actor` as the recorded author. | M |
| 4 | `nova-tokens sum --out jobA/outA --month 2026-09 --bogus` | `nova-tokens sum: flag provided but not defined: -bogus; run: nova-tokens help`. It names the flag but not the nearest valid one, and it is the raw parser shape rather than the `SUM REFUSED:` grammar the other refusals use. | Route unknown flags through the refusal printer and add a nearest-name hint. | S |
| 5 | `nova-tokens report --who example --day 2026-09-12 --repos jobB/rep/repos.tsv --claude bench=jobB/rep` | The `REPORT OK` line ends `subject=tokens 2026-09-12 at=2026-10-04T00:29:10Z build=...`; the value is unquoted and contains blanks plus its own ` at=` and ` build=` pairs, so a reader splitting on blanks cannot find the end of `subject`. | Quote the subject with the shared one-line quoting used for other free text. | S |
| 6 | `nova-tokens session --claude-session jobB/sess.jsonl --out jobB/outR --role reviewer` | With `--role reviewer` the day row is `claude-fable-5-1/reviewer` in the model column but `reviewer` in the repo column; the help says the model is booked as `<model>/<role>` and says nothing about the repo. | State in the help that `--role` sets the repo, or keep the repo and only suffix the model. | S |

## Good, keep

The banner: line 1 says what it does, `how it works:` names the nouns and where state
lives, `first run:` says one line, and the `example:` block runs exactly as printed.
Refusals: one run names every independent problem and what each input wants (`fold` with
no flags names all four), each with `run: nova-tokens help`.
Bounded listings keep their totals and hand back the widening command, and a value a
source never reported prints `-`, never `0`.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| A report with nothing to show printed `REPORT OK` on exit 1 | FIXED | `nova-tokens report --who example --day 2026-09-30 --repos empt/repos.tsv --claude bench=empt/t` prints `REPORT FAIL who=example day=2026-09-30 rows=0 unreadable=0` and exits 1 |
| `subject=` unquoted with blanks | STILL THERE | `nova-tokens report --who example --day 2026-09-12 --repos jobB/rep/repos.tsv --claude bench=jobB/rep` ends `subject=tokens 2026-09-12 at=2026-10-04T00:29:10Z build=...` |
| `--json` numbers rendered as strings | CHANGED | `nova-tokens sum --out jobA/outA --month 2026-09 --json` now prints `nova-tokens sum: flag provided but not defined: -json; run: nova-tokens help`: the flag is gone, so the string rendering is moot |
| The unknown-option refusal omitted the offending flag | FIXED | `nova-tokens sum --out jobA/outA --month 2026-09 --bogus` prints `nova-tokens sum: flag provided but not defined: -bogus; run: nova-tokens help` |
