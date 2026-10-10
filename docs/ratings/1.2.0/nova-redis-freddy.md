# nova-redis READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 (claude-opus-5-5), harness Claude Code
Build: b250530c1ab3
READ: 8/10
USE: 8/10

## Reasons

The build is the sprint base tip b250530c1ab3, built from source on a Linux
bench; no v1.2.0 tag exists on the remote yet, so the binary's version line
reads `nova-redis v1.0.1-0.20261006143256-b250530c1ab3`. Read cold from
`nova-redis help`, `help fn`, every verb's `-h` and docs/SPEC-REDIS.md, then
used in a throwaway directory on that bench. No store was started (the card
forbids a server on the machine), so the dialling verbs were used against a
dead loopback port and a missing socket path; everything that needs no store
ran for real: spill --dry-run in lines and JSON, acl render, acl apply
--dry-run, serve --dry-run with and without --users, version, and every
refusal class.

READ 8. The banner says what, how, where the password comes from and that the
dry-run needs no store, in six lines, then the usage, the exit table and an
example block that runs as printed. Every verb has its own `-h` with flags,
the help line it came from and an effect line. The spec now lists the acl
verbs and the restart test runs. What keeps it from 10: the effect line calls
spill, fn load and acl apply "local write: writes files on this machine",
though each writes to the store at --addr, which may be another machine
(finding 1); the top help still promises --max for "a verb that lists", and
no verb here lists (finding 4); every verb's `-h` repeats the whole tool's exit
table, which omits the acl verbs' outcomes (finding 7); the help and spec say
--addr is `<host:port>` only, while the code takes a socket path (finding 6);
the spec says an unreachable store is a FAIL line and that only spill,
recall and the fn verbs dial (finding 8); docs/CLI.md's nova-redis section has
no `### First run` (finding 11); and comments still tell tickets (finding 13).

USE 8. Two jobs went end to end with no wrong guess from this tool's help:
planning a spill (`spill --dry-run`, one line, `written=0`, the same facts as
one JSON object) and planning the ACL (`acl render`, then `acl apply
--dry-run`, which now prints four `ACL WOULD-SET` lines and exits 0 without
dialling). serve --dry-run refused `0.0.0.0` and a relative `--dir` in one run,
and refused `--users ada` against an empty store naming the file and the
recovery. A spill with four bad flags named all four at once; `--ttl 10` said
`try 10m`; `--user ada` with an empty password variable was refused before
any dial. Exit codes held: 0 on every plan, 2 on every refusal and every dead
store. What keeps it from 10: acl check and acl apply on a dead store give a
login remedy, not "start the store or correct the address" (finding 2); an
explicitly named `--password-env` that is empty is dropped silently when no
--user is given, and the dial goes out as "no password" (finding 3); recall
and fn load have no --dry-run (finding 5); fn check, acl check, acl render and
serve take no --json, so the inspection verbs an AI most wants to parse are
prose only (finding 9); and the fn verbs' lines lead `OK nova_sprint` and
`FAILED nova_sprint` with no verb, while the refusals spell `FN-CHECK
REFUSED` and the failures `ACL CHECK FAILED` (finding 10).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-redis/main.go:142 | `spill -h`, `fn load -h` (fn.go:63) and `acl apply -h` (acl.go:211) say `effect: local write: writes files on this machine`; each writes to the store at --addr, which may be another machine, and none writes a local file, so an AI judging blast radius from the effect line misjudges it | add a store-write effect to pkg/tool ("store write: writes to the store at --addr") and use it on the three verbs | S |
| 2 | cmd/nova-redis/acl.go:271 | `acl check --addr 127.0.0.1:1` and `acl apply --addr 127.0.0.1:1` answer `connection refused` with `remedy="log in as a user that may run ACL GETUSER, ACL CAT and ACL SETUSER..."`; the store never answered, so the remedy sends the reader to fix a login that was never tried, where spill says `start the store or correct the address` | choose the remedy by cause as fn.go does: unreachable gets the address remedy, a refused login the login one | S |
| 3 | cmd/nova-redis/main.go:343 | `spill --addr 127.0.0.1:1 --password-env NOPE_UNSET ...` with no --user dials `as the default user, no password`; the variable the caller named explicitly is empty and nothing says so, so on a store that wants a password the next error is a refused login, not the empty variable | when --password-env is given as a flag and the variable is empty, refuse before the dial as the --user case does | S |
| 4 | pkg/tool/tool.go:512 | the top help says `A verb that lists takes --max <n>`; no nova-redis verb lists, and `recall --max 5` is refused as an unknown flag | print the --max sentence only for a tool with a verb that lists | S |
| 5 | cmd/nova-redis/main.go:178 | recall and fn load have no --dry-run (`recall --dry-run` and `fn load --dry-run` are refused as unknown flags), so the read path and the library deploy cannot be planned store-free, while spill, serve and acl apply can | add --dry-run printing the key or the library digest, the store and the login each would use | S |
| 6 | cmd/nova-redis/main.go:284 | `spill --addr <dir>/redis.sock` is accepted and dialled as a Unix socket (main.go:400), but the flag help, the usage and docs/SPEC-REDIS.md:19 all say `<host:port>` only, so a reader with the socket nova-table makes would not try it | say `<host:port> or the absolute path of a Unix socket` in the flag, the usage and the spec | S |
| 7 | cmd/nova-redis/main.go:121 | every verb's `-h` quotes the whole tool's exit table, so `version -h` lists recall's misses and serve's stops, and the table names no outcome of acl check or acl apply (drift found, a user set, a refusal for a missing --password-env-for) | give each verb its own ExitTable, and write the acl verbs' outcomes into theirs | S |
| 8 | docs/SPEC-REDIS.md:50 | the spec says the verbs that dial are spill, recall, fn load and fn check (also main.go:17), omitting acl check and acl apply; and line 58 says an unreachable store is `one FAIL line`, while spill and recall print `SPILL REFUSED ... class=unreachable` | name all six dialling verbs, and write the unreachable line as the verbs print it | S |
| 9 | cmd/nova-redis/fn.go:78 | fn check, acl check, acl render and serve refuse --json (`unknown flag --json`); the inspection verbs whose answers an AI acts on are prose only, while spill, recall and version give one object | give every verb --json through the shared result type | M |
| 10 | cmd/nova-redis/fn.go:154 | fn load and fn check print `OK nova_sprint ...` and `FAILED nova_sprint ...` (fn.go:112) with no verb, the refusals spell `FN-CHECK REFUSED` and `ACL-RENDER REFUSED` with a hyphen, and the acl failures `ACL CHECK FAILED` as separate words: three grammars for one tool | lead every line with the verb in one spelling, then OK, REFUSED or FAILED | S |
| 11 | docs/CLI.md:2399 | the nova-redis section (2399 to 2462) has no `### First run`; the neighbours open with one | add version, spill --dry-run and acl render as the first run, with their output | S |
| 12 | README.md:29 | the trial is still a live spill against a store the reader must already run, and the row names only scratch values; the banner's own first run is the store-free spill --dry-run, and the acl and fn verbs are absent | make the trial the dry-run and name the ACL and function-library verbs in the row | S |
| 13 | cmd/nova-redis/serve_test.go:4 | comments still tell tickets (`nova-tools #2281 and #3879`; pkg/redisfn/redisfn.go:72 and docs/CLI.md:2461 `#3620`); a cold reader cannot resolve them | state the rule in present tense and drop the ticket numbers | S |
| 14 | pkg/nsprint/verbflag/verbflag.go:111 | `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn load?`, a remedy identical to the input, with nothing saying the verb is two arguments | match the joined form, or say `fn load is two words: run nova-redis fn load -h` | S |

## Good, keep

- spill --dry-run is a bounded store-free plan: key, ttl, expiry, bytes, store and `written=0` in one line, and the same facts as one JSON object.
- acl apply --dry-run is now a real dry run: four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true ... would=4` at exit 0, no dial.
- Refusals name every problem in one run and say why (`an unbounded key is a bug`); serve refuses a wildcard bind and a relative --dir before anything starts, and a --users store missing a user before anything is written.
- Exit codes are honoured exactly: 0 on every plan, 2 on every refusal and every dead store, on lines and --json alike.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| acl apply --dry-run dials the store and may write (USE 1.1.0) | FIXED | `acl apply --dry-run --addr 127.0.0.1:1` printed four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true users=4 set=0 would=4`, exit 0, with nothing listening |
| --addr refuses the socket nova-table's first run makes (USE 1.1.0) | FIXED, undocumented | `spill --addr <dir>/redis.sock` dialled `unix .../redis.sock` and refused `no such file or directory`; the help still says `<host:port>` (finding 6) |
| spec verb list omits the acl verbs (READ 1.1.0, both raters) | FIXED | docs/SPEC-REDIS.md:23-25 lists acl render, check and apply; the dialling-verbs sentence still omits them (finding 8) |
| restart test opens with an unconditional t.Skip (READ 1.1.0) | FIXED | cmd/nova-redis/serve_functional_test.go:25 runs the restart against a real redis-server |
| --max promised for a verb that lists (USE 1.1.0) | STILL THERE | finding 4 |
| "fn load" quoted gives a self-referential remedy (USE 1.1.0) | STILL THERE | finding 14 |
| recall has no --dry-run (USE 1.1.0) | STILL THERE | finding 5 |
| success is several grammars (READ 1.1.0) | STILL THERE | finding 10 |
| docs/CLI.md has no First run for nova-redis (READ 1.1.0, both raters) | STILL THERE | finding 11 |
| README trial is a live spill (READ 1.1.0) | STILL THERE | finding 12 |
| ticket numbers in comments (READ 1.1.0, both raters) | STILL THERE | finding 13 |
| acl apply's remedy names one of several missing users (READ 1.1.0) | NOT RE-SEEN | needs a live store; none was started |
