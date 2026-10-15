# nova-friend AI-usability review, 2026-10-15

Reviewer: Mercury
Build: e9554ca0d8ad
Score: 8/10 (45/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-friend help` -> `nova-friend: what a friend runs to be part of the team: the lanes that work the friend's cards and their sprint metadata beat` |
| 2 | refusals | 5 | `nova-friend status` -> `STATUS REFUSED: --as is required; it wants your name; refusing to guess; run: nova-friend help` |
| 3 | output | 3 | `nova-friend status` -> `STATUS NONE: no daemon has run as me ...; run: nova-friend install --as me --harness <h> --dir friendtest` |
| 4 | dry-run | 5 | `nova-friend install --as me --harness opencode --dir friendtest --model x/y --dry-run` -> `friendtest` dir remains empty |
| 5 | idempotent | 0 | `nova-friend install -h` -> no `--op` flag on any write verb |
| 6 | no prompts | 5 | `nova-friend status --as me --dir friendtest` -> returns immediately, no prompt |
| 7 | exit codes | 5 | `nova-friend status` -> `EXIT=1` (verb said no), matches help exit table |
| 8 | one word | 5 | `nova-friend help` -> verbs are single words (hook, run, install, uninstall, check, host, reach, pong, watch, status, lanes, refuse-go, resume, acceptance, version) |
| 9 | push | 5 | `nova-friend watch -h` -> `It waits on your stream, on your wake file ... and on events, and returns on the first wake` (event/push-driven, not polled) |
| 10 | no leaks | 5 | `nova-friend status --json` -> JSON contains no secret |
| 11 | true help | 3 | `nova-friend install --as bob --harness opencode --dir ./bob --model deepseek/deepseek-v4 --dry-run` -> `INSTALL REFUSED: --redis is required` (help example omits the required flag) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-friend install` / `run` / `host` `-h` | None of the write verbs takes `--op`, so a retry after a timeout is not idempotent; the same call could double-apply | add an `--op <id>` id to every write verb that returns the recorded result when repeated | M |
| 2 | `nova-friend help` example block | The printed examples (`install`, `pong`) are refused because the required `--redis` flag is not shown: `INSTALL REFUSED: --redis is required; ... run: nova-friend help` | add the required `--redis` flag (or the env var) to the example commands so the block runs as printed | S |
| 3 | `nova-friend status` | The line word is `NONE` while the JSON says `"status":"failed"`; the status word is not one of OK/REFUSED/FAILED and the line and JSON drift | render the "said no" case as a standard status word and keep line and JSON from the same value | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|

## Top 3 fixes
1. Add `--op <id>` to every write verb so retries are idempotent.
2. Make the help `example:` block run as printed by including the required `--redis`.
3. Unify the status output: one standard word in both the line and the JSON.

## Good, keep
- Refusals are excellent: they name the cause, what the input wants, and the next command to run, and one run reports every problem at once (`status` refused both `--as` and `--dir` in one run).
- The banner answers what/how/use in the first lines, and `:` at the end gives a clean door.
- `--dry-run` exists on every write verb and demonstrably writes nothing (the target directory stayed empty).

## What could not be tried
- `run`, `install`, `uninstall`, `host`, `reach`, `pong`, `watch`, `resume`, `acceptance`, `hook`, `refuse-go`: each needs a bus store (`--redis`) or a daemon/coordinator that this review must not install, start or beat; judged from help and `--dry-run` only.
- `check` ran without a live session and reported an empty summary.
- No friend entry exists in `docs/ratings` for the 2026-10-02 round, so no old defect could be compared.
