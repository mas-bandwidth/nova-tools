# nova-table AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: d7e47f037ea8
Score: 8.5/10 (46/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> `TABLE DRY-RUN verb=create arg1=demo columns=ready,working,done sends="FCALL ns_table_create" redis=- dialled=0 written=0` |
| 2 | refusals | 5 | `nova-table frobnicate` -> `TABLE REFUSED: unknown verb "frobnicate"; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help` |
| 3 | output | 4 | `nova-table batch --dry-run --json '{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"create-b1","members":[{"id":"b1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":0}}]}'` -> `{"changes":1,"dialled":0,"dry_run":true,"epoch":"0","expected_table_revision":"2","guards":0,"members":1,"operation_id":"create-b1","redis":"-","sends":"FCALL ns_table_apply","table":"demo","written":0}` |
| 4 | dry-run | 4 | `nova-table row add demo build --dry-run` -> `TABLE DRY-RUN verb=row-add arg1=demo arg2=build sends="FCALL ns_table_row_add" redis=- dialled=0 written=0` |
| 5 | idempotent | 0 | `nova-table create demo --columns ready,working,done --op abc123` -> `CREATE REFUSED: unknown flag --op; the flags of create are --actor, --columns, --dry-run, --epoch, --epoch-field, --epoch-key, --fence, --footer, --idem, --member-prefix, --receipt, --redis, --width; run: nova-table help create` |
| 6 | no prompts | 5 | `n/a: no verb waits on input; the one REPL (shell) is entered explicitly and every other verb runs to completion` |
| 7 | exit codes | 4 | `nova-table list` -> `LIST REFUSED: --redis <addr> is required ...; run: d=$(mktemp -d) && ...` (exit 2), while help says `exit codes: 0 done, 1 refused, 2 usage` |
| 8 | one word | 5 | `n/a: no duplicate verbs or tools; subverbs (row add, col del, cell remove, view set) are grouped by their noun` |
| 9 | push | 5 | `n/a: no state is waited on; watch is a periodic renderer (redraws every --every), never a poll for a result` |
| 10 | no leaks | 5 | `nova-table list` -> refusal names the address and a throwaway store command, never a password value (seat password is named by variable or key, not printed) |
| 11 | true help | 4 | `nova-table create demo` -> `CREATE REFUSED: --columns wants the columns ...; run: nova-table help create` exits 2, though help's `exit codes` line says `1 refused`; the refusal/usage split is ambiguous |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-table create demo --columns ready,working,done --op abc123` | `CREATE REFUSED: unknown flag --op; ...` — there is no idempotent op; `--idem` is documented as `does not deduplicate`, so a retry after a lost receipt is unsafe | add a real `--op` that stores and replays the recorded result for the same op and args | M |
| 2 | `nova-table watch demo --out f.txt -h` | `watch` writes `--out` (one local file) but lists no `--dry-run` flag, so a publish cannot be previewed | give `watch` a `--dry-run` that plans the `--out` publish and writes nothing | S |
| 3 | `nova-table list --json` | `LIST REFUSED: unknown flag --json; the flags of list are --redis; run: nova-table help list` — only `batch` takes `--json`; the rest of the tool and the sibling nova tools accept it everywhere (an old defect from the 2026-10-02 ratings, still there) | accept `--json` on every verb, rendering the same result value | M |
| 4 | `nova-table create demo` | exits 2, but help prints `exit codes: 0 done, 1 refused, 2 usage`; a cold AI cannot tell which refusals are 1 and which are 2 without a store | reword the exit line to name the two refusal classes (usage vs the store's own no) | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-table help` example block | the bottom `example:` block (and README First run) needs a live Redis; a storeless `--dry-run` example already sits in the banner | move the storeless example to the example block so a paste runs with no store | S |
| 2 | `nova-table shell` | an extra interactive surface beyond the verbs, documented honestly | none; keep as is | - |

## Top 3 fixes
1. Add a real idempotent `--op` (criterion 5 is zeroed, the single largest score loss).
2. Make `--json` accepted on every verb (aligns with the sibling nova tools and the one-grammar standard).
3. Add `--dry-run` to `watch --out` (the one write with no dry-run).

## Good, keep
- The storeless `--dry-run` story is excellent: every write verb plans its exact FCALL with no store, and the banner hands a cold AI a runnable first run.
- Refusals uniformly name the cause and the exact next command, including a copy-paste throwaway-store command.
- Stable `<VERB> <STATUS> key=value` line grammar, and `batch` has a clean typed/JSON surface.

## What could not be tried
- Live store verbs (create, row/cell/col/member/write, show, render, check, watch, and the real receipt lines) need a Redis that this machine may not start, so they were judged from help, `-h` and `--dry-run` plans only; the exit-1 "store's own no" path (e.g. a stale-epoch refusal) could not be observed.
- The drop/create/set loop reported in the 2026-10-02 ratings (a plain `drop` followed by `create`/`set` refusals that do not name `drop --definition`) could not be re-checked without a live store.
