# nova-config USE rating, nova-tools 1.1.0

Rater: gpt-5.6-sol
Build: 8b2a9c6b8065
Score: 7.5/10

## Reasons

The core local workflow is strong. Starting from `nova-config help`, I migrated a JSON file, added and changed a machine, built a fleet and friend configuration, recorded the coordinator, then separately created a priced route and assigned it to a tier. Each write produced a compact receipt, history remained readable, `--dry-run` proved it did not add a row, and successful `--json` results were structured enough to consume without guessing. Missing required inputs were reported together, bad values named their accepted domain, and every refusal printed a runnable help or recovery command.

The largest first-use failure is that `inventory -h` presents a source-tree-relative fixture as its no-store example; run from the isolated directory exactly as printed, it refuses because that fixture is not installed or created. JSON is also not a true rendering mode for failures: adding `--json` to a supported command still writes the plain refusal to stderr and leaves stdout empty. Finally, typo handling promises a nearest flag but the observed `--widht` refusal only lists all accepted flags, making recovery slower than it claims. A 10 needs every installed-binary example to carry its own data, one result shape for success and refusal, and verified nearest-name suggestions.

I did not try real PostgreSQL, Redis, `apply`, or live inventory because the card forbids network stores and services. `apply --dry-run` explicitly still reads Redis, so I judged its effect and preconditions from help rather than dialing any address.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-config inventory --fixture fleet/testdata/inventory-fixture.yml` | The help's advertised no-store first run exits 2 in a clean working directory because it depends on a fixture path from the source checkout. | Add an embedded `inventory --example` fixture or have help print commands that create a complete local fixture before invoking inventory. | M |
| 2 | `nova-config machine add missing-json --file fleet.json --json` | A command that advertises `--json` emits a plain refusal on stderr with empty stdout, so callers must implement a second parser precisely on failure. | Route parse and validation refusals through the same `tool.Out` JSON encoder when `--json` is present. | M |
| 3 | `nova-config machine list --file fleet.json --widht 3` | The refusal lists every accepted flag but does not suggest `--width`, despite help claiming an unknown flag names the nearest one. | Add edit-distance flag suggestions to the shared parser and test this transposition. | S |
| 4 | `nova-config apply -h` | Help documents that dry-run still reads live Redis, so a cold user cannot preview the full apply plan with only the otherwise capable file store. | Offer an explicit offline snapshot or fixture input for planning, while keeping live conflict checks for the connected dry run. | M |
| 5 | `nova-config help` | The banner is accurate and comprehensive but long enough that the five-command first run appears after a dense kind catalog; choosing a first action takes more scanning than necessary. | Put the executable example immediately after the three-line model and move the full kind catalog behind `kinds` or kind help. | S |

## Good, keep

Keep the file-backed store: it supports meaningful end-to-end work without infrastructure and preserves history and revisions.
Keep multi-problem refusals, explicit effects, compact write receipts, and recovery commands.
Keep `--dry-run` and JSON success output deriving from the same checked operations as real writes.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| `--json` refusals leave stdout empty | STILL THERE | `nova-config machine add missing-json --file fleet.json --json` prints a plain refusal on stderr and no JSON on stdout. |
| `apply --dry-run` passes where apply refuses | CHANGED | `nova-config apply -h` now says dry-run still reads Redis and that full or fleet apply requires both endpoints; live equivalence could not be exercised without the forbidden service. |
| `--json` refusals escape as plain stderr | STILL THERE | `nova-config frobnicate --json` prints the ordinary one-line stderr refusal and exits 2; the unknown top-level verb does not recognize JSON mode. |
