# nova-table USE rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash
Build: 0c5803c2de40
Score: 7/10

## Reasons
nova-table is a strong help surface: a complete banner, per-verb and per-subverb help, an example block, and one-line refusals that name the problem and a remedy at exit 2. Two gaps cost the most. First, no verb offers --dry-run and no verb works without a live store, so an AI cannot preview a write or try a real verb before adopting; the standard's dry-run and store-free properties are unmet. Second, the primary reads (show, list, render, check, member find, cell members, view show, view list) refuse --json, so the one-value-two-renderings rule is not met and an AI must parse text tables; only batch and member read take --json. Refusals also miss the REFUSED status word, create reports one missing input per turn, and unknown names carry no nearest. A 10 would add a store-free or dry-run path, --json on every verb, one-turn refusals that name every missing input, and nearest-name hints.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-table create demo --columns ready --dry-run --redis 127.0.0.1:1` | the help offers no --dry-run on any write verb and the flag is refused as unknown; every read and write needs a live store, so an AI cannot preview a write or try a real verb before adopting | add --dry-run to every write verb, printing the plan from the same code path and writing nothing, plus a store-free example that writes a local fixture | L |
| 2 | `nova-table show demo --json --redis 127.0.0.1:1` | show, list, render, check, member find, cell members, view show and view list refuse --json; only batch and member read take it, so the one value with two renderings rule is unmet and an AI must parse text tables | render the shared result value as JSON for every verb, from the same value the lines come from | L |
| 3 | `nova-table create` | with both the table name and --columns missing it names only the table; the --columns refusal comes on the next run, so recovery takes two turns | report the missing table and --columns together in one refusal | S |
| 4 | `nova-table frobnicate` | the refusal reads `nova-table: unknown verb frobnicate; ...` with no REFUSED status word, against the one grammar `VERB REFUSED: <reason>; run: <remedy>`; the bare command and the usage refusals are the same | print the REFUSED status word after the verb in every refusal | S |
| 5 | `nova-table list --bogus` | it lists the verb's flags but never names the nearest; an unknown verb lists every verb with no nearest | append the nearest known flag or verb to the refusal | S |
| 6 | `nova-table cell add demo build ready b1 --score xyz` | the bad-value refusal ends `run: nova-table help`, not `run: nova-table help cell add`, so the next turn is a search | name the verb's own help in the remedy of a flag-value refusal | S |

## Good, keep
The banner answers what it does, how it works and a first run, and `help <verb>` and `<verb> -h` exist for every verb and subverb and exit 0.
Refusals are one line, name the missing input with its shape, and carry a remedy at exit 2; the connection refusal names the address and says to start the store or correct it.
batch validates the manifest before sending and says changed=no, and the column-spec refusal names the missing count column and how to declare it.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| reads have no --json (1aac13259) | STILL THERE | `nova-table show demo --json --redis 127.0.0.1:1` prints `unknown flag --json; show flags: --at-epoch, --redis`, exit 2 |
| the unknown-option refusal is generic (1aac13259) | CHANGED | `nova-table list --bogus` now prints `unknown flag --bogus; list flags: --redis; run: nova-table help list`, naming the verb's flags though not the nearest |
| the remedy after drop ping-pongs between create and set (1aac13259) | STILL THERE | `nova-table help drop` keeps the saved column definition and the identity, and the only restore verbs are create and set; the drop path could not be exercised because no functional container is available, so this is read from help, not run |
