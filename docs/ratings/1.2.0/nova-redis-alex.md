# nova-redis READ and USE rating, nova-tools 1.2.0

Rater: poolside/laguna-s-2.1 (openrouter/poolside/laguna-s-2.1), harness opencode
Build: 96b7601e9689
READ: 8/10
USE: 8/10

## Reasons

The build is the sprint base tip 96b7601e9689, built from source on a Linux bench
with `go build ./cmd/nova-redis`. No v1.2.0 tag exists on the remote yet, so the
binary's version line reads `nova-redis v1.0.1-0.20261007015736-96b7601e9689`.
Read cold from `nova-redis help`, `help <verb>`, every verb's `-h`, the spec
(docs/SPEC-REDIS.md) and docs/CLI.md, then used in a throwaway directory on that
bench. No store was started (the card forbids a server on the machine), so the
dialling verbs were used against a dead loopback port and a missing socket path;
the store-free verbs ran for real: spill --dry-run in lines and JSON, acl render,
acl apply --dry-run, serve --dry-run with and without --users, install store
--dry-run, uninstall store --dry-run, version, and every refusal class.

READ 8. The banner answers what, how, where the password comes from and that the
dry-run needs no store, in six lines, then the usage, the exit table and an
example block that runs as printed. Every verb has its own `-h` with flags, the
help line it came from and an effect line. The spec lists all its verbs and
documents the acl and fn grammars. What keeps it from 10: the effect line says
"local write: writes files on this machine" on three verbs that write to the store
(finding 1); the top help still promises --max for "a verb that lists" when no verb
lists (finding 4); every verb's `-h` repeats the whole tool exit table, which omits
the acl verbs' outcomes (finding 7); the help and spec say --addr is `<host:port>`
only while the code takes a socket path (finding 6); the spec's dialling-verbs
sentence still omits the acl verbs (finding 8); the spec omits the install and
uninstall verbs entirely (finding 15); the spec does not document the acl verbs'
FAILED line on an unreachable store (finding 16); docs/CLI.md's nova-redis section
has no `### First run` (finding 11); the README row still calls it a scratch-value
store and never names the acl or fn verbs (finding 12); and ticket numbers survive
in comments and specs (finding 13).

USE 8. Two jobs went end to end with no wrong guess from this tool's help:
planning a spill (`spill --dry-run`, one line, `written=0`, the same facts as one
JSON object) and planning the ACL (`acl render`, then `acl apply --dry-run`,
which prints four `ACL WOULD-SET` lines and exits 0 without dialling). serve
--dry-run refused `0.0.0.0` and a relative `--dir` in one run, and a `--users`
store missing users before anything starts; install and uninstall --dry-run
printed their plans store-free. A spill with four bad flags named all four at
once; `--ttl 10` said `try 10m`; `--user ada` with an empty password variable was
refused before any dial. Exit codes held: 0 on every plan, 2 on every refusal and
every dead store. What keeps it from 10: acl check and acl apply on a dead store
give a login remedy, not "start the store or correct the address" (finding 2); an
explicitly named `--password-env` that is empty is dropped silently when no
--user is given (finding 3); recall and fn load have no --dry-run (finding 5);
fn check, acl render, acl check, acl apply, fn load and every install/uninstall
verb take no --json, so the inspection verbs an AI most wants to parse are prose
only (finding 9); fn load and fn check print `FAILED nova_sprint` with no verb,
while acl verbs print `ACL CHECK FAILED` and the refusals spell `FN-LOAD REFUSED`
(findings 10); and a two-word verb passed as one quoted argument gives a
self-referential remedy (finding 14).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-redis/main.go:152, fn.go:63, acl.go:211 | spill, fn load and acl apply `-h` print `effect: local write: writes files on this machine`; each writes to the store at --addr, and none writes a local file (spill and fn load write nothing local), so an AI judging blast radius from the effect line misjudges it | add a store-write effect to internal/tool ("store write: writes to the store at --addr") and use it on spill and fn load; acl apply writes both the store and the ACL file, so state both | S |
| 2 | cmd/nova-redis/acl.go:271 | `acl check --addr 127.0.0.1:1` and `acl apply --addr 127.0.0.1:1` answer `ACL CHECK FAILED` / `ACL APPLY FAILED` with `remedy="log in as a user that may run ACL GETUSER, ACL CAT and ACL SETUSER..."`; the store never answered, so the remedy sends the reader to fix a login that was never tried, where spill and recall say "start the store or correct the address" | choose the remedy by cause as fn.go does: unreachable gets the address remedy, a refused login the login one | S |
| 3 | cmd/nova-redis/main.go:343 | `nova-redis spill --addr 127.0.0.1:1 --password-env NOPE_UNSET ...` with no --user dials `as the default user, no password`; the variable the caller named explicitly is empty and nothing says so, so on a store that wants a password the next error is a refused login, not the empty variable | when --password-env is given as an explicit flag and the variable is empty, refuse before the dial as the --user case does | S |
| 4 | internal/tool/tool.go:522 | the top help says `A verb that lists takes --max <n>`; no nova-redis verb lists, and `recall --max 5` is refused as an unknown flag | print the --max sentence only for a tool with a verb that lists | S |
| 5 | cmd/nova-redis/main.go:188 | recall has no --dry-run (`recall --dry-run` is refused as an unknown flag), so the read path cannot be planned store-free, while spill, serve, acl apply and the install verbs can | add --dry-run printing the key, the store and the login recall would use | S |
| 6 | cmd/nova-redis/main.go:284 | `spill --addr <dir>/redis.sock` is accepted and dialled as a Unix socket, but the flag help, the usage and docs/SPEC-REDIS.md:19 all say `<host:port>` only, so a reader with the socket nova-table makes would not try it | say `<host:port> or the absolute path of a Unix socket` in the flag, the usage and the spec | S |
| 7 | cmd/nova-redis/main.go:121 | every verb's `-h` quotes the whole tool's exit table, so `version -h` lists recall's misses and serve's stops, and the table names no outcome of acl check or acl apply (drift found, a user set, a refusal for a missing --password-env-for) | give each verb its own ExitTable, and write the acl verbs' outcomes into theirs | S |
| 8 | docs/SPEC-REDIS.md:50 | the spec says the verbs that dial are `spill`, `recall`, `fn load` and `fn check`, omitting `acl check` and `acl apply`, which also dial | name all six dialling verbs | S |
| 9 | cmd/nova-redis/fn.go:78, acl.go:182 | fn check, acl render, acl check, acl apply, fn load and every install and uninstall verb take no --json; the inspection verbs whose answers an AI acts on are prose only, while spill, recall, version and serve give one object | give every verb --json through the shared result type | M |
| 10 | cmd/nova-redis/fn.go:154 | fn load and fn check print `FAILED nova_sprint ...` with no verb, the acl failures print `ACL CHECK FAILED` and `ACL APPLY FAILED` as separate words, and the refusals spell `FN-LOAD REFUSED` and `ACL-RENDER REFUSED` with a hyphen: three grammars for one tool | lead every line with the verb in one spelling, then OK, REFUSED or FAILED | S |
| 11 | docs/CLI.md:2498 | the nova-redis section (2498 to 2565) has no `### First run`; every neighbour opens with one | add version, spill --dry-run and acl render as the first run, with their output | S |
| 12 | README.md:29 | the trial is still a live spill against a store the reader must already run, and the row names only scratch values; the banner's own first run is the store-free spill --dry-run, and the acl and fn verbs are absent | make the trial the dry-run and name the ACL and function-library verbs in the row | S |
| 13 | cmd/nova-redis/serve_test.go:4, serve_restart_test.go:6, internal/redisfn/redisfn.go:72, docs/CLI.md:2565 | comments still tell tickets (`nova-tools #2281 and #3879`, `nova-tools #3879`, `nova-tools #3620`); a cold reader cannot resolve them | state the rule in present tense and drop the ticket numbers | S |
| 14 | internal/tool/tool.go:226 | `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn load?`, a remedy identical to the input, with nothing saying the verb is two arguments | match the joined form, or say `fn load is two words: run nova-redis fn load -h` | S |
| 15 | docs/SPEC-REDIS.md | the spec is normative and names ten verbs (serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, version, help), but the binary ships four more (install store, install bus, uninstall store, uninstall bus) that docs/CLI.md documents and the banner lists; a spec a reader is told to trust before the code does not agree with it | add the four install and uninstall verb forms to the spec's verb block | M |
| 16 | docs/SPEC-REDIS.md:63 | the spec documents acl check printing `ACL OK`, `ACL MISSING`, or `ACL DRIFT` and acl apply printing `ACL APPLY OK`, but the verbs print `ACL CHECK FAILED` and `ACL APPLY FAILED` on an unreachable store (exit 2), as the fn verbs' section documents for them; a cold reader of the spec cannot predict the acl verbs' dead-store line | document the FAILED line for acl check and acl apply on an unreachable store, matching the fn verbs' section | S |

## Good, keep

- spill --dry-run is a bounded store-free plan: key, ttl, expiry, bytes, store and
  `written=0` in one line, and the same facts as one JSON object. `recall --json`
  on a dead store gives the same shape with `status: refused` and the remedy.
- acl apply --dry-run is a real dry run: four `ACL WOULD-SET` lines and
  `ACL APPLY OK dry-run=true ... would=4` at exit 0, no dial.
- serve --dry-run validates bind, port and dir before anything starts, refuses a
  wildcard bind and a relative --dir in one run, and refuses a --users store
  missing a user before anything is written, with the recovery named.
- Refusals name every problem at once and say what each flag wants: --addr empty,
  --owner with a colon, --ttl 0 or 10 (not a duration), --name with whitespace, a
  missing --value — each refused before any dial with a `run: nova-redis help`.
- Exit codes are honored exactly: 0 on every plan, 2 on every refusal and every
  dead store, on lines and --json alike; a spill whose reply is lost is
  `SPILL UNCONFIRMED` (exit 1) with a recall remedy, never a blind retry.
- The banner's example block runs as printed: version, spill --dry-run, spill
  against 127.0.0.1:6379, recall.
- install and uninstall --dry-run print their unit plan and the path they would
  touch, writing nothing, and refuse `install` and `uninstall` bare with the verbs
  they take.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| acl apply --dry-run was not a dry run (USE 1.1.0) | FIXED | `acl apply --dry-run --addr 127.0.0.1:6379` printed four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true users=4 set=0 would=4`, exit 0, with nothing listening |
| --addr refuses the socket nova-table's first run makes (USE 1.1.0) | STILL THERE | `spill --addr /tmp/nope/redis.sock` dialled `unix /tmp/nope/redis.sock` and refused `not a directory`; the help still says `<host:port>` (finding 6) |
| spec verb list omits the acl verbs (READ 1.1.0, both raters) | FIXED | docs/SPEC-REDIS.md:23 lists acl render, check and apply; the dialling-verbs sentence still omits them (finding 8) |
| restart test opens with an unconditional t.Skip (READ 1.1.0) | FIXED | cmd/nova-redis/serve_functional_test.go:25 runs the restart against a real redis-server |
| --max promised for a verb that lists (USE 1.1.0) | STILL THERE | finding 4 |
| "fn load" quoted gives a self-referential remedy (USE 1.1.0) | STILL THERE | finding 14 |
| recall has no --dry-run (USE 1.1.0) | STILL THERE | finding 5 |
| acl apply's remedy is a login on a dead store (USE 1.1.0) | STILL THERE | `acl check --addr 127.0.0.1:1` answered `ACL CHECK FAILED ... remedy="log in as a user that may run ACL GETUSER..."`, exit 2 (finding 2) |
| --password-env empty is dropped silently without --user (USE 1.2.0) | STILL THERE | `spill --password-env NOPE_UNSET --addr 127.0.0.1:1` dialled `as the default user, no password` (finding 3) |
| no --json on the inspection verbs (USE 1.2.0) | STILL THERE | `acl render --json` and `fn check --json` both refused as unknown flags (finding 9) |
| success is several grammars (READ 1.1.0) | STILL THERE | finding 10 |
| docs/CLI.md has no First run for nova-redis (READ 1.1.0, both raters) | STILL THERE | finding 11 |
| README trial is a live spill (READ 1.1.0) | STILL THERE | finding 12 |
| ticket numbers in comments (READ 1.1.0, both raters) | STILL THERE | finding 13 |
| effect says "local write" on store-writing verbs (READ 1.2.0) | STILL THERE | finding 1 |
| every verb's -h repeats the whole exit table (READ 1.2.0) | STILL THERE | finding 7 |
| --addr help says <host:port> only (USE 1.2.0) | STILL THERE | finding 6 |
| install and uninstall verbs missing from the spec (READ 1.2.0) | NEW | docs/SPEC-REDIS.md lists ten verbs; install store, install bus, uninstall store, uninstall bus are in the binary and CLI.md but not the spec (finding 15) |
| acl verbs' FAILED line undocumented (READ 1.2.0) | NEW | `acl check --addr 127.0.0.1:1` printed `ACL CHECK FAILED`, exit 2, not described in SPEC-REDIS.md (finding 16) |
| acl apply's remedy names one of several missing users (READ 1.1.0) | NOT RE-SEEN | needs a live store; none was started |
