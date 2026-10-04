# nova-table USE rating, nova-tools 1.1.0

Rater: GLM
Build: 6c30868ccba8
Score: 8.5/10

## Reasons
Cold use with no store permitted on this machine: help and -h answer, and the store-less form the help offers (every write verb under --dry-run) is real enough to run jobs end to end. Two jobs ran so: a board built one verb at a time (create, row add, cell add, cell move), then a batch manifest applied with --json; each dry-run prints the exact call it would send (sends="FCALL ns_table_create", dialled=0, written=0) and refuses exactly where the real run would refuse before sending. Every refusal met (missing required flag, unknown flag, unknown verb, bad value, unreachable address, read with no store) names the problem, the valid alternatives and one runnable next command; the read-with-no-store refusal prints the whole throwaway-store recipe it wants. Help is layered (help, help verb, help verb subverb, -h) and each verb page carries usage, example, flags, connection, exit codes and effect. What keeps this below 9.5: no daemon may start here and the refused mem: guess leaves no in-memory form, so every read verb, watch and shell is judged from its help alone; the reads have no --json (only batch does), so their output must be parsed as drawn text; and a batch manifest with several missing required keys is refused one problem at a time. A 10 needs a store-less way to run the reads end to end, --json on the reads, and manifest checks that name every problem at once.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-table list` | with no daemon permitted and `--redis mem:` refused ("not an address: its port is not a number"), no read verb (list, show, render, check, member, view), watch or shell can be tried at all here; the first-run text's only path to real data asks for a daemon | accept a mem: address (or an embedded store) so the reads run end to end with nothing dialled | L |
| 2 | `nova-table help list` | the read verbs' flags are --redis and --seat only: list, show, render, member read and cell members print drawn text with no --json, so an AI consuming them parses prose-drawn tables | give the reads the same --json receipt batch has | M |
| 3 | `nova-table batch --dry-run '{"schema":1,"table":"demo","members":[{"id":"b1"}]}'` | a manifest missing epoch, expected_table_revision and operation_id is refused with only the first problem ("member entry has no expect record"), so the caller fixes one fault per attempt | check the whole manifest and name every problem in one refusal | S |

## Good, keep
- Refusals that name the problem, the valid alternatives and a runnable next command every time, including the full verb list on an unknown verb and the whole throwaway-store recipe on a read with no store.
- --dry-run that prints the exact call the verb would send with dialled=0 written=0, and refuses exactly where the real run would before sending.
- Layered help with usage, example, flags, connection, exit codes and effect on every verb page, and subverb pages for row, col, cell and view.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 2026-10-02: reads have no --json | STILL THERE | `nova-table help list` shows --redis and --seat only; a read of every verb's help page finds --json on batch alone |
| 2026-10-02: the unknown-option refusal is generic | FIXED | `nova-table list --bogus` says "unknown flag --bogus; the flags of list are --redis; run: nova-table help list" |
| 2026-10-02: the remedy after drop ping-pongs between create and set | CHANGED | `nova-table drop` says "wants one table name: drop <table>; run: nova-table help drop"; the store-level refusal needs a store, not triable here |
