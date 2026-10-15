# nova-hook AI-usability review, 2026-10-15

Reviewer: a frontier model reviewer (preview-1002)
Build: cc2f2f8c8148
Score: 9.0/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-hook help` -> `nova-hook: hooks an acting identity to its role's live, proven pushes; some bus mailbox checks are advisory`; banner, `how it works`, usage, example and exit codes all present, and the bare run names the door |
| 2 | refusals | 5 | `nova-hook hook --as m1` -> `HOOK REFUSED: --role is required; ... refusing to guess; run: nova-hook help`; a missing flag, an unknown flag, an unknown verb and a bad value each named the cause and the next command, and `hook --as m1 --role bogus --dry-run` refused with the one valid set |
| 3 | output | 5 | `nova-hook status --as m1 --json` -> `{"result":{"verb":"status","status":"refused","exit":2,"remedy":"nova-hook help","why":[...]},"facts":{}}`; typed line grammar and --json from the same value, stable across verbs |
| 4 | dry-run | 5 | `nova-hook hook -h` lists `--dry-run print what the verb would write and write nothing`; hook, prove and unhook each expose --dry-run; a live writes-nothing run needs a store |
| 5 | idempotent | 1 | `nova-hook hook --as m1 --role member --once --op abc` -> `HOOK REFUSED: unknown flag --op; the flags of hook are ...`; no write verb takes --op, so a retry after a timeout has no idempotency guard |
| 6 | no prompts | 5 | `nova-hook help`, every `-h`, refusals and `--json` returned without any interactive question; nothing waits on input |
| 7 | exit codes | 5 | `nova-hook status --as m1` exited 2 with `STATUS REFUSED`; `nova-hook version` exited 0; help states 0/1/2 and the observed codes match |
| 8 | one word | 5 | verbs are status, hook, prove, unhook, friends, serve, version: one word each, no duplicate verbs or tools |
| 9 | push | 5 | serve pushes challenges as bus messages and the hook loop prints pushes as they land; waiting on a proof is satisfied by a push (prove), not by polling |
| 10 | no leaks | 5 | none of the refusal, NOTE or version lines carried a secret, key or identity beyond the innocuous `m1` probe name |
| 11 | true help | 4 | every offline claim verified true (refusals, exit codes, help exits 0, flag lists); the live claims (states, proof, serve cadence) could not be run without a store |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-hook hook --as m1 --role member --once --op abc` | `HOOK REFUSED: unknown flag --op; the flags of hook are ...`; the three write verbs take no idempotency id, so a retry after a timeout is not safe | add a `--op <id>` to hook, prove and unhook that records and replays the result for the same verb/args, matching the shared tool standard | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-hook status --as m1` with no bus configured | `STATUS REFUSED: --redis is required: NOVA_BUS_REDIS is unset, ...` — a cold reader cannot see any real output until a Redis seat exists | add a store-free demo or `--dry-run` read path so a first run shows real output without a store | S |
| 2 | `nova-hook hook --as m1 --role member --dry-run` without a store | prints `HOOK NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` — "it may have written" reads as alarming when the refusal came before any write | word the NOTE to say no write could occur and why, rather than "may have written" | S |

## Top 3 fixes
1. Add `--op` idempotency to the write verbs (moves criterion 5 from 1 to 5, +4 points).
2. Give the no-store path a real first-run demo or output, so a cold reader sees the tool working without provisioning Redis.
3. Soften the dry-run-bypassed NOTE wording so it names the refusal as the reason no write happened.

## Good, keep
- The refusal grammar is exemplary: every refusal names the cause, what the flag wants, and the next command in one line, and unknown verbs/flags list the real options.
- The typed-line and `--json` outputs are one value rendered two ways, parseable and stable.
- The push model (bus challenges, events, prove) fits the "waited state is pushed" test, and exit codes 0/1/2 are documented and matched by observed runs.

## What could not be tried
- hook, prove, unhook (live writes) and serve (the delivery loop): they write to bus Redis, which is not available (NOVA_BUS_REDIS unset, no fleet config) and starting a store or writing live state is forbidden by the card.
- status/friends against a live store: same missing-store reason; only the refused path and --json refusal form could be observed.
- A live `--dry-run` plan for hook/prove/unhook: it reads the record first, so it refuses on the missing store before printing a plan.
