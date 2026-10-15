# nova-tokens AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` -> `TOKENS OK days=1 rows=1 sources=1 unreadable=0` |
| 2 | refusals | 5 | `nova-tokens fold --nope` -> `TOKENS REFUSED: unknown flag --nope; ... run: nova-tokens fold -h` (each of missing-flag, unknown-flag, unknown-verb, bad-value names cause and next command) |
| 3 | output | 5 | `nova-tokens sum --out ./out --month 2026-09 --json` -> `{"result":{"verb":"sum","status":"ok","exit":0},"facts":{...},"items":[...]}` |
| 4 | dry-run | 5 | `nova-tokens fold --out ./dryout --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --dry-run` -> `TOKENS OK ... dry_run=true`; `ls dryout` empty after |
| 5 | idempotent | 5 | `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` (2nd run) -> `TOKENS DAY day=2026-09-11 rows=2`; `sum` still `pairs=2` (no doubling) |
| 6 | no prompts | 5 | every verb ran to completion with no input; no interactive wait observed |
| 7 | exit codes | 5 | `nova-tokens version` `=0`; `nova-tokens` (no verb) `=2`; `nova-tokens fold --day not-a-date` `=2`; `nova-tokens fold --dry-run` `=0` |
| 8 | one word | 5 | verbs are fold, report, ledger, sum, check, sources, profiles, session, version; all distinct, one per thing |
| 9 | push | 5 | n/a: no polling surface; state is written (fold to day files, ledger to store), not polled |
| 10 | no leaks | 5 | no error or output line printed a secret; password is never a flag (env vars only) |
| 11 | true help | 3 | help claims `--dry-run` prints `dry_run=true` on its last line, but `report --dry-run` puts it on stderr; the help's own fold+session example double-counts a day |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens fold ... --claude bench=./transcripts` then `nova-tokens session --claude-session ./session.jsonl --out ./freshout` (both from the help example) | `sum` prints `SUM TOTAL input=1624` for a day that spent `input=812`; the same message id `example-1` is folded as `claude:bench` and re-booked as `claude-session` on `unattributed`, doubling the day. | make `session --out` skip message ids already present in the day file from a declared source (or have the fold see and merge the session row), so the documented first run cannot double a day | S |
| 2 | `nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --dry-run` | help promises `--dry-run` prints `dry_run=true` on its last line, but the marker goes to stderr; a caller capturing stdout (`REPORT OK ... dry_run=true` absent) never sees the dry-run marker. | emit the `dry_run=true` marker on the same stream as the artifact (stdout) or fix the help claim | S |
| 3 | `nova-tokens sum --out ./emptyout --month 2026-09` | exits `0` with `SUM OK days=0` over an `--out` holding no day file, while `nova-tokens check --out ./emptyout` calls the same emptiness `CHECK FAILED` exit 1; the two verbs answer "nothing" differently. | make `sum` (and `profiles`) report over an empty `--out` with a FAILED line and exit 1, as `check` does, or say in each help which verbs answer over nothing | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens fold` (no `--day`/`--all`) and `nova-tokens fold --day not-a-date` | refusals end `run: nova-tokens help`, while the unknown-flag refusal ends `run: nova-tokens fold -h`; a new AI reading the message goes to the global help, not the verb. | end every refusal with the verb's own `-h` | S |
| 2 | `nova-tokens version --json` | `VERSION REFUSED: takes no flags and no arguments, got 1` never names `--json`; help already says only `version` takes no `--json`, so a refusal naming `--json` would teach faster. | name the offending flag in the refusal | S |
| 3 | `nova-tokens sum --out ./emptyout --month 2026-09` line `SUM MONTH ... first=- last=-` | hyphens for missing fields are fine; empty `--out` answers OK, which a gate could mistake for a green over nothing. | see blocker 3 (same change) | S |

## Top 3 fixes
1. Make the documented first run stop double-counting (session vs fold share message ids) so a new user's first data is right.
2. Put the `dry_run=true` marker on the same stream the help promises so `--dry-run` is machine-visible.
3. Make `sum`/`profiles` report over an empty `--out` the way `check` does, so "nothing" is one answer.

## Good, keep
The help is unusually complete: a runnable setup/example, exit-code table, environment rules, and per-verb `-h`. `--dry-run` writes nothing and `--json` is stable and structured. Refusals name cause and next command, fold is idempotent by merge-on-source, and the tool never prompts and never leaks a secret. These must not be lost.

## What could not be tried
- `ledger` and `report --redis` store modes: not run against a live store (no Redis service may be touched); judged from help and `ledger --dry-run`, which dials no store and exits 0.
- `--opencode` source: not run against a real OpenCode database (needs sqlite3 and a DB copy; none on the bench); judged from help and the `--scratch`/sqlite3 refusals.
- `--swarm` and `--bus` sources: not run, no fixtures on the bench.
- `sources` over a source that is unreadable/unparsed: not reached (my fixture was clean), so the exit-1-vs-0 claim was not re-verified.
- `profiles --swarm-root` over a real card root: no swarm root on the bench; only the missing-flag and missing-dir refusals were exercised.
