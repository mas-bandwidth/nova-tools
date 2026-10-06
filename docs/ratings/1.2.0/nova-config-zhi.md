# nova-config READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 (claude-opus-5-5), harness Claude Code
Build: 193a6f8002d0
READ: 7/10
USE: 7/10

## Reasons

The build is the sprint base tip 193a6f8002d0, built from source on a Linux
bench; no v1.2.0 tag exists on the remote yet, so the version line reads
`nova-config v1.0.1-0.20261006150140-193a6f8002d0`. Read cold from
`nova-config help`, `kinds`, every top verb's `-h`, every machine verb's `-h`
and the `-h` of fleet set, sprint set, friend add, loop add, route add and
tier set, then docs/CLI.md's nova-config section. Used on a throwaway
`--file` store in a scratch directory on that bench: migrate (print, dry-run,
real), a machine's add, set, list, show, history, width and remove, a loop, the
fleet row, a friend, the sprint row, a route and its tier, status, inventory
from the shipped fixture and from hand-written ones, machine self, login
--check, and every refusal class I could reach. No PostgreSQL or Redis was
started (the card forbids a server on the machine), so apply, status --redis,
machine list --redis and inventory --redis were used against a dead loopback
port.

READ 7. The banner says what the tool is, how the rows flow (PostgreSQL the
truth, Redis the copy, the file for a trial) and gives an example block that
runs as printed with no database. Every verb has a `-h` with its effect line,
a worked example and every field's type, default and meaning, and `kinds`
prints the same schema as one line per kind or one JSON object. The command
file is now 648 lines with the kind verbs, apply, inventory, login and status
in their own files. What keeps it from 10: the friend kind's help and docs
gender every friend "she" and "her" (finding 3); the sprint and route help is
walls (`--answer_rules_off` is one 1,000-character sentence, finding 9), and
the docs/CLI.md kinds paragraph is still one 1,292-character sentence
(finding 10); `--seat` means two different things on two verbs of one kind
and the help never says so (finding 1); `login --as` is a seat while every
other `--as` is an actor (finding 6); every verb's `-h` repeats the whole
tool's exit table, `machine self`'s private exit 3 included (finding 7); the
help for machine remove, show, fleet set, sprint set, friend add, loop add and
tier set has no `from nova-config help` line where its neighbours do, and the
login and logout `-h` have no example and print the effect after the exit
table (finding 13); the README row still ends "This is the Nova fleet
configuration model." (finding 12).

USE 7. One job went end to end with no wrong guess: migrate a file, add a
machine, change its width, list, show, read the history (`width=4>6`), read
`machine width` (now in the result envelope), dry-run a remove (refused for
the fleet's store machine, accepted for another), remove, and read the history
of the removed row. The fleet row, a friend, the sprint row, a route and a
two-turn tier went in on the first try once the help was read. Refusals are
specific: all missing required flags at once, two bad integers at once, a
slash in --provider beside a bad --deadline, `--enabled false` asking for the
measured reason in --note, a password in --pg refused before any dial. What
keeps it from 10: `machine set --seat` and `loop set --seat` cannot change the
row's seat field (the flag is taken by the store's seat profile, and on a
--file store it refuses), so a machine that is the fleet's store can never
have its seat changed at all (finding 1); every refusal under --json still
leaves stdout empty (finding 2); every dial of a dead Redis prints four
`pool.go:762` library lines before the refusal (finding 4); the inventory
fixture refusal still dumps a Go struct type, still omits redis_port and
pg_dsn from the shape it asks for, and points a fixture user at a store write
(finding 5); an unknown flag still hides a bad value beside it (finding 8);
the file store stamps every row migrate makes with 2023-11-14T22:13:20Z
(finding 11).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-config/kindverbs.go:123 | on set, the machine and loop kinds drop their own `seat` field and take `--seat` as the store's seat profile instead (main.go:461), so `machine set m1 --seat s2 --as a1 --file try.json` exits 2 `--seat and --file are exclusive ... set names no field; the fields are user, seat, ...`, naming seat as a field it cannot set; without --file the flag selects a seat profile in seats.tsv (its help says so), never the field; the only route is remove and add, which is refused for the fleet's store machine, so that machine's seat can never change | rename the store selector (`--seat-profile`, or --store-seat) on every verb, so `--seat` is always the field, and say in the add and set help which one it is | M |
| 2 | cmd/nova-config/main.go:341 | refuse and refused write one line to stderr whatever --json says: `machine add m9 --file try.json --json` and `apply --dry-run --redis 127.0.0.1:1 --file try.json --json` exit 2 with stdout empty, and `machine show m2 --file try.json --json` exits 1 with stdout empty; a caller reading stdout sees nothing on the failure path | when --json is set, print the result object with status refused, the exit, the reason and the remedy on stdout | M |
| 3 | internal/config/kind.go:412 | the friend kind's Doc and field help call every friend "she" and "her" (`her slots, which tiers she can do`, lines 412 to 419, and `a friend's card she has not started` at 447; also internal/config/redis.go:73 and :379, and docs/CLI.md:2355); a friend's pronouns are not the tool's to assume | write the help without pronouns: `the friend's slots`, `the tiers it may be dealt`, `the jobs the friend works at once` | S |
| 4 | cmd/nova-config/main.go:273 | apply, status --redis, machine list --redis and inventory --redis on a dead address print four `redis: ... pool.go:762: redis: connection pool: failed to dial after 5 attempts` lines before the one REFUSED line; internal/redisconn/open.go:87 already installs a quiet logger, but nova-config opens Redis through store.Open, which does not | open through internal/redisconn, or install the same quiet logger in store.Open | S |
| 5 | internal/config/inventory.go:412 | `inventory --fixture bad.yml` dumps `cannot unmarshal !!seq into map[string]struct { User string "yaml:\"user\""; ...}`; the shape it asks for says `fleet (store, coordinator)`, but a fixture with exactly that is refused at inventory.go:207 `fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set ... then nova-config apply --kind fleet`, a store write that does not edit the fixture; `--host nosuch` with --fixture answers `run: nova-config machine list`, which reads no fixture | drop the Go type from the line, name redis_port and pg_dsn in the fleet shape, and when --fixture is given make each remedy name the fixture field to add | S |
| 6 | cmd/nova-config/login.go:320 | `login --as` is `the seat of that store whose file holds the password`, while on every other verb --as is the actor a write is recorded under, and login takes the actor as --friend; the flags print as `<string>` where the rest of the tool names a type (`<seat>`, `<name>`) | name the login flag --secrets-seat, or at least give it the `<seat>` placeholder and say in the help that it is not the actor | S |
| 7 | cmd/nova-config/main.go:122 | every verb's `-h` quotes the whole tool's exit table, so `version -h` and `kinds -h` list migrate --dry-run's ready=no and machine self's `2 not a row, 3 unreadable`; machine self --check still exits 2 for a refusal (not a row) and 3 for a dead store (machine.go:43), where every other verb exits 1 and 2 | give each verb its own exit lines, and map machine self onto the family's 0/1/2 (1 not a row, 2 cannot read) | S |
| 8 | internal/nsprint/verbflag/verbflag.go:113 | `machine add m9 --slots no --bogus 1 ...` names only `unknown flag --bogus`; the bad --slots is found only on the next run, while two bad values are named together | collect the unknown flags with the value errors and print them in one refusal | S |
| 9 | internal/config/kind.go:447 | `sprint set -h`'s --answer_rules_off is one sentence of about a thousand characters naming eleven rules, and the decide_* bars each carry calibration history (AUCs, dates, counts); `route add -h` lists 23 fields | one clause per rule as a list under the flag, and move calibration history to docs/SPEC-NOVA-DECIDE.md, leaving the default and the starting point | S |
| 10 | docs/CLI.md:2355 | the paragraph under the nova-config usage block is one sentence of 1,292 characters holding seven kinds in parentheses; docs/CLI.md:2351 shows `tier set flash|pro` though migrate makes a heavy tier too | one short sentence per kind, and `flash|pro|heavy` | S |
| 11 | internal/config/store.go:247 | the --file store's clock for the rows migrate seeds is `time.Unix(1700000000, 0)`, so `fleet show`, `sprint show` and `tier show` on a fresh file print `created=2023-11-14T22:13:20Z`, a date that never happened to the file | stamp seeded rows with the time migrate ran | S |
| 12 | README.md:30 | the nova-config row still ends `This is the Nova fleet configuration model.`, which says nothing; the row's trial is migrate --print, while the banner's first run is the --file example | end the row on what the tool keeps, and make the trial `nova-config migrate --file try.json` | S |
| 13 | cmd/nova-config/login.go:298 | `login -h` and `logout -h` print no example and put the effect line after the exit table, and login's one effect line is `local write` though `login --check` records nothing; machine remove, show, fleet set, sprint set, friend add, loop add and tier set have no `from nova-config help` line where machine add, set, list, history and width do | give login and logout the shared help layout with an example, give --check an inspection effect, and print the help line on every kind verb | S |
| 14 | cmd/nova-config/main.go:33 | the file still imports the legacy sort package (1.1.0 READ finding 5) | use slices.Sort and drop the import | S |
| 15 | internal/config/kind.go:885 | `route add --deadline -5` says `want a non-negative integer` and `--deadline 0` says `above 0`, one rule in two messages; the help says `above 0` | refuse both with the field's own rule, `want the seconds a card may run, above 0` | S |

## Good, keep

- The --file store: every verb but apply's write runs with no database, the example block runs as printed, and `migrate --file new.json --dry-run` lists all 34 pending migrations without making the file.
- One descriptor per kind gives every kind the same six verbs, flags, refusals and history; `kinds` prints that schema as lines or one JSON object.
- Refusals name every missing required flag and every bad value at once, each with what it wants and a remedy that repeats `--file` so it pastes; `--enabled false` on a route asks for the measured reason.
- --dry-run on a write runs the real checks: removing the fleet's store machine is refused under --dry-run exactly as it is without.
- History shows every change as `field=old>new` with who and when, and survives the row's removal.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| machine width --json prints a bare object (READ and USE 1.1.0) | FIXED | `machine width m1 --file try.json --json` printed `{"result":{"verb":"machine width","status":"ok","exit":0},"facts":{...}}` |
| a 1,646-line main.go (READ 1.1.0, both raters) | CHANGED | cmd/nova-config/main.go is 648 lines; the kind verbs, apply, inventory, login, migrate and status are their own files |
| every refusal under --json leaves stdout empty (USE 1.1.0, both raters) | STILL THERE | finding 2 |
| a failed dial prints Redis pool log lines (USE 1.1.0, both raters) | STILL THERE | finding 4 |
| an unknown flag hides a bad value (USE 1.1.0) | STILL THERE | finding 8 |
| fixture shape omits redis_port and pg_dsn, remedy is a store write, Go type dumped (USE 1.1.0) | STILL THERE | finding 5 |
| machine self's private exit 3 (READ 1.1.0) | STILL THERE | finding 7 |
| the kinds paragraph in docs/CLI.md is one sentence (READ 1.1.0) | STILL THERE | finding 10 |
| the sprint kind's help lists the decide bars in one breath (READ 1.1.0) | CHANGED | each bar now has its own flag help, but each carries calibration history and --answer_rules_off is one long sentence (finding 9) |
| legacy sort import (READ 1.1.0) | STILL THERE | finding 14 |
| README row ends "This is the Nova fleet configuration model." (READ 1.1.0) | STILL THERE | finding 12 |
| apply --dry-run passes where apply refuses (USE 1.1.0) | NOT RE-SEEN | needs a live Redis; on a dead one both refuse the same way |
