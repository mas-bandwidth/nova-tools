# nova-up AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-up --local --dry-run --root ./nova-try` -> `UP FAILED ... missing: binaries; nothing was applied; install what the missing line names and run again` |
| 2 | refusals | 5 | `nova-up --seat --dry-run --sprint-redis notanaddr` -> `UP REFUSED: missing fleet endpoints ...; run: nova-up help` |
| 3 | output | 5 | `nova-up --local --dry-run --json --root ./nova-try` -> `{"result":{"verb":"up","status":"failed","exit":1,...` |
| 4 | dry-run | 5 | `nova-up --local --dry-run --root ./nova-try` -> scratch dir empty after run |
| 5 | idempotent | 3 | `nova-up up -h` -> flags list no `--op`, so a retry cannot be made safe by op id |
| 6 | no prompts | 5 | every probe ran non-interactively to completion |
| 7 | exit codes | 5 | `nova-up --local --dry-run` -> exit 1; `nova-up up` -> exit 2; `nova-up version` -> exit 0 |
| 8 | one word | 5 | `nova-up help` -> `the verbs are up, version` (no duplicates) |
| 9 | push | 5 | n/a: up applies steps in order and starts a unit; no polling loop exposed |
| 10 | no leaks | 5 | output `: the loopback Redis ACL passwords sealed, never printed` |
| 11 | true help | 5 | `nova-up --seat --dry-run` -> `UP OK fields=3 dry_run=true` (help's `--seat needs no external programs` holds) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-up up -h` | the only write verb `up` takes no `--op`, so a timed-out retry cannot replay a recorded result; the cross-tool standard says every write verb takes an op id | accept `--op <id>` and return the recorded result for a repeated id | S |
| 2 | `nova-up --seat --dry-run --sprint-redis notanaddr` | a refusal prints `NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` — alarming and false, a refused verb writes nothing | reword the note to say a refusal writes nothing, or drop it | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-up help` example block | the `--seat` example has no `--dry-run`; an AI pasting it verbatim applies a seat profile | add `--dry-run` to the seat example line so a first paste never writes | S |

## Top 3 fixes
1. Add `--op` to the `up` verb for safe retries (moves criterion 5 from 3 to 5).
2. Reword the refusal-time dry-run NOTE so it never implies a write happened.
3. Add `--dry-run` to the help's `--seat` example so a first run from help never applies.

## Good, keep
- A missing external program stops the run before anything is written, names each missing program, and prints one copy-paste install command; nothing is half-applied.
- One output shape end to end: `UP <step> <status> <detail>` lines and the same value as `--json` on every verb, so the two renderings cannot drift.
- Every refusal names its cause and the next command in one line, and exit codes 0/1/2 behave exactly as the banner states.

## What could not be tried
- Actually applying `nova-up --local` or `--seat`: the card is plan-only, never apply, and a real run writes files and starts a systemd unit.
- The help's claim that a second run `prints ok on every line and UP UNCHANGED`: needs an applied machine to verify.
- The `secrets`, `redis`, `seat` and `smoke` apply steps against a live seat/redis/twin: they require the full install, which the binaries gate stops.
