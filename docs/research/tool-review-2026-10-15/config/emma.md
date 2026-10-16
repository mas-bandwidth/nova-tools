# nova-config AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b4f484c84916
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-config kinds` -> `CONFIG KINDS count=8` |
| 2 | refusals | 4 | `nova-config friend` -> `nova-config friend REFUSED: want add, set, remove, list, show or history; run: nova-config friend -h` |
| 3 | output | 3 | `nova-config machine show m1 --file try.json --json` -> `{"result":{"verb":"machine show","status":"ok","exit":0},"facts":{},"items":[{"kind":"machine","fields":{"name":"m1","user":"nova","seat":"s1","slots":"4","runners":"0","roles":"bench","width":"","tla":"false","note":"","created":"2026-10-15T12:00:00Z","updated":"2026-10-15T12:00:00Z","loops":null}}]}` |
| 4 | dry-run | 5 | `nova-config migrate --file try.json --dry-run` -> `CONFIG MIGRATE schema=45 applied=45 pending=0 ready=yes dry_run=true` |
| 5 | idempotent | 4 | `nova-config machine add m1 --op op1 --dry-run` -> `nova-config machine add REFUSED: unknown flag --op; this verb takes --actor, --as, --dry-run, --file, --json, --note, --pg, --roles, --runners, --seat, --slots, --tla, --user, --width; run: nova-config machine add -h` |
| 6 | no prompts | 5 | `nova-config friend list --max -1 --file try.json` -> `nova-config friend list REFUSED: --max must be zero or more; run: nova-config friend list -h` |
| 7 | exit codes | 5 | `nova-config tier show nonexistent --file try.json` -> `nova-config tier show REFUSED: tier nonexistent not found; run: nova-config tier list --file try.json` |
| 8 | one word | 5 | `nova-config version` -> `nova-config 20261015T165143Z-b4f484c84916 linux/amd64 go1.26.6` |
| 9 | push | 5 | `n/a: configuration store and inventory tool with no state watches or polling loops` |
| 10 | no leaks | 5 | `nova-config status --file try.json --redis 127.0.0.1:1` -> `nova-config status REFUSED: redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-config status -h` |
| 11 | true help | 3 | `nova-config friend -h` -> `effect: inspection: this help of the friend verbs; each verb's own -h has its flags and an example` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-config friend -h`, `nova-config provider -h`, `nova-config route -h`, `nova-config tier -h` | Running `nova-config friend` refuses with `run: nova-config friend -h`, but `nova-config friend -h` prints only `usage: nova-config friend [flags]` and omits both the valid subverbs (`add, set, remove, list, show, history`) and the kind's fields | Render each kind's subverb synopses and field descriptions directly from its `pkg/config.Kind` descriptor in `<kind> -h` and `help <kind>` | S |
| 2 | `nova-config machine show m1 --file try.json --json` and `nova-config friend list --file try.json --json` | `facts` uses native JSON numbers and booleans (`"width":0`, `"member":true`, `"rows":1`), whereas `items[].fields` serializes numeric and boolean columns as strings (`"slots":"4"`, `"runners":"0"`, `"tla":"false"`, `"width":"8"`, `"token_cap":"6000000"`) and emits `"loops":null` alongside `"width":""` | Type numeric and boolean fields consistently in `--json` `items[].fields` (or document that `items[].fields` are canonical text cells) and use a consistent empty representation | M |
| 3 | `nova-config inventory --fixture inv.yml --json` | `inventory` outputs JSON by default, so its FlagSet omits `--json`, but `main.go` intercepts `--json` before dispatch and wraps `unknown flag --json` inside a JSON refusal envelope (`{"result":{"verb":"inventory","status":"refused","exit":2,...}}`) | Register `--json` as a no-op flag on `nova-config inventory` so callers passing `--json` uniformly across verbs succeed | S |
| 4 | `nova-config version --json` | Emits `"facts":{}` and packs the binary name, version, OS/arch, and Go version into a whitespace-delimited `"payload":"nova-config ... linux/amd64 go1.26.6"` string | Populate structured `facts` (`name`, `version`, `os`, `arch`, `go`) in `nova-config version --json` | S |
| 5 | `nova-config --help` | Line 3 (`how it works: each kind (machine, fleet, friend, sprint, loop, route, tier)`) lists only 7 kinds and omits `provider`, even though `nova-config kinds` and the `kinds:` section list 8 kinds | Add `provider` to the `how it works:` kind list in `nova-config --help` | S |
| 6 | `nova-config friend list --max notanint --file try.json` and `nova-config friend list --bogus --max -1 --file try.json` | Invalid flag values emit Go's raw single-dash `invalid value "notanint" for flag -max: parse error`, and an unknown flag aborts parsing before reporting `--max -1` | Normalize single-dash flag names to `--<flag>` in parse errors and validate remaining flags so all flag problems are reported at once | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-config <kind> add`, `set`, `remove`, `apply` | Write verbs key mutations by row name and `--actor` and support `--dry-run`, but do not accept an explicit `--op` idempotence token | Accept `--op` on write verbs for uniform idempotence token coverage across tools | M |
| 2 | `nova-config <kind> <verb> -h` exit-code footer | Every subverb `-h` repeats `migrate --dry-run: ready=no, nothing attempted` and `machine self: 2 not a row, 3 unreadable` in its exit-code line | Scope the `migrate --dry-run` and `machine self` exit-code clauses to `migrate -h`, `machine self -h`, and top-level `help` | S |

## Top 3 fixes
1. Populate `nova-config <kind> -h` and `nova-config help <kind>` (`friend`, `provider`, `route`, `tier`) with each kind's valid subverbs and field list from `pkg/config.Kind`, and add `provider` to `how it works:` in `--help`.
2. Type numeric and boolean fields consistently in `--json` `items[].fields` (matching `facts`) and populate structured `facts` in `nova-config version --json`.
3. Accept `--json` as a no-op on `nova-config inventory` so `inventory --json` does not refuse with `unknown flag --json` inside a JSON envelope.

## Good, keep
- Top-level `--help` provides a self-contained 5-line `--file try.json` quickstart that works cold without PostgreSQL or Redis, and `inventory --example` generates a runnable fixture offline.
- Every refusal goes strictly to stderr in plain mode and strictly to stdout as a single JSON envelope under `--json`, naming the next command to run (`run: nova-config tier list --file try.json`).
- Unreachable Redis endpoints (`--redis 127.0.0.1:1`) suppress `go-redis` connection-pool log noise and emit a single clean `REFUSED:` line with zero secret leakage.

## What could not be tried
- `nova-config <kind> add`, `set`, `remove`, and `apply` without `--dry-run`: forbidden by the card rules (`Read verbs only (the friend and machine lists, a row shown); never a set, an apply or a delete`); judged from `-h` and `--dry-run`.
- `nova-config login`, `logout`, and live PostgreSQL/Redis mutations: touch live credentials or services outside `<job>/scratch`; judged from `-h` and `--dry-run`.
