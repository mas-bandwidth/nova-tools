# nova-redis USE rating, nova-tools 1.1.0

Rater: GLM-5.3-flash
Build: 63814fcd5504
Score: 7.5/10

## Reasons
Cold, store-free USE rating at the head: no store was started, and nothing listens on 127.0.0.1:6379 here, so the rating is what the help and the refusals say, plus the store-free plans the help offers. The help offers no in-memory address form, so the store-free surfaces are `spill --dry-run`, `acl render`, `version` and `help`. Every command ran in the job's scratch directory.

What was tried, all clean: `nova-redis version`; `nova-redis help`, then `help` for spill, recall, fn, acl and serve, plus `-h` for every sub-verb; `nova-redis acl render`; `spill --dry-run` plain and with `--json`; and all four provoked refusals — missing required flags, unknown flag, unknown verb, bad value — plain and through `--json`. Exit codes matched the help every time: 0 for the clean runs, 2 for usage errors and an unreachable store, 1 for the acl apply store refusal.

Could not be tried without a store: a real spill (the write itself), recall of a real value, fn load, fn check, acl check, a real acl apply, and serve, which would run redis-server, which the rules forbid on this machine; serve is judged from its help only. So the earlier hex-escape and missing-users remedies were not re-seen.

The refusal discipline is exemplary: all four missing flags named at once, each with its reason and the next command; the unknown-flag refusal lists the verb's full flag set and the specific `-h`; the bad value echoes what was given and suggests (try 10m); through `--json` the same refusal is one object with every problem in its "why" list. The store-free spill plan is bounded and act-on-able: full key, ttl, absolute expiry, byte count, the store, written=0, dry_run=true. The help is precise, repeats the usage per verb, and states each verb's effect (writes files or reads) and the same exit-code table.

What keeps the score at 7.5: acl apply --dry-run breaks the contract its flag description states — it dials the store, so the one verb an AI would plan with before touching users cannot be planned without a store, and on an absent store it answers two contradicting lines, one an internal guard naming a Go field and warning "it may have written" when nothing could have been written; the remedy bound to that failure tells an AI to check login credentials for a connection-refused problem. The help's first-run line ("the --dry-run line needs no store") is true of the spill example only. The plan echoes --ttl 10m as 10m0s.

A 10 would need: a store-free plan for every --dry-run that exists (or an up-front refusal saying the plan needs a store); remedies bound to the failure class, an unreachable store getting start-the-store rather than a login check; a value recalled in the form it was spilled; and a first-run line that says exactly which dry-runs need no store.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | The flag promises "print what the verb would write and write nothing", but the verb dials, and with no store it answers two contradicting lines: "ACL APPLY FAILED store=127.0.0.1:6379 err=redis at 127.0.0.1:6379 as the default user, no password: unreachable: dial tcp 127.0.0.1:6379: connect: connection refused remedy=\"log in as a user that may run ACL GETUSER, ACL CAT and ACL SETUSER: check --user (NOVA_REDIS_USER) and the password in NOVA_REDIS_PASSWORD, then nova-redis acl apply --addr 127.0.0.1:6379\"" and then "ACL-APPLY FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written" — an internal guard naming a Go field, telling an AI a plan-run may have written when nothing could have been | Make acl apply --dry-run print its plan without dialling, or refuse up front ("acl apply --dry-run needs the store to diff against"); keep the guard internal | M |
| 2 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | The remedy for a connection-refused store tells an AI to fix login ("check --user (NOVA_REDIS_USER) and the password in NOVA_REDIS_PASSWORD"); no credential change makes an unreachable store reachable, and recall's refusal of the same failure correctly says "start the store or correct the address" | Bind remedies to the failure class: an unreachable store gets start the store or correct --addr; the login remedy belongs to an auth refusal | S |
| 3 | `nova-redis help` | The first-run line "the --dry-run line needs no store; spill and recall need a Redis at 127.0.0.1:6379" reads as "--dry-run needs no store" for every verb; only spill --dry-run is store-free, and acl apply --dry-run dials | Say which dry-runs need no store: "spill --dry-run needs no store; acl apply --dry-run reads the store it is given" | S |
| 4 | `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi` | The plan echoes the ttl as Go renders it, ttl=10m0s, not the 10m that was given, and the JSON repeats 10m0s, so an AI quoting the plan back types a duration it never chose | Echo the duration as given and keep the absolute expires line for precision | S |
| 5 | `nova-redis spill --addr 127.0.0.1:6379` | The four missing-flag lines each end "run: nova-redis help" while the unknown-flag line of the same verb ends "run: nova-redis spill -h"; the specific pointer is one step nearer the answer | End every spill refusal with "run: nova-redis spill -h" | S |

## Good, keep
- Every refusal names every problem at once and ends with the exact command to run next; with --json the same refusal is one object with the whole list in its "why".
- The store-free spill plan is bounded and act-on-able: key=ada:note, ttl, absolute expiry, bytes, the store, written=0, dry_run=true.
- The password never passes on the command line (--password-env names the variable, never the value), and acl render ends in a checksum an AI can compare (library=11dc308260975247).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| acl apply's remedy names one of four missing users (rater 8, 2026-10-02) | CHANGED | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` answers one failure line with the error bound in and a runnable remedy; the missing-users path needs a store, so only the unreachable path was seen |
| recall hex-escapes the value (rater 8, 2026-10-02) | CHANGED | `nova-redis help recall` at the head documents no encoding and `nova-redis recall --addr 127.0.0.1:6379 --owner ada --name note` with no store answers RECALL REFUSED class=unreachable before any value is seen; the value path needs a store, so the escape was not re-seen |
| --addr refuses the socket nova-table's first run makes (rater 8, 2026-10-02) | STILL THERE | `nova-redis recall --addr nosuch.sock --owner ada --name note` answers "RECALL REFUSED: --addr \"nosuch.sock\" is not <host:port>; refusing to guess; run: nova-redis help" and the help offers --addr only as <host:port> |
| a 10-rated rater praised bounded store-free plans and aggregated JSON refusals (2026-10-02) | STILL THERE | `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi` prints "SPILL OK key=ada:note ttl=10m0s expires=2026-10-04T14:13:43Z bytes=2 store=127.0.0.1:6379 written=0 dry_run=true" and `nova-redis spill --json --addr 127.0.0.1:6379` answers one JSON object whose "why" holds all four missing flags |
