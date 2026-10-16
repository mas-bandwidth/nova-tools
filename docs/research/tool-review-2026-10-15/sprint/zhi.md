# nova-sprint AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: b4070856f5c0
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-sprint help` -> `first run: no Redis needed; the store is the file sprint.twin` |
| 2 | refusals | 5 | `nova-sprint add` -> `REFUSED: wants --stream and either ids, --count <n> or --sentinel <id>; run: nova-sprint add -h` |
| 3 | output | 5 | `nova-sprint where --json` -> `{"all":2,"archived_cards":0,"archived_landed":0,"at":"..."}` |
| 4 | dry-run | 3 | `nova-sprint rebase --from sprint/a --to sprint/b --dry-run` -> exit 0; `nova-sprint drop card1 --dry-run` -> `REFUSED: unknown flag --dry-run` though help shows it |
| 5 | idempotent | 5 | `nova-sprint add --stream s1 --brief-file card1.md --one --op op1` (twice) -> `ADD OK ... op=op1-1 replay=yes`, card count unchanged |
| 6 | no prompts | 5 | every verb ran to completion on flags alone; no interactive wait seen |
| 7 | exit codes | 5 | `nova-sprint add` (bad value) -> exit 2; `nova-sprint add` (ok) -> exit 0 |
| 8 | one word | 4 | help verb list has both `sentinel` and `sentinels`, `logged` and `verify-landed`, `check`/`repair`/`fsck`/`log` overlap |
| 9 | push | 4 | n/a: live-store push/poll not observable against the read-only store; judged from help only |
| 10 | no leaks | 5 | no error or output line in any run leaked a secret |
| 11 | true help | 3 | `nova-sprint help drop` -> usage `[--dry-run]`, but `nova-sprint drop --dry-run` -> `REFUSED: unknown flag --dry-run` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint drop --dry-run` | `REFUSED: unknown flag --dry-run` although `nova-sprint help drop` usage advertises `[--dry-run]` | implement --dry-run for drop, or drop it from the usage line | S |
| 2 | `nova-sprint add --brief ...` with a short brief | first successful write needs the brief to quote every general-rule sentence verbatim; help sends the user to another tool (`nova-swarm template`) for the required text | print the six default-rule sentences inline in `add -h` (or ship a passing example brief) | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `press` / `release` / `return` / `rework` selectors | `--dry-run` is inconsistent across write verbs (present on some, missing on others, advertised-but-absent on drop) | unify a `--dry-run` across every write verb | M |
| 2 | verb list | near-duplicate and overlapping verbs (`sentinel`/`sentinels`, `check`/`repair`/`fsck`/`log`) | rename or alias to one word per thing | M |
| 3 | `where --json` | JSON keys are not documented against the view columns | document the JSON key set where --json is offered | S |
| 4 | `add` lint | the refusal lists each missing sentence but the exact demanded phrasing is only in a separate tool | echo the verbatim required sentences in the refusal | S |

## Top 3 fixes
1. Implement (or un-advertise) `--dry-run` on `drop`; it is shown in help but refused, failing both true-help and dry-run criteria.
2. Make `add` self-contained for the first run by printing the default rule sentences (or a passing example) in its own help instead of sending the user to `nova-swarm`.
3. Unify `--dry-run` and `--json` coverage across all write verbs so a caller never has to guess which verb supports them.

## Good, keep
- Refusals are excellent: every one names the cause and the exact next command, and exit codes (0/1/2) match the help.
- `--op` idempotency is real and verified: replaying the same op returns the recorded result (`replay=yes`) and writes nothing new.
- The help is large but well-structured: `nova-sprint help <verb>` gives usage, examples, flags and exit codes, and the `mem:sprint.twin` first-run path needs no Redis.

## What could not be tried
- Live-store write verbs (`take`, `start`, `finish`, `progress`, `read`, `accept`, `return`, `hold`, `release`, `land`, `merge`, `tick` and friends) were not run against a live store per the card's constraint; they were judged from help and `--dry-run` only.
- Full `selftest` suite was not completed (a first attempt refused on a missing scratch dir; exit 2).
- No Redis server was started, so push-vs-poll behavior and server verbs were not exercised.
