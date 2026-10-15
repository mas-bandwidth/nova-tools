# nova-table AI-usability review, 2026-10-15

Reviewer: x-ai/grok-build-0.1
Build: d07b465f364e
Score: 9/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> `TABLE DRY-RUN verb=create arg1=demo columns=ready,working,done sends="FCALL ns_table_create" redis=- dialled=0 written=0` |
| 2 | refusals | 5 | `nova-table foobar` -> `TABLE REFUSED: unknown verb "foobar"; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help` |
| 3 | output | 4 | `nova-table batch --dry-run --json '{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"o","members":[{"id":"b1","expect":{"absent":true},"create":{"row":"r","col":"c","score":1}}]}` -> `{"changes":1,"dialled":0,"dry_run":true,"epoch":"0",...}` ; lines `TABLE *` or render ascii; --json only where offered |
| 4 | dry-run | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> `TABLE DRY-RUN ... dialled=0 written=0` (n/a for reads: no write surface) |
| 5 | idempotent | 2 | `nova-table help` -> `--idem (receipt metadata only; does not deduplicate retries: a second cell add of the same member is refused, naming the place it already sits` |
| 6 | no prompts | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> prints and exits; no verb waits for stdin answer |
| 7 | exit codes | 5 | `nova-table foobar; echo $?` -> `2`; `nova-table create demo --columns x --dry-run; echo $?` -> `0`; `nova-table batch --dry-run --json '<bad>' ; echo $?` -> `1`; help states `exit codes: 0 done, 1 refused, 2 usage` |
| 8 | one word | 5 | `nova-table help` -> verbs `help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version` (distinct, no dups) |
| 9 | push | 5 | n/a: no state waited on in normal verbs; watch redraws from store pushes |
| 10 | no leaks | 5 | `nova-table list` -> `LIST REFUSED: --redis <addr> is required ... run: d=$(mktemp -d) ...` (names addr tried, no password or secret) |
| 11 | true help | 4 | `nova-table help` example block lines require a store (first run section gives one dry-run line); observed exits and dry-run behaviour match stated; some --json shape differs on dry success |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-table list --json` | `LIST REFUSED: unknown flag --json; the flags of list are --redis; run: nova-table help list` | support --json (the standard result envelope) on every verb, including all reads and version | S |
| 2 | `nova-table help` | `--idem (receipt metadata only; does not deduplicate retries` | implement deduplication for --idem (an op id that returns prior receipt on replay) or rename the flag so its name matches what it does | M |
| 3 | `nova-table help` (the example: block) | `nova-table create demo --columns ready,working,done` (and following lines) | make every line in the example: block runnable cold (use --dry-run for writes; a second store-free transcript after the note) | S |
| 4 | `nova-table create gg --columns y` (after plain drop) | `CREATE REFUSED: ...; run: nova-table set 'gg' --columns ...` (set then names create; neither names --definition) | name `drop 'gg' --definition` in the refusal, or let create replace a definition no live table holds | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-table list`, `show`, `check`, `render`, `cell members`, `member find` | these read-only verbs refuse --dry-run; an AI that uniformly passes --dry-run hits a refusal | accept --dry-run on read-only verbs as no-op (reflecting that they write nothing) | S |
| 2 | `nova-table version --json` | `VERSION REFUSED: takes no arguments; run: nova-table help version` | allow --json (and other standard flags that do not affect) on version | S |
| 3 | `nova-table batch --dry-run --json ...` (success path) | emits flat object `{"changes":1,"dry_run":true,...}` not the `{"result":{...}}` envelope used for refusals | make --json dry-run plan use the same result wrapper as other --json cases for uniform parsing | S |

## Top 3 fixes
1. Wire --json into every verb (blocker 1; raises output and true help).
2. Make --idem deduplicate by op id, or rename flag (blocker 2; raises idempotent).
3. Make the example: block run cold with --dry-run writes (blocker 3; raises true help and cold).

## Good, keep
The refusal grammar is uniform: every refusal is one line `VERB REFUSED: <cause>; run: <remedy>` naming cause, wants, and a pasteable next command. Exit codes match the documented table (0/1/2). Every write has --dry-run that runs the checks and emits `dialled=0 written=0` with no side effects. Formulas compute exactly. The throwaway store recipe in help works when followed. (Corroborates 2026-10-02 ratings on refusal quality, dry-run safety, trips=, and exact folds.)

## What could not be tried
All verbs needing a live Redis store (the full success paths for create/set/drop/row/col/cell/member/batch/clear/show/check/render/watch/view/list/cell-members/member-find, and interactive shell/watch loops): no server may be started and no live table written (per card). They were judged only from `help`, each verb's `-h`, and `--dry-run` (for writes). Real idempotent replays, batch receipts on live, silent no-op cases, and member cross-table behaviour not exercised. `shell` and continuous `watch` only from help.
