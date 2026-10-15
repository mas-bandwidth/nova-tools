# nova-table AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.0/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-table help` -> `nova-table: tables whose cells are ordered sets, kept in Redis and drawn as text` |
| 2 | refusals | 5 | `nova-table frobnicate` -> `TABLE REFUSED: unknown verb "frobnicate"; the verbs are ...; run: nova-table help` |
| 3 | output | 4 | `nova-table batch --dry-run '<manifest>' --json` -> `{"changes":1,"dry_run":true,"redis":"-","dialled":0,"written":0}`; but `nova-table list --json` -> `LIST REFUSED: unknown flag --json` |
| 4 | dry-run | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> `TABLE DRY-RUN verb=create ... dialled=0 written=0` |
| 5 | idempotent | 2 | `nova-table help create` -> `--idem <string> attempt identifier recorded with the change; does not deduplicate` |
| 6 | no prompts | 5 | `nova-table help` -> `With no store at all, help and -h answer`; no verb waits on an interactive answer |
| 7 | exit codes | 5 | `nova-table frobnicate` -> `EXIT:2`; matches help `0 done, 1 refused, 2 usage` |
| 8 | one word | 5 | `nova-table help` -> `the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version` |
| 9 | push | 4 | n/a: no state-wait primitive exists to observe; watch is a redraw display, not a poll, and could not be run without a store |
| 10 | no leaks | 5 | `NOVA_SPRINT_REDIS_PASSWORD_ENV=supersecretpw nova-table list` -> output prints `(the password's variable is not shown)` and never the value |
| 11 | true help | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> `dialled=0 written=0`; the dry-run claim in help holds |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-table list --json` | `LIST REFUSED: unknown flag --json; the flags of list are --redis; run: nova-table help list`; the platform standard promises every verb accepts --json, but only batch and member read do, so an AI consuming a read must parse drawn text | build the one value each verb already renders and encode it when --json is asked | M |
| 2 | `nova-table help create` | `--idem <string> attempt identifier recorded with the change; does not deduplicate`; a write retried with the same --idem applies again, so a timed-out write can double-apply | make --idem deduplicate repeated ops by the recorded receipt, or drop the platform's op-id idempotency claim | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-table create` | reports `wants one table name` but not the also-missing `--columns` in the same refusal, so a caller fixes one fault per attempt | report every missing or invalid required flag in one refusal | S |
| 2 | `nova-table help --json` | treats `--json` as a verb name and prints help instead of refusing or answering | treat an unknown argument on help as a refusal naming the valid verbs | S |

## Top 3 fixes
1. Add --json to every verb from the one result value, so reads are program-consumable.
2. Make --idem actually deduplicate repeated ops, so a retried write cannot double-apply.
3. Name every missing input in one refusal, so a cold AI fixes the call once.

## Good, keep
- Every refusal names its cause and a runnable next command; --dry-run works with no store and writes nothing (dialled=0 written=0), so a new AI can trial every write verb cold.
- Clean typed line grammar and JSON on batch and member read; the password's variable is never shown, so no leak.
- Honest disclaimers: --idem does not deduplicate, receipts and epoch staleness are stated plainly.

## What could not be tried
Every store-backed verb (list, show, render, watch, check, cell members, member find and read, and real create, set, drop, row, col, cell, view, batch writes, and shell) because no Redis server may be started on this machine; each is judged from its help and its --dry-run. The real exit code 1 (store said no) is likewise untested here.
