# nova-tokens AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` -> `TOKENS OK days=1 rows=1 sources=1 unreadable=0`, after pasting the help's setup and example lines verbatim |
| 2 | refusals | 5 | `nova-tokens frobnicate` -> `TOKENS REFUSED: unknown verb "frobnicate"; the verbs are fold, report, ledger, sum, check, sources, profiles, session, version; run: nova-tokens help`; a missing-flag run named all its missing flags at once, each with a run: door |
| 3 | output | 5 | `nova-tokens check --out ./out --json` -> `{"result":{"verb":"check","status":"ok","exit":0},...}`; lines are a stable key=value grammar and --json is the same value |
| 4 | dry-run | 5 | `nova-tokens fold ... --dry-run` -> `TOKENS DAY ... written=false would_write=true`; the --out directory stayed empty after it |
| 5 | idempotent | 0 | `nova-tokens fold -h` -> flags list holds no `--op`; fold, session, report --note and ledger write with no op id, so a retry after a timeout cannot be told from a second write |
| 6 | no prompts | 5 | `nova-tokens help` -> banner printed and exited 0, no interactive prompt; every run completed unattended |
| 7 | exit codes | 5 | `nova-tokens check --out ./emptyout` -> `CHECK FAILED looked at nothing...` exit 1, matching the help's table (0 pass, 1 failed, 2 could not run); refusals exit 2 |
| 8 | one word | 5 | `nova-tokens help` -> usage lists nine single-word verbs (fold, report, ledger, sum, check, sources, profiles, session, version), no duplicates |
| 9 | push | 5 | `nova-tokens help` -> fold waits on a local fold.lock, not a poll; the only remote write is ledger pushing to Redis |
| 10 | no leaks | 5 | `nova-tokens help` -> "The password is never a flag", read from env variables; no secret appeared in any output line |
| 11 | true help | 5 | `nova-tokens help` -> the setup and example lines all ran as printed; dry-run wrote nothing as claimed; exit codes behaved as stated |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens fold -h` | The write verbs (fold, session, report --note, ledger) take no `--op <id>`, so a retry after a timeout cannot be re-run with the same id; the same op twice is not told apart, and the family standard's idempotency-by-op-id is absent | Thread `--op <id>` through the shared skeleton on every write verb and return the recorded result for a repeated id | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens help` setup: | The first run needs eight hand-written `printf` lines to build one transcript; a stranger risks a typo before the first fold | Add an `example` verb that writes the fixture (as nova-self-talk example does), or ship the fixture | S |
| 2 | `nova-tokens report --who ...` | Writes record the author via `--who`, not the family-standard `--actor <name>` | Accept `--actor` alongside `--who` for one-shape consistency | S |
| 3 | `nova-tokens profiles -h` | profiles has no shipped fixture, so a cold reader cannot try it on real card-usage files | Ship a small swarm-root fixture and an example line | S |

## Top 3 fixes
1. Add `--op` to every write verb, the one finding that costs a full rubric point.
2. Add a fixture-writing `example` verb so a first run needs no hand-typed transcript.
3. Accept `--actor` on writes for the family's one-shape `--actor` column.

## Good, keep
- The dash-vs-zero rule: an unreported type prints `-`, never `0`, and sum counts the dashes, so a month is never silently claimed complete.
- The merge-by-source fold: re-folding one source recomputes its own rows and keeps every other row, so a repeat fold changes nothing even before an --op exists.
- The refusals: one run names every independent problem and what each wants, with a `run:` door that is a pasteable next command; unknown flags get a `did you mean` hint.

## What could not be tried
- `ledger` and `report --redis` over a live store: they dial a Redis server this card must not touch; judged from help, and `ledger --dry-run` ran clean over the shipped fixture with no dial (`LEDGER OK ... dry_run=true`).
- `profiles --swarm-root` over real card-usage files: no swarm-root fixture is shipped; judged from help only.
- `--opencode` source parsing: it runs a `sqlite3` subprocess over an OpenCode database file, and none is shipped; not tried.
