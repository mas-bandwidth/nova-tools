# nova-config AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-config` -> `nova-config REFUSED: no verb; want ...; run: nova-config help`; `nova-config help` then runs every `example:` line |
| 2 | refusals | 5 | `nova-config frobnicate` -> `nova-config REFUSED: unknown verb "frobnicate"; want ...; run: nova-config help` |
| 3 | output | 5 | `nova-config machine list --file try.json` -> `CONFIG LIST kind=machine rows=0` |
| 4 | dry-run | 5 | `nova-config migrate --file try2.json --dry-run` -> `CONFIG MIGRATE file=try2.json from=0 to=45 applied=0 dry_run=true pending=45`; try2.json not created |
| 5 | idempotent | 0 | `nova-config machine add -h` -> `(no --op flag; every write verb repeats the write on a retry)` |
| 6 | no prompts | 5 | `nova-config status --file try.json` -> `CONFIG STATUS file=try.json ...` returned without asking anything |
| 7 | exit codes | 5 | `nova-config inventory --fixture inv.yml --host nosuch` -> exit 1; `nova-config status --bogus` -> exit 2; matches the help table |
| 8 | one word | 5 | `nova-config kinds` -> `CONFIG KIND name=machine ...` one kind per line, no duplicate verbs |
| 9 | push | 5 | n/a: read verbs wait on no state; apply pushes to Redis |
| 10 | no leaks | 5 | `nova-config status --file try.json` -> `CONFIG STATUS ...` no secret or DSN password printed |
| 11 | true help | 5 | `nova-config inventory --example` -> printed the minimal fixture the help promised |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine add m1 --user nova --seat s1 --slots 8 --actor a1 --file try.json` | write verbs take no `--op`; a retry after a timeout re-applies the write and adds a second row off a new rev, so a resumed run is not safe | add a `--op <id>` flag to add, set, remove and apply, and make the same op id for the same verb and arguments return the recorded result and write nothing | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine self` | with no machine rows it still prints a bare hostname-derived token and exits 0, so a reader cannot tell it matched a row; help says 'nothing is marked local' but the line does not say so | print a `NOTE` on stderr saying the match basis (row name, or hostname label, or no row) | S |
| 2 | `nova-config kinds` | the items print `CONFIG KIND` while the summary prints `CONFIG KINDS`; consistent, but the summary could carry the count in the same field order | keep; no change needed except maybe name the count field `count` on every listing | S |
| 3 | `nova-config version` | the version line does not open with the verb name and status word (`VERSION OK`), unlike every other verb | prefix the line with `VERSION OK` | S |

## Top 3 fixes
1. Add `--op` idempotency to every write verb (add, set, remove, apply) — this alone moves criterion 5 from 0 to 5 and the score to 10/10.
2. Clarify `machine self` output with a NOTE naming the match basis.
3. Give `version` the standard leading `VERSION OK` status word.

## Good, keep
The refusal grammar is excellent: every refusal names the cause and the exact next command in one line (`run: nova-config ...`), and `machine show` with no name and no store reported both problems at once. The `help` banner is a complete cold start: what it does, how it works, and an `example:` block whose lines all ran as printed with no database (migrate, list, status, kinds, inventory). `--json` is offered and correct on every verb, and the typed line and JSON are the same value, so the two cannot drift.

The 2026-10-02 rating's main defect (a refusal under `--json` left stdout empty and printed plain text on stderr) is FIXED on the read path: `nova-config machine list --file try.json --max nope --json` exits 2 and prints the refusal as a JSON object on stdout with `result.status=refused`, `exit`, `why` and `remedy`, stderr empty.

## What could not be tried
apply, add, set and remove (store writes) were not run: this review is read verbs only per the card, so `--dry-run` for those was judged from help and from migrate's confirmed dry-run. The Redis-backed verbs (machine list/set/show with `--redis`, apply, status --redis) need a live Redis and were not run. `login`, `logout` and `loop run` write live state or start a process and were not run. `machine self` was run.
