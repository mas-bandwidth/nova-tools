# nova-redis USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 8/10

## Reasons

The first run `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi` exits 0 and prints `SPILL OK key=ada:note ttl=10m0s expires=... bytes=2 store=127.0.0.1:6379 written=0 dry_run=true` with no store and no guessing. The real job this tool exists for is scratch with a TTL; the dry-run form and the refused form (no --addr, bad --addr, bad --ttl) each name the problem, the next command, and exit 2 before anything is dialled. The four provoked refusals all do exactly that: missing `--addr` says `--addr is required ... refusing to guess; run: nova-redis help`, an unknown flag lists every flag spill has, an unknown verb lists the verbs, and a bad value names the flag and the format it wanted. `spill --json --dry-run` returns one JSON object with the facts as typed values and the same content as the line, so a program could act on it without guessing. `nova-redis acl render` opens no store and prints the ACL users and families, exit 0.

What costs the score: `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` still dials the store, exits 1, and stderr ends with `ACL-APPLY FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` — while the flag help says `--dry-run print what the verb would write and write nothing`. That is the one broken promise. Smaller: `help acl check` and `help acl apply` show no example section, and `recall --json` on a refused call puts the real next step inside `facts.err` while `result.remedy` stays the generic `nova-redis help`. The verbs that need a real service (recall, fn load, fn check, acl check, acl apply against a live store) could not be tried, and the Reasons above rate their refusal and help paths only.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | exits 1 and stderr says the verb may have written, while the flag help says write nothing; the dry-run promise is broken | honour --dry-run before opening the store and print the would-set plan | S |
| 2 | `nova-redis help acl check` | no example section, so a cold user gets flags and exit codes but no sample to copy | add one Example per ACL verb in cmd/nova-redis/acl.go | S |
| 3 | `nova-redis recall --json --addr 127.0.0.1:6379 --owner ada --name missing` | JSON `result.remedy` is the generic `nova-redis help` while the classified next step sits inside `facts.err`; a JSON-only caller gets a useless remedy | set result.remedy to the classified next step | S |

## Good, keep

The refusal grammar: one problem per line, all problems in one run, each with `; run:`. The `spill --dry-run` path that checks every flag and the login before printing the write. The `--json` envelope on spill and recall that carries the value exactly.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| acl apply's remedy names one of four missing users | STILL THERE | cmd/nova-redis/acl.go:334 remedy names only the first unsourced user |
| recall hex-escapes the value | CHANGED | docs/CLI.md:2045 says the line escapes spaces and recall --json carries the value exactly |
| --addr refuses the socket nova-table's first run makes | STILL THERE | `nova-redis spill --dry-run --addr /tmp/redis.sock --owner ada --name n --ttl 10m --value v` exits 2 with `--addr "/tmp/redis.sock" is not <host:port>` |
