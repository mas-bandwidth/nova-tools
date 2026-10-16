# nova-check AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-check help` -> `nova-check: checks over markdown records and repositories, each finding named by file and line` and the example block ran as printed |
| 2 | refusals | 4 | `nova-check links` -> `LINKS REFUSED: --dir is required; it wants --dir <dir> is ...; refusing to guess; run: nova-check help` |
| 3 | output | 5 | `nova-check links --dir ./self --json` -> `{"result":{"verb":"links","status":"ok","exit":0},"facts":{"dir":"./self","files":1,...}}` |
| 4 | dry-run | 5 | `nova-check spelling --file ./miss.md --write --dry-run` -> `SPELLING OK files=1 misspellings=1 written=0 dry_run=true` and the file was unchanged |
| 5 | idempotent | 3 | `nova-check dogfood record --cli docs/CLI.md --tool nova-check --verb links --by me --ok --notes test --receipts ./receipts` repeated -> same receipt file name, no change; no --op flag exists on any write verb |
| 6 | no prompts | 5 | every invocation returned without waiting on input |
| 7 | exit codes | 5 | `nova-check links --dir <repo>` -> `LINKS FAILED ...` exit 1; `nova-check links --bogus` exit 2; `nova-check links --dir ./self` exit 0 |
| 8 | one word | 5 | help lists each verb once, distinct spellings, no duplicate verbs or tools |
| 9 | push | 5 | n/a: nothing waits on state; no polling or wait loop observed |
| 10 | no leaks | 5 | `nova-check version --json` -> `{"result":...},"payload":"nova-check v1.2.26-0.20261015145010-cc2f2f8c8148 linux/amd64 go1.26.6"}` no secret |
| 11 | true help | 5 | `nova-check quickstart --dir ./self` -> `QUICKSTART OK done=2 worst-exit=0` matches the help example |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-check dogfood record --cli docs/CLI.md --tool nova-check --verb links --by me --ok --notes test --receipts ./receipts` | write verbs take no --op, so a caller cannot name a retry-safe operation id; the write is deterministic (same receipt file) but unflagable | add --op <id> to the write verbs (dogfood record, spelling --write, convergence --state) per the standard | M |
| 2 | `nova-check links --dir <repo>` | on a failing check every line goes to stderr; stdout is empty, so a reader that takes stdout alone sees a silent empty result | also print findings to stdout, or state the stream contract in help so a reader knows to read both | S |
| 3 | `nova-check links --dir ./emptydir` | reports `LINKS OK dir=./emptydir files=0 links=0 excluded=0 broken=0` and exits 0: a green over zero files | warn when a check classifies nothing, as nocode already does with its NOTE line | S |
| 4 | `nova-check links` | the missing-required-flag refusal ends `run: nova-check help`, the global help, not `nova-check links -h` | point required-flag refusals at the verb's own -h | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-check dogfood ledger --cli docs/CLI.md --receipts ./receipts` | prints `tool=nova-check verb=-` and `tool=nova-self-talk verb=-` rows for tools without a declared verb list | skip empty verb rows, or explain them | S |

## Top 3 fixes
1. Add --op to the write verbs, moving criterion 5 from 3 to 5.
2. Stream check findings to stdout (or state the stderr-only contract in help) so a stdout reader is never empty on failure, fixing the biggest parsing trap.
3. Warn on a check that classifies zero files instead of reporting green, preventing a false pass.

## Good, keep
- The help `example:` block runs as printed and is the fastest way into the tool; keep it in sync with the code.
- Every verb emits the one result value as both typed lines and --json from the same shape; the grammar is stable and parseable.
- Refusals already name every problem at once with the next command; the two-unknown-flags case prints both, each with its own remedy.

## What could not be tried
- `nova-check convergence` real run: reads forge data through gh and touches no store; only its help and shape were judged because the card must not reach a live forge.
- `nova-check dogfood gate` full run: requires a shipped cmd dir and a receipt set; only help and the ledger shape were exercised.
- `nova-check attest` end to end: needs a memory-home tree and a manifest; only its help and --json shape were read.
