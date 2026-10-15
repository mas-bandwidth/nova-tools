# nova-bus AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: cc2f2f8c8148
Score: 8/10 (43/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-bus help` -> `nova-bus: messages between AIs over Redis streams: sent once, delivered until acked` |
| 2 | refusals | 4 | `nova-bus send --as ada --to bob --body hi` -> `SEND REFUSED: --subject is required; it wants one line saying what the message is; refusing to guess; run: nova-bus help` |
| 3 | output | 3 | `nova-bus wait --as bob --timeout 1s --redis 127.0.0.1:16389 --json` -> `{"status":"ok","word":"NONE","after":"0-0","messages":[]}` |
| 4 | dry-run | 3 | `nova-bus overdue --as bob --redis 127.0.0.1:16389 --dry-run` -> `OVERDUE REFUSED: unknown flag --dry-run; the flags of overdue are --as, --json, --max, --older, --redis, --timeout; run: nova-bus overdue -h` |
| 5 | idempotent | 3 | `nova-bus send -h` -> `--token <string>  your word for this one send, the same on every retry of it (letters, digits, . _ : -; at most 128 bytes)` |
| 6 | no prompts | 5 | `nova-bus send --as ada --to bob --subject s` -> `SEND REFUSED: the body comes from exactly one of --body <text> or --stdin; run: nova-bus help` |
| 7 | exit codes | 5 | `nova-bus wait --as bob --timeout 1s --redis 127.0.0.1:16389` -> `WAIT NONE after=0-0 waited=1s` |
| 8 | one word | 4 | `nova-bus help` -> `nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> or --all] [--ack] [--exec <command>] [--forever --exec <command>] [--timeout <duration>] [--redis <addr>] [--dry-run]` |
| 9 | push | 4 | `nova-bus wait --as bob --timeout 1s --redis 127.0.0.1:16389` -> `WAIT ARMED after=0-0` |
| 10 | no leaks | 5 | `nova-bus names --as bob --redis 127.0.0.1:19999` -> `NAMES REFUSED: redis at 127.0.0.1:19999 as the default user, no password: unreachable: dial tcp 127.0.0.1:19999: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-bus help` |
| 11 | true help | 3 | `nova-bus send --as ada --to bob --subject hello --body "are you there?" --redis 127.0.0.1:16389 --dry-run` -> `SEND OK id=- to=bob cc=- at=2026-10-15T16:34:31Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none dry_run=true` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus overdue --as bob --redis 127.0.0.1:16389 --dry-run` | `OVERDUE REFUSED: unknown flag --dry-run; the flags of overdue are --as, --json, --max, --older, --redis, --timeout; run: nova-bus overdue -h` even though `overdue` has `effect: delivery` and writes timeout messages (`b.ExpireAsks`) before listing overdue entries. | Add `DryRun: true` to `overdue` in `cmd/nova-bus/main.go:453-482` and skip `b.ExpireAsks` Writes when `c.DryRun()` is true. | S |
| 2 | `nova-bus recv --as bob --redis 127.0.0.1:16389 --dry-run` | When `recv --dry-run` or `ack --dry-run` exits early on an unhooked caller, unreachable Redis, or bad `--kind`, it prints `RECV NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` (and `ACK NOTE ...`). | Call `dry := c.DryRun()` at the top of `recv` (`cmd/nova-bus/main.go:862`) and `ack` (`cmd/nova-bus/main.go:978`), just as `send` already does at `cmd/nova-bus/main.go:675`. | S |
| 3 | `nova-bus wait --as bob --timeout 1s --redis 127.0.0.1:16389 --json` | Prints custom JSON `{"status":"ok","word":"NONE","after":"0-0","messages":[]}` on stdout at exit 1 with `"status":"ok"` and no `"exit"` field, while hook/flag failures on `wait --json` print the `pkg/tool` `{"result":{...},"facts":{...}}` envelope. | Render `wait --json` through the standard `pkg/tool` `Out` envelope (`cmd/nova-bus/main.go:1304,1382`) with `status: "failed"` and `exit: 1` on `WAIT NONE`. | M |
| 4 | `nova-bus peek --max 1` | `PEEK REFUSED: unknown flag --max; the flags of peek are --as, --json, --kind, --redis, --timeout; run: nova-bus peek -h` (and `NAMES REFUSED: unknown flag --max` on `nova-bus names --max 1`), contradicting `nova-bus help`: `A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest.` | Add `f.Max()` to `peek` (`cmd/nova-bus/main.go:349`) and `names` (`cmd/nova-bus/main.go:549`), and clarify in `nova-bus help` that `recv --max <n>` is a delivery batch count (>= 1) while `--all` takes all. | S |
| 5 | `nova-bus send --as ada --to bob --subject hello --body "are you there?" --redis 127.0.0.1:16389 --dry-run` | `send -h` claims `--dry-run checks the message as send does (every problem named) and prints the line with no id`, but text output prints `id=-` (`SEND OK id=- ... dry_run=true`) while `--json` prints `"id":""`. | Update `send -h` (`cmd/nova-bus/main.go:293`) to state `id=-` and `dry_run=true`, and set `id` to `"-"` consistently in both text and `--json`. | S |
| 6 | `nova-bus overdue -h` | The `exit codes:` line says `1 BUS OVERDUE, one or more, or not hooked in (FAILED reason=unhooked)`, whereas `Detail` and `cmd/nova-bus/main.go:1154` print `OVERDUE OVERDUE count=<n> older=<d>`. | Change `1 BUS OVERDUE` to `1 OVERDUE OVERDUE` in `cmd/nova-bus/main.go:456`. | S |
| 7 | `nova-bus send --as ada --to bob --subject s --body hi --kind bogus --redis 127.0.0.1:19999 --dry-run` | Prints `SEND REFUSED: redis at 127.0.0.1:19999 ... unreachable` instead of refusing `--kind bogus`, because `--kind` and `--token` are validated only after dialing Redis and checking the hook gate (`cmd/nova-bus/main.go:687-709,863-875,1011-1023`). | Validate `--kind` (`bus.CheckKinds`) and `--token` (`bus.CheckToken`) inside `Flags.Check` in `send`, `peek`, and `recv` before opening Redis. | S |
| 8 | `nova-bus send --as ada --to bob --body hi` | Prints `SEND REFUSED: --subject is required; it wants one line saying what the message is; refusing to guess; run: nova-bus help` — pointing to generic `nova-bus help` instead of `nova-bus send -h` on verb-specific flag errors. | Set the default refusal remedy for per-verb flag/check refusals in `pkg/tool` to `nova-bus <verb> -h`. | S |
| 9 | `nova-bus help` | A cold first run on a fresh Redis fails with `FAILED reason=unhooked`, and after hooking/proving fails with `ada is no known name`, because `nova-bus help` does not mention that a store needs friend rows (`friends` set) and a proven `nova-hook` challenge before the `example:` block can run. | State the full first-run prerequisites (`nova-config` friend rows and `nova-hook hook` + `nova-hook prove`) in `nova-bus help` (`cmd/nova-bus/main.go:218-220`), and update `docs/CLI.md:1384-1435` (which still documents retired `push=proven` output on `names` and omits `receipts` from the commands table). | S |
| 10 | `nova-bus wait --as bob --timeout 1s --redis 127.0.0.1:16389` | In `cmd/nova-bus/main.go:1414`, `WAIT MESSAGE` formats `Fact("subject", m.Subject)` without `tool.Text(m.Subject)`, so multi-word subjects on `WAIT MESSAGE` are unquoted while `peek`, `recv`, `log`, `overdue`, and `asks` quote `subject` with `tool.Text`. | Wrap `m.Subject` in `tool.Text(m.Subject)` on `WAIT MESSAGE` in `cmd/nova-bus/main.go:1414`. | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus send -h` | `send` names its idempotency key `--token <t>` instead of the suite-wide `--op <id>` flag, and `recv` has no `--op` idempotency key. | Accept `--op` as the standard flag name (keeping `--token` as an alias) in `cmd/nova-bus/main.go:315`. | S |
| 2 | `nova-bus wait -h` | When `--wake-file <path>` is given, `wait` drops `XREAD` block time to `WaitTick = 1s` (`cmd/nova-bus/main.go:1249,1375-1377`) and polls the file once per second. | Watch `--wake-file` with OS filesystem notifications (`fsnotify`/`inotify`) alongside the blocking stream read. | M |

## Top 3 fixes
1. Add `--dry-run` to `overdue` (`cmd/nova-bus/main.go:453-482`) and call `c.DryRun()` at the start of `recv` and `ack` (`cmd/nova-bus/main.go:862,978`) so every write verb has a clean, side-effect-free dry run without false `Call.DryRun` warnings on early refusals.
2. Unify `wait --json` (`cmd/nova-bus/main.go:1304,1382`) with the `pkg/tool` JSON envelope (`status: "failed"`, `exit: 1` on `WAIT NONE`) and quote `WAIT MESSAGE` `subject=` with `tool.Text(m.Subject)` (`cmd/nova-bus/main.go:1414`).
3. Make every claim in `nova-bus help`, `<verb> -h`, and `docs/CLI.md` true when run: add `--max` to `peek` and `names`, fix `overdue -h` (`OVERDUE OVERDUE`), fix `send -h` (`id=-` and `dry_run=true`), validate `--kind`/`--token` in `Flags.Check`, and refresh `docs/CLI.md:1384-1435`.

## Good, keep
- Every `SEND OK` line reports `bytes=<n>` and `sha256=<hex>` as stored in Redis, and `--token` makes retries idempotent and conflict-checked.
- `wait` reads with `XREAD` outside the consumer group and prints `WAIT ARMED after=<id>` first so callers can re-arm across turns without losing messages or disturbing `recv`.
- Unhooked callers receive a structured `FAILED reason=unhooked` result with role-specific `nova-hook` remedies on `NOTE` lines.

## What could not be tried
- `nova-bus send` (without `--dry-run`), `nova-bus recv` (without `--dry-run`, including `--ack`, `--exec`, `--forever`), `nova-bus ack` (without `--dry-run`), and `nova-bus overdue` on a store with expired asks: the review rules restrict `nova-bus` probes to read-only inspection and `--dry-run` writes.
