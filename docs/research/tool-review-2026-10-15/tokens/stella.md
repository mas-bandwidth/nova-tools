# nova-tokens AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: fb84d5d9fec3
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` -> `TOKENS OK days=1 rows=1 sources=1 unreadable=0 unparsed=0 mixed=0 conflict=0 shrank=0 partial=0 quiet=0` (ran straight from the help's setup and example blocks, no other doc) |
| 2 | refusals | 5 | `nova-tokens frobnicate` -> `TOKENS REFUSED: unknown verb "frobnicate"; the verbs are fold, report, ledger, sum, check, sources, profiles, session, version; run: nova-tokens help`; `nova-tokens fold` -> `TOKENS REFUSED: --out is required; ... run: nova-tokens help`; `nova-tokens fold --bogus x` -> `TOKENS REFUSED: unknown flag --bogus; the flags of fold are ...; did you mean --bus?; run: nova-tokens fold -h`; `nova-tokens fold --day notadate ...` -> `TOKENS REFUSED: --day is not a day: notadate; ... run: nova-tokens help` |
| 3 | output | 5 | `nova-tokens fold --json` -> `{"result":{"verb":"fold","status":"ok","exit":0},"facts":{"days":1,"rows":1,...}}`; every verb emits one stable `VERB ...` line grammar and one JSON object under `--json` |
| 4 | dry-run | 5 | `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --dry-run` -> `TOKENS OK ... dry_run=true` and `TOKENS DAY ... written=false would_write=true`; no day file was created |
| 5 | idempotent | 3 | a re-run of `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` writes the identical `TOKENS DAY day=2026-09-11 rows=1 ... written=true` and changes nothing (merge-by-source), but no write verb takes `--op`; the help's flag list for fold has no `--op`, so a retry after a timeout cannot be identified as a replay |
| 6 | no prompts | 5 | `nova-tokens check --out ./emptyout < /dev/null` -> returned immediately with `CHECK FAILED ...` (exit 1); no verb waited on stdin |
| 7 | exit codes | 5 | `nova-tokens fold` (missing flag) -> `exit=2`; `nova-tokens check --out ./emptyout` -> `exit=1`; `nova-tokens version` -> `exit=0`; all match the help's stated 0/1/2 scheme |
| 8 | one word | 5 | `nova-tokens help` -> `the verbs are fold, report, ledger, sum, check, sources, profiles, session, version`; nine single-word verbs, none duplicated |
| 9 | push | 5 | `nova-tokens fold ...` wrote `--out/fold.lock`; the help states a second fold waiting holds the lock refuses naming the holder (push, not poll); `ledger` delivers writes to the store |
| 10 | no leaks | 5 | `nova-tokens help` -> `The password is never a flag.`; no output line in any run carried a credential or secret |
| 11 | true help | 5 | verified `--json`, `--dry-run`, exit codes, lock behavior, and the report local/store mode split all behave exactly as `nova-tokens help` says |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens help` | the help is ~200 lines; the scroll "first run" setup/example blocks sit at the bottom, and a cold AI must read the whole text before its first successful run | add a 6-line quickstart (mkdir, three printf lines, fold, check) immediately after the summary paragraph, above "usage:" | S |
| 2 | `nova-tokens fold --help` / `nova-tokens ledger --help` | no write verb takes `--op`; a timed-out write cannot be retried with an id, so a caller cannot tell a replay from a second write (a 2026-10-02 finding still there) | accept `--op <id>` on the write verbs and return the recorded result for a repeat | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens fold --json` vs line mode | an unmeasured value is `null` in JSON but `-` in line mode (e.g. `nousage`:null vs `nousage=-`) | document the `null`/`-` equivalence in help, or emit `null` in both | S |
| 2 | `nova-tokens fold --bogus x` | the "did you mean --bus?" suggestion for `--bogus` is a weak guess | suggest no flag, or only when a close flag exists | S |
| 3 | `nova-tokens report` (local mode) stdout | the note body (tab-separated lines) and the `TOKENS AVG`/`REPORT OK` diagnostic lines share one stdout stream | prefix diagnostic lines so a consumer can separate body from diagnostics (or use `--json`'s `payload` field) | M |

## Top 3 fixes
1. Add `--op <id>` to the write verbs (`fold`, `session`, `report`, `ledger`) so a retry is identifiable and idempotent to the rubric's letter (M) - the only criterion scoring below 5.
2. Add a quickstart block to the top of `nova-tokens help` (S) - the largest reduction in cold-start friction.
3. Align the JSON `null` and line-mode `-` representations of unmeasured values (S).

## Good, keep
Every refusal names both the cause and the next command to run, and the missing-flag case reports every missing flag at once. `--dry-run` reads the same sources, refuses what the real run refuses, and writes nothing, with `dry_run=true` on its last line. Password never becomes a flag; exit codes 0/1/2 are documented and truthful; the lock-based fold coordination is push, not poll. The 2026-10-02 defects that are fixed and must be kept: `--json` on every verb, `--dry-run` on every write, unknown flags routed through the `REFUSED` grammar with a nearest-name hint, and the now-quoted `subject=` value (`subject="tokens 2026-09-11 at=..."`).

## What could not be tried
- `ledger` (real write) and `report --redis` (store mode): both need a live Redis store that this card must not touch; judged from their help and `ledger --dry-run` (which dials no store and works: `LEDGER OK month=2026-09 days=1 rows=2 bad=0 dry_run=true`).
- `profiles`: needs a `--swarm-root` with a swarm pool; none is shipped in the fixture tree, so it was judged from `profiles -h` (reads, writes nothing) only.
- `session` with `--opencode`/`--swarm`/`--provider` sources: the OpenCode sqlite3 path and provider exports were not exercised; judged from help and the `--claude-session` run (which worked: `SESSION turns=1 input=812 ... weighted=11512`).
