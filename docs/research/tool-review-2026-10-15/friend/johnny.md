# nova-friend AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-friend help` -> `usage: ... example: nova-friend install --as bob --harness opencode --dir ./bob --model deepseek/deepseek-v4 --dry-run`; first run from help alone: `nova-friend status --as uuid-0d1b2c --dir ./bobsdir` -> `STATUS NONE: no daemon has run as uuid-0d1b2c` |
| 2 | refusals | 5 | `nova-friend status` -> `STATUS REFUSED: --as is required; it wants your name; refusing to guess; run: nova-friend help`; `nova-friend status --frobnicate` -> `STATUS REFUSED: unknown flag --frobnicate; the flags of status are --as, --dir, --json, --redis, --state-dir; run: nova-friend status -h`; `nova-friend frobnicate` -> `FRIEND REFUSED: "frobnicate" is no verb ... run: nova-friend help`; `nova-friend check --since nonsense` -> `CHECK REFUSED: invalid value for --since: it wants a duration such as 30s or 5m ... run: nova-friend check -h` |
| 3 | output | 5 | `nova-friend status --as uuid-0d1b2c --dir ./bobsdir --json` -> `{"result":{"verb":"status","status":"failed","exit":1,"word":"NONE",...},"facts":{}}` (one JSON value, same as the line) |
| 4 | dry-run | 3 | `nova-friend uninstall --as uuid-0d1b2c --dry-run` -> `UNINSTALL OK label=com.nova.friend-uuid-0d1b2c ... dry_run=true` then `UNINSTALL PLAN command=...`; but `nova-friend resume --as uuid-0d1b2c --dir ./bobsdir --dry-run` -> `RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 2 | `nova-friend host --as uuid-0d1b2c --harness opencode --dir ./bobsdir --op xyz --dry-run -- true` -> `HOST REFUSED: unknown flag --op; the flags of host are --as, --dir, --dry-run, --harness, --json, --prompt, --state-dir`; no verb of nova-friend takes `--op` |
| 6 | no prompts | 5 | `nova-friend lanes --as uuid-0d1b2c --dir ./bobsdir --json` -> `{"result":{"verb":"lanes","status":"ok","exit":0},"facts":{"lane_fault":"none"}}` (returned without any interactive prompt) |
| 7 | exit codes | 5 | `nova-friend version` -> `EXIT=0`; `nova-friend status` (missing flag) -> `EXIT=2`; `nova-friend status --as uuid-0d1b2c --dir ./bobsdir` (no daemon) -> `EXIT=1`; all match the help table `0 done, 1 ran and said no, 2 could not run` |
| 8 | one word | 5 | `nova-friend help` -> `the verbs are hook, run, install, uninstall, check, host, reach, pong, watch, status, lanes, refuse-go, resume, acceptance, version` (all distinct single words, none duplicated) |
| 9 | push | 4 | `nova-friend help` -> `how it works: ... beats to the sprint server, whose answer carries the friend's row` (state is pushed by beats, not polled; liveness proven by the hook record) |
| 10 | no leaks | 5 | `nova-friend check uuid-0d1b2c` -> `CHECK DAEMON friend=uuid-0d1b2c agent=none ... ` and `CHECK VERDICT friend=uuid-0d1b2c verdict=down ...` (no secret in any error or output line observed) |
| 11 | true help | 3 | `nova-friend resume --as uuid-0d1b2c --dir ./bobsdir --dry-run` -> `RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`; the same for `pong`; the help lists `--dry-run` for both verbs, so the claim is false when run |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-friend resume --as <me> --dir <d> --dry-run` | `RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` (exit 1): a dry run forbidding no write, and telling the AI it may have written | implement `--dry-run` in resume (print the plan, write nothing) or remove the flag from its help and usage | S |
| 2 | `nova-friend pong --as <me> --nonce abc123 --to <c> --dry-run` | `PONG NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` (exit 2 after the refusal): the advertised dry run is not honored | implement `--dry-run` in pong or remove it from its help and usage | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-friend install --as <me> --harness opencode --dir <abs> --model <m> --dry-run` | a dry run refuses without a `--redis` value (`INSTALL REFUSED: --redis is required`) and an absolute `--dir`, so a cold reader cannot preview an install without a live store address | allow a store-free dry run (print the plan with the redis address as the one unknown, or accept a placeholder) | M |
| 2 | every write verb (host, install, uninstall, resume, pong) | none takes `--op`, so a retry after a timeout cannot be shown to be a no-op | add the standard `--op` idempotency to the write verbs | M |
| 3 | `resume` / `pong` | the unimplemented `--dry-run` is refused at exit 1 (resume) and reported as a NOTE at exit 2 (pong) rather than as a usage error | exit 2 when the verb cannot honor a flag it advertises | S |

## Top 3 fixes
1. Implement or remove `--dry-run` on resume and pong (fixes the two blockers; moves criteria 4 and 11).
2. Add `--op` idempotency to the write verbs (moves criterion 5).
3. Let `install --dry-run` run without a live `--redis` value (smoothes the cold first run).

## Good, keep
- Every refusal names the cause, what the input wants, and the exact next command to run, in one line.
- Every verb prints one structured value, rendered as a line and as `--json` of the same value, so the two cannot drift.
- The help envelope (banner, usage per verb, `example:` block, exit table) makes a first cold run achievable from the help alone.

## What could not be tried
- `run`, `watch`, `reach`, `acceptance`, `refuse-go`: judged only from help and `--dry-run`; `run`/`watch`/`reach` start the agent or touch the daemon/bus, and `acceptance`/`refuse-go` write state.
- `install`, `uninstall`, `host` (real runs): not executed, to avoid installing, starting or stopping the daemon; their `--dry-run` plans were checked.
