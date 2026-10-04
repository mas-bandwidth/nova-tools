# nova-table USE rating, nova-tools 1.1.0

Rater: qwen3.8-flash
Build: bd7949b97aec
Score: 6.5/10

## Reasons

The question asked of every tool: is this a good tool for an AI to use? Cold against the binary, the command side is met and the result side is not.

Good first: `nova-table help` answers what it does, how it works and how to use it; every verb's -h carries usage, a runnable example, one wants-line per flag, the effect word (inspection, store write) and the exit table, at exit 0 before anything is dialled. Refusals name the problem, say what the flag wants, and answer a near miss with the nearest name (`did you mean --columns?`). Every write verb plans offline under `--dry-run` and states its own bound honestly: what only the store can check is left to the real run. `batch` runs one manifest through lines or `--json` from one value, plan, receipt and refusal alike, and replays by operation id.

What costs the score: the read side cannot be met at all without a live store, and the tool offers no store-free form: `--redis mem:` is refused as not an address, so the banner's example: block cannot run as printed on a cold machine (finding 1). `show`, `render`, `list`, `member read` and `cell members` refuse `--json`, offered only on `batch`, while the house shape says every verb accepts it, and the help never prints the line shape an AI must scrape (finding 2). One refusal names one problem: a width error waits behind the columns error, and a second unknown flag waits behind the first, so recovery costs a turn per problem (finding 3). No verb takes `--max`, so listings are unbounded (finding 4). The dry-run line leads with the undocumented status word DRY-RUN after the tool prefix (finding 5).

Where I guessed: whether reads accept `--json` (the house shape says they do; they refuse), `--redis mem:` as the in-memory form a first try wants, and that the width error rides behind the columns error rather than beside it (two runs to learn).

Verbs not tried, each needing a real store this rater may not start: `list`, `show`, `render`, `watch`, `check`, the drop-to-create round trip, the epoch and receipt lines, bound cells, `row sort --keep`, `shell`, `view show` and `view list`; they are judged from their help and, where offered, `--dry-run`.

A 10 would need: a store-free trial (an in-memory `--redis` form or a quickstart that loads a fixture), `--json` on every verb, one refusal naming every independent problem at once, `--max` with its MORE line on the listings, and dry-run lines in the documented grammar.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-table show demo` | SHOW REFUSED: --redis is required; with no store every read verb refuses, --redis mem: is refused as not an address, and the example: block (create through render) exits 2 as printed, so a cold AI can plan writes but never sees a table | add a store-free trial: an in-memory --redis value or a quickstart that loads a fixture, so the read verbs answer offline and the example block runs as printed | L |
| 2 | `nova-table show demo --json` | SHOW REFUSED: unknown flag --json; batch is the only verb whose help offers --json, so the results an AI most needs to parse (show, render, list, member read, cell members) arrive only as scrapeable text whose shape the help never prints | route every verb's one result value through the shared skeleton's JSON rendering, and print a sample result line in the read verbs' help | M |
| 3 | `nova-table create demo --columns 'a:pct(x' --width bad --dry-run` | one CREATE REFUSED names only the columns error; the width error appears only on the next run, and two unknown flags name only the first, so a reader fixes the call over turns instead of once | collect all independent validation errors and print them in one refusal, each with its wants-line | M |
| 4 | `nova-table help list` | no verb's help offers --max (none appears anywhere in the tool), so list, show, render and cell members print unbounded and a large store floods the reader with no totals to act on | add --max with the MORE shown total line to the listing verbs, as the rest of the set carries it | M |
| 5 | `nova-table create demo --columns ready,working,done --dry-run` | the plan line reads TABLE DRY-RUN verb=create ..., leading with a fourth status word after the tool prefix while the documented grammar is the verb name then OK, REFUSED or FAILED, so a parser built from the help misreads it | print CREATE OK with a dry_run fact, or name DRY-RUN in the banner's status and exit lines | S |

## Good, keep
Every verb's -h answers at exit 0 before anything is dialled: usage, a runnable example, a wants-line per flag, the effect word and the exit table; help is never a refusal.

Refusals carry the remedy in the line: what the flag wants, the nearest name for a near miss, and for an unreachable store the address tried plus a runnable throwaway-store command.

`batch` is the model of the house shape: one manifest, one value rendered as lines or `--json`, dry-run plan and refusal included, replay by operation id documented in the help.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the remedy after drop ping-pongs between create and set (rated 2026-10-02 at 1aac13259) | CHANGED | `nova-table drop demo --dry-run` prints TABLE DRY-RUN verb=drop sends=FCALL ns_table_drop, and `nova-table help drop` now states what drop keeps (the column definition and the table identity) and what --definition removes; the real round trip needs a store and could not be run here |
| reads have no --json (rated 2026-10-02 at 1aac13259) | STILL THERE | `nova-table show demo --json` prints SHOW REFUSED: unknown flag --json; the flags of show are --at-epoch, --redis |
| the unknown-option refusal is generic (rated 2026-10-02 at 1aac13259) | FIXED | `nova-table create demo --column a --dry-run` prints CREATE REFUSED: unknown flag --column; the flags of create are --actor, --columns, ...; did you mean --columns?; run: nova-table help create |
