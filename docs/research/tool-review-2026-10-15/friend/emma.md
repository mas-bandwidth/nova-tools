# nova-friend AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b4f484c84916
Score: 8.0/10 (43/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 3 | `nova-friend install --as f1 --harness opencode --dir ./work --model provider/model --dry-run` -> `INSTALL REFUSED: --redis is required; it wants the bus store's Redis address, host:port (or NOVA_BUS_REDIS), written into the agent; refusing to guess; run: nova-friend help` |
| 2 | refusals | 4 | `nova-friend status` -> `STATUS REFUSED: --as is required; it wants your name; refusing to guess; run: nova-friend help` |
| 3 | output | 4 | `nova-friend lanes --as f1 --dir ./work --json` -> `{"result":{"verb":"lanes","status":"ok","exit":0},"facts":{"lane_fault":"none"}}` |
| 4 | dry-run | 2 | `nova-friend resume --as f1 --dir ./work --dry-run` -> `RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 4 | `nova-friend lanes --as f1 --dir ./work` -> `LANES OK lane_fault=none` |
| 6 | no prompts | 3 | `nova-friend uninstall --as f1 --dry-run` -> `UNINSTALL OK label=com.nova.friend-f1 plist=/home/user/Library/LaunchAgents/com.nova.friend-f1.plist dry_run=true` |
| 7 | exit codes | 5 | `nova-friend status --as f1 --dir ./work` -> `STATUS NONE: no daemon has run as f1 (no status file in /home/user/.nova-friend/f1); run: nova-friend install --as f1 --harness <h> --dir ./work` |
| 8 | one word | 5 | `nova-friend version` -> `nova-friend 20261015T165143Z-b4f484c84916 linux/amd64 go1.26.6` |
| 9 | push | 5 | `nova-friend reach --as s1 --to f1 --dir ./work --dry-run` -> `REACH DRY-RUN friend=f1 from=bus step_timeout=1m0s dry_run=true` |
| 10 | no leaks | 5 | `nova-friend run --as f1 --harness opencode --dir /home/user/work --redis 127.0.0.1:6379 --dry-run` -> `RUN DRY-RUN as=f1 harness=opencode dir=/home/user/work state=work/.nova-friend redis=127.0.0.1:6379; nothing was started` |
| 11 | true help | 3 | `nova-friend install --as f1 --harness opencode --dir ./work --model provider/model --redis 127.0.0.1:6379 --dry-run` -> `INSTALL REFUSED: the friend's directory "./work" is not absolute: a harness's settings name it by its absolute path; run: nova-friend help` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-friend resume --as f1 --dir ./work --dry-run` | `resume` advertises `[--dry-run]` in `nova-friend --help`, but passing `--dry-run` fails with exit code 1: `RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` | Check `Call.DryRun` in `nova-friend resume` before writing state and print a `RESUME DRY-RUN` plan line | S |
| 2 | `nova-friend --help` (`example:` and `usage:` for `install` and `run`) | The first example in `--help` (`nova-friend install --as bob --harness opencode --dir ./bob --model deepseek/deepseek-v4 --dry-run`) fails twice when run cold: first with `INSTALL REFUSED: --redis is required` (marked optional `[--redis <addr>]` in `usage:`), and second with `INSTALL REFUSED: the friend's directory "./work" is not absolute` | Mark `--redis <addr>` required in `install` and `run` usage synopses and use an absolute `--dir /path/to/bob` with `--redis 127.0.0.1:6379` in the `install` example | S |
| 3 | `nova-friend pong --as f1 --nonce n1 --to s1 --dir ./work --dry-run` and `nova-friend run --as f1 --harness opencode --dir ./work --dry-run` | When `pong` or `run` refuses with `--dry-run` set, it appends `NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written`, and `pong --dry-run` with `--redis 127.0.0.1:6379` still dials Redis to validate friend names (`PONG REFUSED: f1 is no known name`) | Read `Call.DryRun` at the start of `pong` and `run` so `--dry-run` neither dials Redis nor triggers the unread-`DryRun` warning on early refusal | S |
| 4 | `nova-friend hook --harness opencode` | Invoking `nova-friend hook --harness <h>` from an interactive shell or AI probe blocks indefinitely waiting for hook JSON on stdin with no timeout or terminal check | Refuse immediately with a usage hint when stdin is a terminal (or support `--dry-run` on `hook`) so a cold probe never hangs | S |
| 5 | `nova-friend --help` vs `nova-friend status --as f1 --dir ./work` | `--help` says `state: <dir>/.nova-friend/ (--state-dir moves it)`, but `status --as f1 --dir ./work` looks in `/home/user/.nova-friend/f1` by default (`no status file in /home/user/.nova-friend/f1`), and `--help` claims `A verb that lists takes --max <n>` when no verb accepts `--max` | Align the `state:` documentation in `--help` with the actual default state directory and remove the inapplicable `--max <n>` sentence | S |
| 6 | `nova-friend magicverb`, `nova-friend status`, `nova-friend install --harness bogus ...` | Unknown-verb refusal says `"magicverb" is no verb and no file; ... and a file is given by its path (./magicverb)` even though `nova-friend` takes no file argument, and missing-flag/bad-value refusals end with `run: nova-friend help` instead of `run: nova-friend <verb> -h` | Drop the `no file` clause on unknown-verb refusals and point verb-level flag and value refusals to `run: nova-friend <verb> -h` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-friend version --json` | Emits `"facts":{}` and packs the version banner into `"payload"` instead of structured fields | Populate structured `facts` (`name`, `version`, `os`, `arch`, `go`) in `nova-friend version --json` | S |

## Top 3 fixes
1. Implement `Call.DryRun` handling in `nova-friend resume` and early in `pong` and `run` so `--dry-run` never writes, never dials Redis, and never trips the unread-`DryRun` failure.
2. Fix the `nova-friend --help` `install` example and `usage:` lines so `--redis <addr>` and an absolute `--dir` path are shown where required, and align the `state:` path description with `status`.
3. Refuse `nova-friend hook` when stdin is a TTY (or add `--dry-run`) so a cold invocation does not hang waiting on stdin, and point verb-level refusals to `nova-friend <verb> -h`.

## Good, keep
- When multiple required flags are missing (`nova-friend status`), the tool reports every missing flag (`--as` and `--dir`) in a single invocation.
- `nova-friend uninstall --dry-run`, `host --dry-run`, `reach --dry-run`, and `run --dry-run` (with `--redis` and an absolute `--dir`) print clear, parseable execution plans (`UNINSTALL PLAN`, `HOST DRY-RUN`, `REACH STEP`, `RUN DRY-RUN`) without touching live processes.
- `nova-friend status` and `nova-friend lanes` return clean `STATUS` and `LANES` lines and matching `--json` envelopes (`{"result":{"verb":"lanes","status":"ok","exit":0},"facts":{"lane_fault":"none"}}`).

## What could not be tried
- `nova-friend run`, `install`, `uninstall`, `host`, `reach`, `pong`, and `resume` without `--dry-run`: start or stop daemons, touch launchd/systemd agents, or send live wakes/pongs over Redis, which the card rules forbid (`Help, and the read verbs (status and the like) for your own name only; never install, start or stop the daemon, never beat, never wake anyone`); judged from `-h` and `--dry-run`.
- `nova-friend check`, `watch`, and `acceptance`: require a live coordinator store or spawn a live harness sub-invocation; judged from `-h`.
