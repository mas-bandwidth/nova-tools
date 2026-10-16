# nova-version AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-version example --out versions.tsv` -> `EXAMPLE OK wrote=versions.tsv entries=1 unchanged=false` |
| 2 | refusals | 5 | `nova-version diff` -> `DIFF REFUSED: --from is required; ... run: nova-version help` |
| 3 | output | 5 | `nova-version snapshot --bin ../bin --out bin.tsv` -> `SNAPSHOT OK bin=../bin out=bin.tsv tools=22 stamp=v1.2.26-0.20261015145010-cc2f2f8c8148` |
| 4 | dry-run | 4 | `nova-version snapshot --bin ../bin --out drybin.tsv --dry-run` -> `SNAPSHOT NOTE dry run: drybin.tsv not written`; but `nova-version snapshot --bin ../bin --dry-run` -> `SNAPSHOT REFUSED: missing --out` |
| 5 | idempotent | 5 | `nova-version example --out versions.tsv` (second run) -> `EXAMPLE OK wrote=versions.tsv entries=1 unchanged=true`; snapshot `--out` twice gives identical md5 |
| 6 | no prompts | 5 | `nova-version report --file versions.tsv` -> `REPORT OK checked=1 known=1 unknown=0 ...` (returns directly, no prompt) |
| 7 | exit codes | 5 | `nova-version diff` -> exit 2; `nova-version report --file bad.tsv` -> exit 1; matches the exit-code table in help |
| 8 | one word | 5 | `nova-version help` -> `the verbs are example, moved, snapshot, diff, report, send, version` (single words, no duplicates) |
| 9 | push | 5 | `nova-version report --file versions.tsv --draft --as friend1 --to friend2` -> prints the note; delivery is pushed via nova-bus, never polled |
| 10 | no leaks | 5 | `nova-version report --file versions.tsv` -> `REPORT TOOL name=go kind=tool version=1.26.6 ...` (no secret in any line) |
| 11 | true help | 4 | `nova-version snapshot --bin ../bin --dry-run` -> `SNAPSHOT REFUSED: missing --out` despite help saying `--dry-run ... write no --out` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-version snapshot --bin ../bin --dry-run` | Refused `SNAPSHOT REFUSED: missing --out; refusing to guess (...)` although the flag's own help says `--dry-run ... write no --out`; an AI that only wants the listing must guess a dummy `--out` path, and the remedy's `--file` suggestion counts adopted tools instead of listing a directory | With `--dry-run` make `--out` optional, or have the refusal say: give any path, nothing is written | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-version report --file versions.tsv --json` | report (and send) refuse `--json`, while every other verb offers it; the value has one rendering only | Let report and send render the same value as JSON | M |
| 2 | `nova-version moved --from 400d44e70 --to cc2f2f8c8148 --repo ../repo --out m.md --dry-run` | The OK line prints `added=0 deleted=0 renamed=0 verbs=220` and nothing says whether verbs counts checked or changed | Print the meaning beside the number | S |
| 3 | `nova-version report --file versions.tsv --draft` | Even a draft refuses without `--as` and `--to`, so the first attempt to preview a report stumbles | Let `--draft` print with placeholders unless given | S |

## Top 3 fixes
1. Let `snapshot --bin --dry-run` run without `--out` (removes the biggest first-run stumble).
2. Give report and send the same `--json` rendering as every other verb.
3. Explain the `verbs=N` count in a moved OK line.

## Good, keep
Refusals are exemplary: every one names the cause and the next command, and missing-flag refusals list every problem at once. The line grammar is stable (`PREFIX KEY=val`) with `--json` on every verb that reports facts, and exit codes are honored as documented (0 done, 1 UNKNOWN, 2 usage). The dry-run and `--draft` paths genuinely write nothing, and the example verb's second run is provably idempotent (`unchanged=true`).

## What could not be tried
`send` (and `report --send`) were not run: they deliver a note to the nova-bus service, which this review may not touch; they were judged from their help and the `--draft` path. `moved` was run with `--dry-run` only; the real `--out` write was not exercised. The github, npm, brew and ollama `latest` lookups were not tried (network).
