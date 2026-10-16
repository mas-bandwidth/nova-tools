# nova-table AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-table --help` -> `nova-table: tables whose cells are ordered sets, kept in Redis and drawn as text` |
| 2 | refusals | 5 | `nova-table frobnicate` -> `TABLE REFUSED: unknown verb "frobnicate"; the verbs are ...; run: nova-table help` |
| 3 | output | 4 | `nova-table batch --dry-run --json '{...}'` -> `{"changes":1,"dialled":0,"dry_run":true,...,"written":0}` |
| 4 | dry-run | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> `TABLE DRY-RUN verb=create arg1=demo ... dialled=0 written=0` |
| 5 | idempotent | 0 | `nova-table help create` -> `--idem <string>  attempt identifier recorded with the change; does not deduplicate` |
| 6 | no prompts | 5 | `nova-table create demo --columns x --bogus` -> `CREATE REFUSED: unknown flag --bogus; ... run: nova-table help create` (returns at once, nothing waits) |
| 7 | exit codes | 5 | `nova-table frobnicate; echo $?` -> `2`; `nova-table create demo --columns ready,working,done --dry-run; echo $?` -> `0` |
| 8 | one word | 4 | `nova-table --help` -> unique verb list, but `show` and `render` both draw a table (overlap) |
| 9 | push | 5 | n/a: inspection reads the store; nothing waits on pushed state |
| 10 | no leaks | 5 | `nova-table create demo --columns ready,working,done --dry-run` -> `TABLE DRY-RUN ... redis=- dialled=0 written=0` |
| 11 | true help | 5 | `nova-table create demo --columns ready,working,done` -> `CREATE REFUSED: --redis <addr> is required ...; run: d=$(mktemp -d) ...` matches help's refusal description |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-table help create` | writes carry `--idem` "does not deduplicate", so a retried write duplicates the row/cell; the one apparent idempotency knob does not deduplicate | make `--idem` actually deduplicate a retried op (or add a real `--op` that leaves a repeat unchanged) | L |
| 2 | `nova-table show demo --json` | the read verbs (show, list, render, check, member find, cell members, view show, view list) refuse `--json`; only batch takes it, so an AI must parse text tables (old defect from the 2026-10-02 rating, still there) | render the same result value as JSON from every verb | M |
| 3 | `nova-table create` | with both the table name and `--columns` missing it names only the table, so recovery takes two turns (old defect from the 2026-10-02 rating, still there) | report every missing input in one refusal | S |
| 4 | `nova-table show demo` vs `nova-table render demo` | two verbs both draw a table; a new AI cannot tell which to use | document the difference in both helps, or alias one to the other | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-table help batch` | the example lists `nova-table show demo` and `member read demo b1` before the JSON manifest, which reads like unrelated instructions | trim the example to the manifest only | S |
| 2 | `nova-table help shell` | the `shell` verb opens an interactive REPL, a prompt a pipeline would hang on | note in help that `shell` is interactive only | S |

## Top 3 fixes
1. Give writes real idempotency (make `--idem` deduplicate, or add `--op`) so retries do not duplicate state.
2. Add `--json` to the read-only inspection verbs so AI output is parseable without text scraping.
3. Make `create` report every missing input in one refusal.

## Good, keep
- The no-store refusal names the exact cause, the address it tried, and a copy-paste throwaway Redis command with exit 2, which is a model for every verb.
- `--dry-run` exists on every write verb and provably dials/writes nothing (`dialled=0 written=0`), and refuses exactly where the real run refuses before sending.
- Help is exhaustive and consistent: flags, connection precedence, epoch/receipt semantics, exit codes (0/1/2) and effect are documented per verb and all claims verified true when run.
- Old defects from the 2026-10-02 rating are largely fixed: every write now has `--dry-run`; refusals carry the `VERB REFUSED:` status word; a bad flag-value names the verb's own help (`run: nova-table help cell add`).

## What could not be tried
- Every store verb (create/set/drop/list/row/col/cell/member/batch/check/clear/show/render/watch/view) needs a Redis 7 store; a live store may not be started on this machine, so no real table was created or read. Judged from help and `--dry-run` only.
- `shell` (interactive REPL) and `watch` (long-running drawing loop) not run; judged from help and `--dry-run`.
- `--json` honoured only where offered (`batch`); other verbs refuse it.
