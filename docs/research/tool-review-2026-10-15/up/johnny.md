# nova-up AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-up` -> `UP REFUSED: no verb and no file given; the verbs are up, version; run: nova-up help`; `nova-up help` example lines run as printed |
| 2 | refusals | 5 | `nova-up frobnicate` -> `UP REFUSED: "frobnicate" is no verb and no file; the verbs are up, version, and a file is given by its path (./frobnicate); run: nova-up help` |
| 3 | output | 5 | `nova-up up --local --dry-run --json` -> `{"result":{"verb":"up","status":"failed","exit":1,"why":["missing: binaries; nothing was applied..."]},"facts":{...},"payload":"..."}` |
| 4 | dry-run | 5 | `nova-up up --local --dry-run --root ./nova-try` -> `UP dirs create .../nova-try: . stores keys logs smoke`; scratch dir left empty |
| 5 | idempotent | 2 | `nova-up up -h` flags list has no `--op`; the write verb `up` has no op-id surface, so op-idempotency is unprovable and a retry is not made safe by an id |
| 6 | no prompts | 5 | `nova-up up --seat --dry-run --sprint-redis 127.0.0.1:6380 --server 127.0.0.1:6390 --bus-redis 127.0.0.1:6380` -> `UP OK fields=3 dry_run=true`, no interactive wait |
| 7 | exit codes | 5 | `nova-up version` -> exit 0; `nova-up up --local --dry-run --root ./nova-try` -> exit 1; `nova-up up` -> exit 2 (matches help's 0/1/2 table) |
| 8 | one word | 5 | `nova-up help` -> `the verbs are up, version`; two verbs, no duplicates |
| 9 | push | 5 | n/a: a setup tool; it writes a plan and applies, it never waits on polled state |
| 10 | no leaks | 5 | `nova-up up --local --dry-run --json` payload names no password value (passwords sealed, never printed), no key material |
| 11 | true help | 5 | `nova-up --local --dry-run --root ./nova-try` -> exit 1 exactly as help says for `a step is missing`; `nova-up version` exits 0 |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-up up -h` | the flags of `up` carry no `--op <id>`; a write verb without an op id gives no handle for a safe retry, so the idempotency criterion has no surface and a timed-out apply cannot be replayed | add `--op <id>` to `up` and record/replay per id as the standard's other writers do | M |
| 2 | `nova-up up --seat --dry-run` | a refusal prints `UP NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` though nothing is written, alarming a dry-run reader who asked for nothing to be touched | suppress the NOTE on a refusal (or only emit it when a write was actually attempted) so a dry run never implies a write | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-up version --json` | `{"result":{...},"facts":{},"payload":"nova-up v..."}` keeps the version only in `payload`, so the JSON object has no first-class version fact | expose `version` as a `facts` key | S |
| 2 | `nova-up help` | the usage line mixes modes with `|` in one block while the verbs line says the verbs are `up, version`; a cold reader must infer the bare form is `up` | add one usage line spelling the bare `up` form and its aliases | S |

## Top 3 fixes
1. Add `--op <id>` to the `up` verb (moves the score most; unlocks criterion 5).
2. Fix the `--seat --dry-run` refusal's `it may have written` NOTE so a refusal never implies a write.
3. Give `version --json` a first-class `version` fact key.

## Good, keep
- Every refusal names its cause and the next command in one line (`run: nova-up help` / `run: nova-up up -h`), and `--seat`+`--local` refusals each name the missing flags and the fix.
- Both write modes honor `--dry-run` and write nothing (verified by an empty scratch tree after dry runs).
- Exit codes match the documented table exactly (0/1/2 all triggered and observed), and the plan-first output (one `UP <step>` line before any apply) makes the dry run genuinely readable.
- No prior nova-up rating exists in docs/ratings (the 2026-10-02 set covers nova-update, not nova-up), so no old defect was carried forward.

## What could not be tried
- Applying `up --local` (would install binaries and start a systemd user unit) and applying `up --seat` (would write a real seat env): this card is plan-only, so both modes were judged from their help and `--dry-run` plans only.
- The `redis` step's unit-create/ACL-apply and the `smoke` step's card landing: plan lines only, not run, for the same reason.
