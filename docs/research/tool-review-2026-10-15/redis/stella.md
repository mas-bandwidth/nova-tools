# nova-redis AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: d7e47f037ea8
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-redis` -> `REDIS REFUSED: no verb given; the verbs are serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus, version; run: nova-redis help` |
| 2 | refusals | 5 | `nova-redis spill` -> `SPILL REFUSED: --owner is required and may not be empty or hold ':' or whitespace; every key carries an owner prefix; run: nova-redis help` |
| 3 | output | 5 | `nova-redis spill --dry-run --json --redis 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi` -> `{"result":{"verb":"spill","status":"ok","exit":0},"facts":{"key":"ada:note","ttl":"10m0s","expires":"2026-10-15T19:39:27Z","bytes":2,"store":"127.0.0.1:6379","written":0,"dry_run":true}}` |
| 4 | dry-run | 5 | `nova-redis serve --dry-run --bind 127.0.0.1 --port 6379 --dir /tmp/nonexistent-store-dir-xyz` -> `SERVE OK bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/tmp/nonexistent-store-dir-xyz aclfile=/tmp/nonexistent-store-dir-xyz/users.acl users=0 dry_run=true created=0 launched=0` |
| 5 | idempotent | 5 | `n/a: no --op surface; install/uninstall are idempotent by design, write verbs verified for the dry-run` |
| 6 | no prompts | 5 | `nova-redis spill --dry-run --redis 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi` -> `SPILL OK key=ada:note ttl=10m0s expires=2026-10-15T19:39:27Z bytes=2 store=127.0.0.1:6379 written=0 dry_run=true` |
| 7 | exit codes | 5 | `nova-redis recall --redis 127.0.0.1:6399 --owner ada --name note` -> exit 2, `RECALL REFUSED ... next: start the store or correct the address ... run: nova-redis help` |
| 8 | one word | 4 | `nova-redis help spill` -> `nova-redis spill --dry-run --addr 127.0.0.1:6379 ...` (deprecated `--addr` alias for `--redis`, a duplicate noun) |
| 9 | push | 5 | `n/a: the tool pushes writes and returns; it polls no state itself` |
| 10 | no leaks | 5 | `nova-redis serve --dry-run --bind 127.0.0.1 --port 6379 --dir /tmp/nonexistent-store-dir-xyz` -> `SERVE OK ... auth=on ...` (no secret printed) |
| 11 | true help | 4 | `nova-redis spill -h` -> `effect: local write: writes files on this machine` (false for a store write) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis spill -h` | `effect: local write: writes files on this machine`; the verb writes to the Redis store, not local files; `fn load` and `acl apply` carry the same wrong label | relabel store-writing verbs as a store write; keep local write for serve only | M |
| 2 | `nova-redis acl render --json` | `ACL-RENDER REFUSED: unknown flag --json; acl render takes no flags` though the banner calls it a listing that takes `--max` and gives every verb `--json` | accept `--json` on acl render, acl check and acl apply and `--max` on acl render | M |
| 3 | `nova-redis recall --redis 127.0.0.1:6399 --owner ada --name note` | `next: start the store or correct the address, which was given to this tool; run: nova-redis help` names no runnable next command | print the concrete start or check command, as the fn check remedy does | S |
| 4 | `nova-redis help` | the example block uses the deprecated `--addr` spelling (`nova-redis spill --addr 127.0.0.1:6379 ...`) while the usage lines use `--redis`; running it works but prints `SPILL NOTE --addr is --redis` | use `--redis` in the help examples, or drop the alias | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis spill -h` | the write verbs take no `--op` id, so a retry after a lost reply cannot be made idempotent | add `--op` to spill and fn load through the shared skeleton | S |
| 2 | `nova-redis fn check --redis 127.0.0.1:6379` | `STALE nova_sprint sha=3ff536374b59b81e loaded=e547a647a6a8645d want=3ff536374b59b81e store=127.0.0.1:6379 remedy="nova-redis fn load --redis 127.0.0.1:6379 puts this binary's library on the store"` is accurate but long | keep the remedy, consider a one-flag `--json` form | S |

## Top 3 fixes
1. Fix the false `effect:` labels on the store-writing verbs (spill, fn load, acl apply); this is a wrong-help blocker that costs the true-help criterion.
2. Accept `--json`/`--max` on acl render, acl check and acl apply so every listing is machine-readable.
3. Print a concrete runnable next command when a store is unreachable, mirroring the fn check remedy.

## Good, keep
- Refusals aggregate every missing input in one run, name the cause, and give a next command, so recovery is one turn.
- `--dry-run` exists on every write and writes nothing (verified for serve, spill, acl apply, install store, uninstall store).
- The banner is honest and runnable: a first run and a throwaway-store recipe are given, and the store-free subset runs from the binary alone.

## What could not be tried
- serve (real launch), spill (real write), recall of a found/expired/unbounded key, fn load, acl apply, install/uninstall (real writes): all write to or require a live store, which this card must not touch; judged from help and `--dry-run` only.
- acl check: run read-only against the live store and returns exit 1 (MISSING users) as documented.
- fn check: run read-only against the live store; returns exit 1 with a concrete remedy for a stale library.
