# nova-hook AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: 52ada6aff220
Score: 8.2/10 (45/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-hook help` -> `nova-hook version` (`nova-hook devel linux/amd64 go1.26.6`, exit 0) and `nova-hook status --redis 127.0.0.1:1` (refuses with cause and next step at exit 2) |
| 2 | refusals | 3 | `nova-hook status --bogus` -> `STATUS REFUSED: unknown flag --bogus; the flags of status are --as, --json, --max, --redis; run: nova-hook status -h` (exit 2); `USER= nova-hook hook` reports only `--role is required` and hides missing `--as` and `--redis` across three sequential runs |
| 3 | output | 4 | `nova-hook version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-hook devel linux/amd64 go1.26.6"}` (`prove --dry-run --json` mixes `"would-prove"` with `"dry_run"` in `facts`) |
| 4 | dry-run | 3 | `nova-hook hook --as m1 --role member --dry-run` without `--redis` -> prints `HOOK REFUSED: --redis is required...` followed by a false `HOOK NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 5 | `nova-hook version` and `nova-hook status` are read-only inspections; `unhook` on an unhooked or absent record prints `was=unhooked` or `was=none` at exit 0 |
| 6 | no prompts | 5 | `nova-hook status --redis 127.0.0.1:1` -> exits 2 immediately without prompting on stdin |
| 7 | exit codes | 5 | `nova-hook version` -> exit 0; `nova-hook status` on an unhooked name -> exit 1; flag or store refusal -> exit 2 |
| 8 | one word | 4 | `push`, `role`, `epoch`, and `nonce` are consistent in prose, but `prove --dry-run` emits `would-prove` (hyphen) beside `dry_run` (underscore) and `serve_alive` (underscore) |
| 9 | push | 5 | Built specifically for push delivery (`hook` streams push notices and `prove` answers challenges in-turn rather than polling) |
| 10 | no leaks | 5 | `nova-hook status --redis 127.0.0.1:1` -> `STATUS REFUSED: redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-hook help` |
| 11 | true help | 3 | `nova-hook help` says `A verb that lists takes --max <n> (default 20, 0 lists all)`, but `nova-hook friends --max 5` is refused at exit 2 (`unknown flag --max`); `usage:` shows `--as <me>` as required on `hook`, `prove`, `unhook`, and `serve` while the binary silently defaults `--as` to the OS login user |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-hook hook --as m1 --role member --dry-run` | Any early `REFUSED` or `FAILED` return on `hook`, `prove`, `unhook`, or `serve` when `--dry-run` is passed prints a false warning `<VERB> NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` because `c.DryRun()` is only read after flag, store, and role checks (`cmd/nova-hook/main.go`, `pkg/tool`) | Suppress the unread-`DryRun` note in `pkg/tool` when the verb returns `refused` or `failed`, or call `c.DryRun()` at the top of `hook`, `prove`, `unhook`, and `serve` | S |
| 2 | `nova-hook friends --max 5` | Refuses with `FRIENDS REFUSED: unknown flag --max; the flags of friends are --as, --json, --redis; run: nova-hook friends -h` (exit 2), contradicting the banner rule `A verb that lists takes --max <n> (default 20, 0 lists all)` while `friends` hardcodes a 50-item cap | Add `f.Max()` to `friends` in `cmd/nova-hook` so `--max` controls the listing cap as promised by the banner | S |
| 3 | `USER= nova-hook hook` | Prints only `HOOK REFUSED: --role is required...` (exit 2), hiding missing `--as` and missing `--redis` until the caller re-runs two more times; when `USER` is set in the environment, `--as` silently defaults to the OS login user even though `nova-hook help` usage writes `--as <me>` as required | Validate `--role`, `--as`, and `--redis` together in `f.Check` so one run names every missing or invalid flag, and either require `--as` explicitly or bracket `[--as <me>]` in `usage:` | S |
| 4 | `nova-hook status --as m1 --redis <addr>` | When `m1` is not hooked in, `status -h` promises `STATUS FAILED with what is not live and the command that hooks it in (exit 1)`, but the headline remedy is `run: nova-hook status --as m1` (the exact failing command, and missing `--redis <addr>`) and the notes append `unhook: nova-hook unhook --as m1` for a record in `state=none` | Set the `STATUS FAILED` remedy to a runnable `nova-hook hook --as <name> --role <role>` command (carrying `--redis` when given) and omit the `unhook` note when `state=none` or `state=unhooked` | S |
| 5 | `nova-hook serve --as m1 --redis <addr> --dry-run` | Prints `SERVE FAILED: the heartbeat server must itself be hooked in, as coordinator or comms: ...; run: nova-hook hook --as m1 --role comms; run: nova-hook hook --as m1 --role comms` with the `run:` remedy duplicated twice on the same line, and refuses to plan a `--dry-run` pass unless `--as` is already hooked in | Deduplicate the `run:` remedy on `serve` failure and allow `serve --dry-run` to run `PlanPass` without requiring `--as` to hold a live hook lease | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-hook status --as "BAD NAME" --redis <addr>` | `status` does not validate `--as` with the identity-name check, so an invalid name returns `STATUS FAILED name=BAD\x20NAME ...` at exit 1 with unquoted `run: nova-hook hook --as BAD NAME --role comms` notes instead of `STATUS REFUSED` at exit 2 | Validate `--as` in `status` (and `friends`) with the same name check `hook` uses, refusing invalid names at exit 2 | S |
| 2 | `nova-hook prove --as m1 --nonce 0123456789abcdef --redis <addr> --dry-run --json` | Emits `"would-prove":0` with a hyphen alongside `"dry_run":true` with an underscore in `facts` | Rename `would-prove` to `would_prove` (or keep one separator convention across all `nova-hook` fact keys) | S |
| 3 | `nova-hook hook --as m1 --role invalid` | Verb-level flag and value refusals end with `run: nova-hook help` (the full tool banner) while unknown-flag refusals end with `run: nova-hook <verb> -h` | Point verb-specific flag and value refusals to `nova-hook <verb> -h` | S |

## Top 3 fixes
1. Stop emitting `<VERB> NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` when `hook`, `prove`, `unhook`, or `serve` exits early on `REFUSED` or `FAILED`.
2. Add `--max <n>` to `nova-hook friends` so it matches the banner's `--max` promise, and check all required flags (`--role`, `--as`, `--redis`) in one pass rather than across sequential runs.
3. Fix the `STATUS FAILED` remedy on `nova-hook status --as <name>` (so it suggests `nova-hook hook` instead of repeating `nova-hook status --as <name>`) and remove the duplicate `; run: ...; run: ...` remedy on `nova-hook serve`.

## Good, keep
- Clear conceptual summary in `nova-hook help`: roles (`coordinator`, `friend`, `comms`, `member`, `reader`) and their required pushes (`bus`, `sprint`, `work`) are stated in five lines at the top of the banner.
- Every verb supports `--json` on stdout and `<verb> -h` at exit 0, and `hook`, `prove`, `unhook`, and `serve` all expose `--dry-run`.
- Connection refusals (`redis at 127.0.0.1:1 as the default user, no password: unreachable...`) state the exact address, auth mode, transport error, and next action in one bounded line without leaking credentials or stack traces.

## What could not be tried
- `nova-hook hook` (without `--dry-run`), `nova-hook prove` (without `--dry-run`), `nova-hook unhook` (without `--dry-run`), and `nova-hook serve` (without `--dry-run`): not run against live Redis because mutating hook records, spending nonces, or running the heartbeat server against the live store is forbidden by the review rules.
