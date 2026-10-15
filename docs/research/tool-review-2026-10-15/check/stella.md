# nova-check AI-usability review, 2026-10-15

Reviewer: gpt-6-luna
Build: 453ce3a00131
Score: 7.5/10 (40/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-check quickstart --dir scratch/self` -> `QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus ...` |
| 2 | refusals | 4 | `nova-check version --frobnicate --also-bad` names both flags and points to `version -h`; `nova-check kernel` names both missing inputs but points only to global help |
| 3 | output | 3 | `nova-check dogfood ledger --cli repo/docs/CLI.md --receipts scratch/receipts --max 1` -> `DOGFOOD OK verbs=173 dogfooded=0 by-nonauthor=0 open-edges=0 unfiled=0 unmatched=0` |
| 4 | dry-run | 2 | `nova-check spelling --file scratch/spelling.md --write --dry-run --json` -> `"status":"ok","exit":0` despite `"misspellings":1` |
| 5 | idempotent | 0 | `nova-check dogfood record -h` -> `effect: local write: appends one receipt file to --receipts (--dry-run writes none)` |
| 6 | no prompts | 5 | `nova-check quickstart --dir scratch/self` -> `QUICKSTART OK done=2 worst-exit=0` |
| 7 | exit codes | 5 | `nova-check kernel` -> `exit=2` |
| 8 | one word | 4 | `nova-check help` -> `dogfood ledger, dogfood record, dogfood gate` |
| 9 | push | 5 | n/a: no verb waits for state |
| 10 | no leaks | 5 | `nova-check kernel --file scratch/self/docs/SEED-CORE.md --max-bytes nope` -> `invalid value for --max-bytes: it wants a whole number` |
| 11 | true help | 3 | `nova-check dogfood ledger --cli repo/docs/CLI.md --receipts scratch/receipts --max 1` -> `DOGFOOD OK verbs=173 dogfooded=0 by-nonauthor=0 open-edges=0 unfiled=0 unmatched=0` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-check spelling --file scratch/spelling.md --write --dry-run --json` | exit 0 and `status=ok` while `misspellings=1`; a dry-run finding looks successful | return the same finding status and exit as the write path, while keeping `written=0` | S |
| 2 | `nova-check dogfood ledger --cli repo/docs/CLI.md --receipts scratch/receipts --max 1` | `--max 1` still emits all 173 verb rows despite the help promise to bound listed items | apply the bound to the typed listing and emit its total | S |
| 3 | `nova-check links --dir scratch/broken --exclude ./scratch/broken/sub` | `excluded=0`, then the file is reported broken; the matching prefix `sub/a.md` works, but help does not say that prefixes are relative to `--dir` | state the path base for `--exclude` in help | S |
| 4 | `nova-check kernel` | both missing inputs are named, but the recovery command is only `nova-check help` | point to `nova-check kernel -h` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-check dogfood record -h` | the write flags do not take `--op`; a retry after a lost answer can append twice | consider adding an operation id and replay-safe result to each write | M |

## Top 3 fixes
1. Preserve failing status in spelling dry-runs.
2. Make `dogfood ledger --max` bound its output and report `MORE`.
3. State the `--exclude` path base in help.

## Good, keep
`quickstart` offers a small infrastructure-free run; the synthetic Markdown tree passes and names useful next checks.
`dogfood record --dry-run` and spelling dry-run did not alter scratch receipts or the Markdown hash. Unknown-flag refusals now name both bad flags, fixing a flaw described in earlier 1.2.0 ratings.
Refusals name missing kernel inputs together and give specific remedies for bad values and unknown flags, though the missing-input remedy still points only to global help.

## What could not be tried
`convergence` was not run: its help says it reads the forge through `gh`, including when it would write state; no remote service was accessed.
`floors` was not run because the staged checkout has no seed-document pair.
