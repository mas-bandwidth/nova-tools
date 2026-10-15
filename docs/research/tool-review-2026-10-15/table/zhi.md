# nova-table AI-usability review, 2026-10-15

Reviewer: z-ai/glm-5.3
Build: af87d7fc7a0d
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> `TABLE DRY-RUN verb=create arg1=demo columns=ready,working,done sends="FCALL ns_table_create" redis=- dialled=0 written=0` |
| 2 | refusals | 4 | `nova-table create` -> `CREATE REFUSED: wants one table name: create <table> --columns <name[:projection[:fold[:label]]],...>; run: nova-table help create`; the also-missing --columns waits for a second run |
| 3 | output | 4 | `nova-table list --json` -> `LIST REFUSED: unknown flag --json; the flags of list are --redis; run: nova-table help list`; --json answers on batch and member read only |
| 4 | dry-run | 5 | `nova-table drop demo --dry-run` -> `TABLE DRY-RUN verb=drop arg1=demo sends="FCALL ns_table_drop" redis=- dialled=0 written=0` |
| 5 | idempotent | 2 | `nova-table shell -h` -> `--idem <string>  attempt identifier recorded with the change; does not deduplicate` |
| 6 | no prompts | 5 | `nova-table shell --dry-run` -> `SHELL REFUSED: --redis <addr> is required (or a configured seat); run: nova-table help shell`; it exits at once, no verb waits on an answer, and the shell reads stdin lines |
| 7 | exit codes | 5 | `nova-table frobnicate` -> `TABLE REFUSED: unknown verb "frobnicate"; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help` at exit 2, as every -h table states; dry-runs exit 0, manifest refusals 1 |
| 8 | one word | 5 | `nova-table` -> `TABLE REFUSED: no verb given; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help`; eighteen one-word verbs, no duplicates |
| 9 | push | 4 | `nova-table help watch` -> `--out <string>  publish to this file by atomic rename instead of drawing in place`; a push, but store-bound, so untried here |
| 10 | no leaks | 5 | `NOVA_SPRINT_REDIS_USER=someuser NOVA_SPRINT_REDIS_PASSWORD_ENV=SECRETPW123 nova-table list --redis 127.0.0.1:1` -> `LIST REFUSED: redis at 127.0.0.1:1 as user someuser (password from SECRETPW123): unreachable: dial tcp 127.0.0.1:1: connect: connection refused; ...`; the variable is named, its value never prints |
| 11 | true help | 4 | `nova-table help` -> `render <table> prints the table and nothing else, nothing at all when it is empty`, while the render row in docs/CLI.md says an empty table prints its header and footer; one claim is false and no store existed here to run it |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-table shell -h` | `--idem <string>  attempt identifier recorded with the change; does not deduplicate`; a write retried after a timeout is not made safe by the recorded id, while batch alone replays by operation_id | deduplicate a repeated --idem by its recorded receipt, as batch already does | M |
| 2 | `nova-table list --json` | `LIST REFUSED: unknown flag --json; the flags of list are --redis; run: nova-table help list`; every read but member read must be parsed as drawn text, against the family promise that every verb answers --json | encode the one value each verb already renders when --json is asked | M |
| 3 | `nova-table create` | `CREATE REFUSED: wants one table name: create <table> --columns ...`; with the name and --columns both missing, --columns is unmentioned until the second run | name every missing input in one refusal | S |
| 4 | `nova-table batch --dry-run '{"schema":1}'` | `BATCH REFUSED: table "" wants letters, digits, _ . and -; ...; code=NAME` and the next run names operation_id; a manifest is fixed one fault per run | name every missing or invalid key in one refusal | S |
| 5 | `nova-table help` | `render <table> prints the table and nothing else, nothing at all when it is empty`, but docs/CLI.md's render row says an empty table prints its header and footer; one of the two docs is wrong | make the banner say what the renderer prints for an empty table, agreeing with the other doc | S |
| 6 | `nova-table shell --dry-run` | `SHELL REFUSED: --redis <addr> is required (or a configured seat); run: nova-table help shell`; the one store verb whose no-store remedy omits the address env names and the no-redis-server note every other verb prints | print the same long no-store refusal as every other store verb | S |
| 7 | `nova-table help -h` | `HELP REFUSED: unknown verb "-h"; the verbs are ...; run: nova-table help` at exit 2 (help --json the same), while `nova-table -h` answers the banner at exit 0 | answer -h and --help on help as the root help at exit 0 | S |
| 8 | `nova-table help create` | the banner says `drop keeps the saved column definition and the table's identity unless --definition is given`, but neither create's page nor set's names `drop --definition` as the exit from a definition that survived a plain drop, so the create-set remedies ping-pong; read from help, not run (still there since the 2026-10-02 ratings) | name `nova-table drop <table> --definition` in create's and set's help as the way out | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-table list --bogus` | the refusal lists the verb's flags but names no nearest one; an unknown verb names no nearest verb either | append a did-you-mean to unknown-name refusals | S |
| 2 | `nova-table show -h` | the flags list prints `--at-epoch <string>` and cell add prints `--score <string>`, where the usage lines and the value refusals say a number | print `<n>` for numeric flags in the flags list | S |
| 3 | `nova-table member read demo m1 --json` | the refusal JSON ends `"remedy":"nova-table help"`, the generic page, not the verb's own | name `nova-table help member read` as the remedy | S |
| 4 | `nova-table frobnicate` | refusals print on stderr while results and --json print on stdout, and no help line says which stream is which | one banner line naming the stream of each kind | S |

## Top 3 fixes
1. Deduplicate a repeated --idem by its recorded receipt, so a retried write is safe (criterion 5 stands at 2 of 5).
2. Answer --json on every verb from the value each already renders, so reads are program-consumable.
3. Name every missing input in one refusal, create's name and columns, batch's every key, so recovery takes one turn.

## Good, keep
- The store-free --dry-run on every write verb: the same one-line plan, `dialled=0 written=0`, so a cold AI can trial every write with nothing at risk.
- One refusal grammar everywhere: the cause, what the input wants, and a runnable next command; effect lines name inspection, store write and local file honestly.
- The password probe names the variable and never its value; and since the 2026-10-02 ratings the --dry-run on every write, the REFUSED status word and the per-verb value remedies have arrived, while the --json gap, the one-problem-per-turn refusals, the no-nearest hints and the drop definition dead end remain (blockers 2 to 4 and 8, nice-to-have 1).

## What could not be tried
Every store-backed verb: the real create, set, drop, row, col, cell, member and clear writes, batch's real apply and replay, list, show, render, check, cell members, member find, member read's happy path, the view verbs, watch and shell, because starting a store on this machine is forbidden and none was reachable (no redis-server is on PATH); each is judged from its help and its --dry-run plan. The store-refused exit 1 path is likewise untried, and the empty-render question that would decide which of the two docs is right could not be run.
