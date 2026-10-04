# nova-work USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 264f9c0135c7
Score: 8.5/10

## Reasons
The tool was tested cold in a local scratch directory using built binaries from cmd/nova-work, operating strictly offline through a recorded GraphQL stand-in via the --gh flag without touching the network or live services.

A first successful run with import --dry-run generated the complete execution plan, estimated calls, and confirmed read-only operation. Two small real jobs were executed end to end: first, importing a 20-issue repository into a canonical tree file with automatic round-trip encoding validation; second, verifying the tree against the source to confirm zero differences. Provoking drift by modifying issue titles in the tree file caused verify to pinpoint all 20 modifications with exact DRIFT lines, field names, and exit code 1.

Four distinct refusals were provoked and inspected. Missing required flags (--org and --out on import; --tree on verify) were surfaced simultaneously. Unknown flags (--bogus) listed all valid flags and suggested subcommand help. Unknown verbs listed all supported verbs. Multiple bad values (--page-size 0 and --max-calls 0) were both caught and reported in a single turn at exit 2.

The primary frictions preventing a 10/10 are that import and verify reject --json as an unknown flag despite the tool standard requiring JSON across all verbs, initial evaluation requires synthesizing an external gh mock script because no built-in offline demo exists, flag refusals point to global help instead of verb help, and tree syntax errors halt on the first encountered token.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-work import --org example-org --dry-run --json` | import and verify refuse --json with unknown flag error because both verbs bypass standard structured output | implement tool.Out structured result handling for import and verify to support --json | L |
| 2 | `nova-work import --org example-org --dry-run` | import requires a functional gh executable even for dry runs, lacking a built-in replay or fixture mode | add a built-in offline test mode or flag to replay from bundled test fixtures | M |
| 3 | `nova-work import` | missing required flag refusals point callers to global help rather than verb-specific help | point refusal remedy hints to run: nova-work import -h | S |
| 4 | `nova-work verify` | missing required tree flag points callers to run: nova-work help instead of run: nova-work verify -h | point refusal remedy hint to run: nova-work verify -h | S |
| 5 | `nova-work verify --tree ./tree-bad.lisp` | tree file decoding halts on the first lexical or structural error instead of reporting multiple syntax issues | report all syntax and schema errors encountered within bounded depth | M |
| 6 | `nova-work help import` | command help specifies tree.lisp file extension but omits the expected schema or s-expression structure of the tree | provide a brief summary of the tree file format in verb help | S |

## Good, keep
Clean dry-run mode that calculates call budgets and validates plans without writing to disk.
Deterministic field-for-field drift verification reporting exact attribute mismatches with exit code 1.
Multi-error validation that catches all out-of-range flag parameters in a single pass.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| import cannot be tried without a gh login | STILL THERE | `nova-work import --org test-org --dry-run --gh /nonexistent/gh` exits 2 naming CLI not found |
| value shapes undocumented | STILL THERE | `nova-work help import` names `--out <tree.lisp>` without documenting s-expression keywords |
| tree errors one per run | STILL THERE | `nova-work verify --tree ./tree-bad.lisp` halts at first parse error trailing bytes after one form |
| fake import and offline drift verification work | FIXED | `nova-work verify --tree ./tree-drift.lisp --gh ./fake-gh` prints DRIFT lines and exits 1 |
