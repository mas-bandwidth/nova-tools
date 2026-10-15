# nova-redis AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: 4540d7fd37ca
Score: 8/10 (43/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-redis help` -> `first run: --dry-run needs no store; the throwaway recipe below starts a store on a socket`; `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi` -> `SPILL OK key=ada:note ttl=10m0s expires=... bytes=2 store=127.0.0.1:6379 written=0 dry_run=true` |
| 2 | refusals | 5 | `nova-redis spill --addr 127.0.0.1:6379` -> `SPILL REFUSED: --value is required: ... ; --owner is required ... ; --name is required ... ; --ttl is required ... ; run: nova-redis help` (every problem at once, each with what it wants) |
| 3 | output | 4 | `nova-redis spill --dry-run --json --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi` -> `{"result":{"verb":"spill","status":"ok","exit":0},"facts":{...},"notes":["--addr is --redis"]}`; but `nova-redis acl render --json` -> `ACL-RENDER REFUSED: unknown flag --json; acl render takes no flags` |
| 4 | dry-run | 2 | `nova-redis install store --dry-run --units ./units` -> `INSTALL-STORE REFUSED: ... the verb never read it (Call.DryRun); it may have written`; `nova-redis fn load -h` -> flags list has no `--dry-run` |
| 5 | idempotent | 1 | `nova-redis spill -h` -> flags are `--addr --dry-run --json --name --owner --password-env --redis --ttl --user --value` with no `--op`; fn load and acl apply also take none |
| 6 | no prompts | 5 | `nova-redis acl render` -> runs to a complete exit 0 with no interaction |
| 7 | exit codes | 5 | `nova-redis` -> `REDIS REFUSED: ... ; run: nova-redis help` exit 2; `nova-redis version` exit 0; `nova-redis fn check --addr 127.0.0.1:6399` -> `FAILED nova_sprint ...` exit 2 |
| 8 | one word | 5 | verb set serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus, version, help; no duplicate verbs or tools |
| 9 | push | 5 | n/a: verbs read or write directly (recall reads, spill writes); no waited-on state is polled |
| 10 | no leaks | 5 | `nova-redis fn check --addr 127.0.0.1:6399` -> FAILED line carries only the address and remedy; passwords are read from `NOVA_REDIS_PASSWORD_ENV`/`NOVA_REDIS_PASSWORD`, never an argument |
| 11 | true help | 2 | `nova-redis spill -h` -> `effect: local write: writes files on this machine` but spill writes to the Redis store; `nova-redis install store --dry-run` refuses where its help says it prints the unit and writes nothing |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis install store --dry-run --units ./units` | the advertised dry run refuses without the full secrets login and then warns `INSTALL-STORE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written`, contradicting its own `nothing was written`; an AI cannot preview an install cold | make `--dry-run` compute and print the unit plan without requiring the secrets login, and suppress the self-warning when DryRun is set | M |
| 2 | `nova-redis spill -h` | `effect: local write: writes files on this machine` on spill, fn load and acl apply, though all three write to the Redis store, not to local files | relabel the store-writing verbs as `store write:` and keep `local write` for serve alone | S |
| 3 | `nova-redis fn load -h` | fn load is the one write verb with no `--dry-run` and no `--op`; an AI cannot preview the function-library replace or retry it safely | add `--dry-run` and `--op` through the shared skeleton | M |
| 4 | `nova-redis spill -h` | no write verb takes `--op`, so a retry after a lost reply cannot be made idempotent | add `--op` to spill, acl apply and fn load | M |
| 5 | `nova-redis acl render --json` | the one store-free listing refuses `--json`, so no machine-parseable rendering of the ACL is available to an AI | accept `--json` and `--max` on acl render, acl check and acl apply | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis spill --dry-run --addr 127.0.0.1:6379` | the deprecated `--addr` prints a `SPILL NOTE --addr is --redis` line on every run, and the help banner and example block still use `--addr`, so a first run prints extra noise | update the banner and example lines to `--redis` | S |
| 2 | `nova-redis help` | the banner is long and repeats the whole exit-codes table on every `-h` | keep the exit table in the banner only, and let each verb's `-h` reference it | S |

## Top 3 fixes
1. Make `install store|bus --dry-run` actually work: print the plan and write nothing without demanding the secrets login, and silence the `it may have written` self-warning when DryRun is set.
2. Fix the effect labels on spill / fn load / acl apply to say `store write` instead of `local write: writes files on this machine`.
3. Add `--op` to the write verbs and `--dry-run` to fn load, so writes are previewable and retries are idempotent-safe.

## Good, keep
- Refusals name every problem at once with what each flag wants and the next command; the unknown-verb and unknown-flag refusals carry the nearest and the full set.
- The store-free first run really works: `spill --dry-run` and `acl render` need no Redis, and `serve --dry-run` prints the whole plan without touching the machine.
- Two old defects are gone: recall no longer hex-escapes its value, and a Unix socket address is accepted (`--addr unix:///tmp/novaredis.sock` and `--redis /tmp/xx.sock` both dry-run OK).

## What could not be tried
- serve, spill, recall, fn load, acl check, acl apply on a live store: each would start or write to a redis-server, which this review may not do; judged from their help, their refusals and `--dry-run` only.
- install store/bus, uninstall store/bus: they write service units and touch the host service manager; judged from their help and `--dry-run` only.
