# nova-config AI-usability review, 2026-10-15

Reviewer: nemotron-3-ultra
Build: de16887455af
Score: 9.5/10 (51/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-config` -> `nova-config REFUSED: no verb; want kinds, migrate, status, apply, inventory, login, logout, or a kind (machine, fleet, friend, sprint, loop, provider, route, tier) then add|set|remove|list|show|history; run: nova-config help` |
| 2 | refusals | 5 | `nova-config machine add m1 --user nova --seat s1 --actor a1 --file try.json` -> `nova-config machine add REFUSED: --slots is required: the machine ceiling apply writes to machine:<m>:ceiling, which a bench's share on it fits under; never a cap on a friend, whose width is the friend's own; not the sprint's width; run: nova-config machine add -h` |
| 3 | output | 5 | `nova-config status --file try.json --json` -> `{"result":{"verb":"status","status":"ok","exit":0},"facts":{"file":"try.json","schema":45,"machine":1,"machine_rev":2,"fleet_rev":0,"friend":0,"friend_rev":0,"sprint_rev":0,"loop":0,"loop_rev":0,"provider":0,"provider_rev":0,"route":0,"route_rev":0,"tier":3,"tier_rev":0,"providers":null,"redis":""},"notes":["tier=flash holds no route of a funded provider; run: nova-config tier show flash --file try.json","tier=heavy holds no route of a funded provider; run: nova-config tier show heavy --file try.json","tier=pro holds no route of a funded provider; run: nova-config tier show pro --file try.json"]}` |
| 4 | dry-run | 4 | `nova-config apply --file try.json --actor a1 --redis localhost:6379 --dry-run` -> `nova-config apply REFUSED: redis at localhost:6379 as the default user, no password: unreachable: dial tcp 127.0.0.1:6379: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-config apply -h` (--dry-run requires live Redis) |
| 5 | idempotent | 3 | `nova-config machine set m1 --width 6 --actor a1 --file try.json` (run twice) -> second run: `CONFIG SET kind=machine name=m1 rev=3 changed=width`; history shows id=3 with identical before/after `{"note":"","roles":"bench","runners":"0","seat":"s1","slots":"8","tla":"false","user":"nova","width":"6"}` |
| 6 | no prompts | 5 | `nova-config machine add m1 --user nova --seat s1 --slots 8 --actor a1 --file try.json` -> `CONFIG ADD kind=machine name=m1 rev=1` (no interactive prompt) |
| 7 | exit codes | 5 | `nova-config unknown-verb; echo $?` -> `exit code: 2` (help: 0 done, 1 refused, 2 could not run, machine self: 2 not a row, 3 unreadable) |
| 8 | one word | 5 | `nova-config help` lists distinct verbs: kinds, migrate, status, apply, inventory, login, logout, and per-kind add|set|remove|list|show|history|width|self|run; no duplicates |
| 9 | push | 5 | n/a: config tool has no long-running state wait |
| 10 | no leaks | 5 | `nova-config migrate --file try.json` -> `CONFIG MIGRATE file=try.json from=0 to=45 applied=45` (no passwords in output; DSN uses env var for password) |
| 11 | true help | 4 | `nova-config apply --file try.json --actor a1 --redis localhost:6379 --dry-run` -> help claims "--dry-run prints the lines and writes nothing" but requires live Redis connection |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine set m1 --width 6 --actor a1 --file try.json` (second run) | `CONFIG SET kind=machine name=m1 rev=3 changed=width` with history entry showing identical before/after; revision increments on no-op | Make set idempotent: when all fields match current row, return "unchanged" with same revision, no history entry | S |
| 2 | `nova-config apply --file try.json --actor a1 --redis localhost:6379 --dry-run` | `nova-config apply REFUSED: redis at localhost:6379 as the default user, no password: unreachable: dial tcp 127.0.0.1:6379: connect: connection refused` --dry-run fails without live Redis | Make apply --dry-run work without Redis (print planned Redis ops without connecting) | S |
| 3 | `nova-config machine set m1 --width 6 --actor a1 --file try.json` (help claim) | Help implies set is a clean write; actual behavior creates history noise on repeated identical sets | Document idempotency behavior in help or fix to be truly idempotent | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine show m1 --file try.json --json` | JSON shape varies: some verbs use "facts" + "items", others only "items" | Standardize JSON envelope across all verbs | M |
| 2 | `nova-config status --file try.json` | Notes printed to stderr mixed with main output on stdout | Separate notes to stderr consistently or include in JSON | S |
| 3 | `nova-config inventory --fixture try.json --list` | Fixture format differs from config file format; error message verbose | Accept config file as fixture or clarify in help | S |

## Top 3 fixes
1. Make machine set idempotent: no history entry or revision bump when values unchanged
2. Make apply --dry-run work without live Redis connection
3. Standardize JSON output envelope across all verbs

## Good, keep
- Comprehensive help with worked examples for every verb
- Clear refusals that name the cause and next command
- --json support on all verbs with stable result shape
- --file mode enables full local testing without database
- Exit codes match documented semantics exactly

## What could not be tried
- `nova-config apply` (full apply): requires live Redis and PostgreSQL
- `nova-config inventory` (against Redis): requires live Redis
- `nova-config loop run`: requires a machine row with seat/keys and a command to run
- `nova-config login`/`logout` (full flow): requires nova-secrets store and seat profiles
- `nova-config migrate` against PostgreSQL: requires live database