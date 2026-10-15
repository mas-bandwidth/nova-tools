# nova-check AI-usability review, 2026-10-15

Reviewer: gpt-6-luna
Build: 453ce3a00131
Score: 7/10 (38/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-check quickstart --dir scratch/self` -> `QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus ...` |
| 2 | refusals | 4 | `nova-check version --frobnicate --also-bad` names both flags and points to `version -h`; `nova-check kernel` names both missing inputs but points only to global help |
| 3 | output | 2 | `nova-check dogfood ledger --cli repo/docs/CLI.md --receipts scratch/receipts --max 1` -> `DOGFOOD OK verbs=173 dogfooded=0 by-nonauthor=0 open-edges=0 unfiled=0 unmatched=0` |
| 4 | dry-run | 2 | `nova-check spelling --file scratch/spelling.md --write --dry-run --json` -> `"status":"ok","exit":0` despite `"misspellings":1` |
| 5 | idempotent | 0 | `nova-check dogfood record -h` -> `effect: local write: appends one receipt file to --receipts (--dry-run writes none)` |
| 6 | no prompts | 5 | `nova-check quickstart --dir scratch/self` -> `QUICKSTART OK done=2 worst-exit=0` |
| 7 | exit codes | 5 | `nova-check kernel` -> `exit=2` |
| 8 | one word | 4 | `nova-check help` -> `dogfood ledger, dogfood record, dogfood gate` |
| 9 | push | 5 | n/a: no verb waits for state |
| 10 | no leaks | 5 | `nova-check kernel --file scratch/self/docs/SEED-CORE.md --max-bytes nope` -> `invalid value for --max-bytes: it wants a whole number` |
| 11 | true help | 2 | `--max 1` does not bound `dogfood ledger`; help also omits the `--dir` base for `--exclude` and `--file` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-check dogfood record -h` | `appends one receipt file`; the write flags have no `--op`, so a retry after a lost answer can append twice | add an operation id and replay-safe result to each write | M |
| 2 | `nova-check spelling --file scratch/spelling.md --write --dry-run --json` | exit 0 and `status=ok` while `misspellings=1`; a dry-run finding looks successful | return the same finding status and exit as the write path, while keeping `written=0` | S |
| 3 | `nova-check dogfood ledger --cli repo/docs/CLI.md --receipts scratch/receipts --max 1` | `--max 1` still emits all 173 verb rows instead of one row and `MORE` | apply the bound to the typed listing and emit its total | S |
| 4 | `nova-check links --dir scratch/broken --exclude ./scratch/broken/sub` | `excluded=0`, then the file is reported broken; `--exclude sub/a.md` works because the path is relative to `--dir`, a base the help does not say | state and consistently resolve the path base, or accept invocation-relative paths under `--dir` | S |
| 5 | `nova-check links --dir repo` and `nova-check links --dir scratch/empty` | failed counts and findings are all on stderr (stdout is empty); empty input reports `LINKS OK files=1 links=0` with no NOTE | document the stream contract and print a NOTE rather than green on an empty scan | S |
| 6 | `nova-check links --dir repo --max 5` | `LINKS BROKEN file=docs/dogfood/2026-10-06/codex/memory.md line=99 ...` does not use the documented continuation words `MORE` or `NOTE` | render findings with the shared continuation grammar | S |
| 7 | `nova-check kernel` | both missing inputs are named, but the recovery command is only `nova-check help` | point to `nova-check kernel -h` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|

## Top 3 fixes
1. Make write verbs replay-safe with a stable operation id, and preserve failure status in dry-run output.
2. Make `dogfood ledger --max` bound its output and report `MORE`.
3. Put failure output in the documented grammar and make empty scans visibly empty rather than green.

## Good, keep
`quickstart` offers a small infrastructure-free run; the synthetic Markdown tree passes and names useful next checks.
`dogfood record --dry-run` and spelling dry-run did not alter scratch receipts or the Markdown hash. Unknown-flag refusals now name both bad flags, fixing a flaw described in earlier 1.2.0 ratings.
Refusals name missing kernel inputs together and give specific remedies for bad values and unknown flags, though the missing-input remedy still points only to global help.

## What could not be tried
`convergence` was not run: its help says it reads the forge through `gh`, including when it would write state; no remote service was accessed.
`floors` was not run because the staged checkout has no seed-document pair.
