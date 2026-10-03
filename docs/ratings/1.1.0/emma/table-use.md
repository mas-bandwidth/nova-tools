# nova-table USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 264f9c0135c7
Score: 8.5/10

## Reasons
The command-line interface provides comprehensive help, consistent flag naming across commands, and informative refusal messages. When arguments or flags are invalid, the tool precisely identifies the issue and presents helpful guidance. Flag errors list all allowed options for the specific verb, and formula syntax validation offers thorough explanations of supported projection types and column references.

A score of 10 would require implementing an in-memory or store-free mode for local testing without an external daemon, adding dry-run capabilities across write operations, extending structured JSON output to all inspection and read commands, formatting client-side manifest validation errors as JSON when requested, and directing missing flag refusals to verb-specific help topics.

Verbs requiring an active Redis daemon were evaluated from their help and dry-run specifications because host redis-server execution is prohibited and in-memory mode is unsupported. The unexecuted verbs were create, set, drop, list, row add, row set, row hide, row show, row del, row move, row order, row sort, col add, col del, col move, cell add, cell remove, cell move, cell members, member create, member find, member read, batch, check, clear, show, render, watch, view set, view state, view show, view list, view del, and shell.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-table show demo --json` | Read and inspection verbs reject the json flag preventing automated machine parsing | implement json output across show list check and view verbs | M |
| 2 | `nova-table create --redis mem: demo --columns ready,done` | Tool rejects mem address and requires an active daemon preventing store-free testing | support in-memory mock store for local execution | L |
| 3 | `nova-table create demo --dry-run --columns ready,done` | Write verbs lack dry-run support to preview changes before execution | implement dry-run flag across write operations | M |
| 4 | `nova-table batch --json '{"schema": 1}'` | Pre-send manifest validation failures print plain text refusal lines rather than JSON | output validation refusals as structured JSON when requested | S |
| 5 | `nova-table create demo` | Missing flag error directs caller to root help instead of verb-specific help | update missing flag guidance to suggest nova-table help create | S |
| 6 | `nova-table create demo --columns 'pct(done)'` | Column validation error on missing identifier reports generic naming refusal | clarify formula syntax requirement when identifier is omitted | S |

## Good, keep
Unknown flag refusals list every allowed flag for the verb alongside usage guidance.
Formula validation explains projection semantics and identifies missing column prerequisites.
Exit codes cleanly distinguish success, semantic refusals, and invocation syntax errors.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the remedy after drop ping-pongs between create and set | FIXED | internal/ntable/store.go:341 suggests drop with definition flag |
| reads have no --json | STILL THERE | `nova-table show demo --json` yields unknown flag --json |
| the unknown-option refusal is generic | FIXED | `nova-table create --unknown-flag demo --columns ready,done` lists valid flags |
