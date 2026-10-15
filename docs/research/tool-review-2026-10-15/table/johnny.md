# nova-table AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-table help` -> `nova-table: tables whose cells are ordered sets, kept in Redis and drawn as text` |
| 2 | refusals | 5 | `nova-table frobnicate` -> `TABLE REFUSED: unknown verb "frobnicate"; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help` |
| 3 | output | 4 | `nova-table member read demo m1 --json` -> `{"result":{"verb":"member read","status":"refused","exit":2,"remedy":"...","why":["..."]},"facts":{}}` |
| 4 | dry-run | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> `TABLE DRY-RUN verb=create arg1=demo columns=ready,working,done sends="FCALL ns_table_create" redis=- dialled=0 written=0` |
| 5 | idempotent | 2 | `nova-table help` -> `--idem (receipt metadata only; does not deduplicate retries: a second cell add of the same member is refused, naming the place it already sits` |
| 6 | no prompts | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> prints and exits without waiting |
| 7 | exit codes | 5 | `nova-table frobnicate; echo $?` -> `2`; `nova-table row add demo build --dry-run; echo $?` -> `0`; help states `exit codes: 0 done, 1 refused, 2 usage` |
| 8 | one word | 5 | `nova-table help` -> `help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version` |
| 9 | push | 5 | n/a: no state is waited on; `nova-table watch` redraws in place rather than polling |
| 10 | no leaks | 5 | `nova-table member read demo m1 --json` -> refusal JSON carries `why` and `remedy`, no credential material |
| 11 | true help | 4 | `nova-table help` -> exit table `0 done, 1 refused, 2 usage` matches observed exits (2 for unknown verb/flag/missing flag, 1 for semantic refusal, 0 for dry-run) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-table list --json` | `LIST REFUSED: unknown flag --json; the flags of list are --redis; run: nova-table help list` | wire --json into every verb through pkg/tool so an AI can ask for JSON uniformly (an old defect, 2026-10-02 ratings: reads have no --json, still there) | M |
| 2 | `nova-table help` | `--idem (receipt metadata only; does not deduplicate retries` | give --idem real deduplication by an op id, or rename it so the name does not promise idempotency it does not provide | M |
| 3 | `nova-table help` | `example: (the lines need the store the first run describes; this one runs with none)` | add a store-free example line (each write shown with --dry-run) so a cold first run succeeds without a store | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-table list`, `show`, `check`, `render`, `cell members`, `member find` | these read-only verbs refuse --dry-run; an AI that uniformly passes --dry-run hits a refusal | accept --dry-run on read-only verbs as ` no-op reflecting that they write nothing | S |
| 2 | `nova-table version --json` | `VERSION REFUSED: takes no arguments` | allow --json on version so a uniform JSON caller works | S |
| 3 | `nova-table help` example block | store-dependent example lines fail cold | print the verified dry-run transcript as the first example, keep the store transcript second | S |

## Top 3 fixes
1. Make --json a universal flag on every verb (blocker 1).
2. Give --idem real op-id deduplication, or rename it to disclaim idempotency (blocker 2).
3. Make the help example block run store-free with --dry-run lines (blocker 3).

## Good, keep
The refusal grammar is consistent and complete: `VERB REFUSED: <cause>; run: <remedy>` on every refusal, with the next command pasted. The exit-code table is documented in help and honored in practice (0/1/2). Every write verb has a --dry-run that prints the exact FCALL it would send with `dialled=0 written=0`, so a cold reader can preview any mutation safely. The typed `TABLE VERB key=value` line grammar is stable and parseable, with `--json` a real alternative where offered.

## What could not be tried
Live-store verbs (create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell) were not run end to end: starting a redis-server is forbidden in this environment. They were judged from `help`/`-h` and `--dry-run` (which dials nothing). The two real jobs, a success-path batch, and the JSON success shape of `member read` could not be exercised. `watch` was reviewed from help only.
