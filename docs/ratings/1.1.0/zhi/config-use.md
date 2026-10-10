# nova-config USE rating, nova-tools 1.1.0

Rater: Claude <deepseek-v4-pro>
Build: 2c02b2aa2042
Score: 8/10

## Reasons

The banner answers all three questions and the first run is a paste:
`nova-config migrate --file try.json`, then machine add, set, list and history,
every line as the README prints it. Two real jobs ran end to end on the
--file store: a machine's lifecycle, then a fleet's shape (fleet endpoints, a
friend, a sprint handover, a route and its tier, a supervised loop), each write
returning its `CONFIG ADD|SET` line and its history row, and `status` and
`machine show` reading them back. Nothing real was touched: every write went to
the local JSON file; `apply` and `inventory` ran only in store-free forms
(migrate --print, migrate --dry-run, inventory --fixture) or refused cleanly
when a live Redis was absent.

Four refusals were provoked, and each names the problem, every problem at once,
and the next command: a machine add with no required fields named all three
missing flags and `run: nova-config machine add -h`; an unknown flag named
`--jsno (nearest: --json)` and the flags the verb takes; an unknown verb named
what is wanted; a bad `--tier quantum` named `want one of flash, pro`.

`--dry-run` on add, set and remove prints the change and writes nothing, and it
runs the real checks: `machine remove m1 --dry-run` refused because m1 is the
fleet's --store. `--json` on a success is one object in pkg/tool's shape
(result, facts, items) and is actable. Where I had to guess: `apply` against a
live Redis and `inventory` against a live Redis were not run (the rules forbid
starting one), so the CEILING, CONFLICT and roles refusals, and the parity of
`apply --dry-run` with a real apply, are read from the code, not observed.

The score is held down by one systemic gap and two small ones. The systemic
gap: `--json` never wraps a refusal, so a refused write exits 1 or 2 with
stdout empty and the refusal only on stderr; a program reading stdout sees
nothing on the failure path. A 10 renders every refusal as the same JSON object
with a refused status and the remedy, makes `machine width --json` use that one
shape, and silences the Redis client's own log lines so a failed dial is one
clean refusal line.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine add m1 --user nova --seat s1 --slots 8 --as a1 --file try.json --json` | a refused write leaves stdout empty and puts the refusal on stderr, and the same holds for a usage refusal (a missing required flag): --json never wraps a refusal, so a program reading stdout sees nothing on the failure path | render every refusal and failure as the same tool.Out JSON object (status refused, remedy) the success path prints | M |
| 2 | `nova-config machine width m1 --file try.json --json` | machine width --json prints a bare object (machine, width, member) instead of pkg/tool's result, facts and items shape, so --json is not one shape across the tool | emit the width as one item under the standard result shape, or drop --json here and print the line | S |
| 3 | `nova-config apply --dry-run --file try.json --redis 127.0.0.1:6379` | when the store does not answer, the Redis client's own pool log lines print to stderr before the tool's one-line refusal, so the failure is noisy instead of one line | silence the Redis client logger on the tool's stderr and keep only the one refusal line with its remedy | S |

## Good, keep

The first run needs no database: migrate --file makes a local store and every
verb but apply's write runs against it. The refusal grammar names every problem
at once with the next command, and a printed remedy repeats --file or --pg so
it pastes. --dry-run prints the change from the same checks and writes nothing,
and a write verb refuses a row another kind names.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| every refusal under --json leaves stdout empty | STILL THERE | `nova-config machine add m1 --user nova --seat s1 --slots 8 --as a1 --file try.json --json` — exit 1, stdout empty, stderr `nova-config machine add REFUSED: machine m1 exists; run: nova-config machine set m1 --<field> <value> --file try.json` |
| --json refusals escape as plain stderr | STILL THERE | `nova-config machine add m2 --as a1 --file try.json --json` — exit 2, stdout empty, stderr `nova-config machine add REFUSED: --seat is required ... --slots ... --user ...; run: nova-config machine add -h` |
| apply --dry-run passes where apply refuses | CHANGED | `nova-config machine remove m1 --as a1 --file try.json --dry-run` — exit 1 `machine m1 is the --store of the fleet` (a dry run runs the ref checks); cmd/nova-config/main.go:1432-1440 runs the fleet endpoint check for --check too |
