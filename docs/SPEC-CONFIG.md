# SPEC-CONFIG: the permanent configuration store

*Normative for `nova-config`. Where the code and this page disagree, one of
them has a bug and the tests decide which.*

## The boundary

Redis is a hot store of data that can be rebuilt, never the place for
permanent data. `nova-config` manages the permanent (non-ephemeral)
configuration, kept cleanly separate from runtime, ephemeral state verbs.

So there are two stores with two jobs:

- **Postgres is the permanent store.** Schema `config`, owned by the
  `nova_config` role, holds every registry of the fleet and the history of
  every change to it. `nova-config` is its one writer.
- **Redis is a copy.** `nova-config apply` writes the configuration into the
  keys the runtime tools read, through the runtime's own Redis Functions, and
  removes what Postgres does not have. Lose Redis: run `nova-config apply`.
  The runtime tools read configuration from Redis and never write it.

**History is not configuration.** Scores, receipts, ledgers, beats, copies,
leases and every other thing a tool writes as it runs stay with the tool that
writes them. `config.history` is the history of the configuration itself,
nothing else.

## Kinds

**The placement rule.** Per-machine facts belong to machines, and global
fleet facts belong to the fleet. The test for every field of every kind: does its value vary from machine to
machine? Then it is a machine field. Is there one value for the whole fleet?
Then it is a fleet field. Neither: it is invented and is not a field. The
same test decides a friend's row: what someone decides for her is
configuration; what she would just know is runtime data in Redis. And the
person coordinating is sprint-global configuration: the
fleet row holds machines only (the store, the coordinator machine); the
sprint row holds who coordinates; a friend's roles are what the deal reads.

Where each field of this cut sits:

| side | fields |
| --- | --- |
| machine (varies per machine) | `user`, `seat`, `slots`, `runners` |
| fleet (one value for the whole fleet) | `store`, `coordinator` (both machines) |
| friend (decided for her) | `slots`, `tiers`, `roles` |
| sprint (one value for the whole sprint) | `coordinator` (a friend) |

A kind is one registry: one table under schema `config`, one Go descriptor
(`internal/config/kind.go`: `Kind`), one migration, one Redis writer. The
grammar is one for every kind:

```
nova-config <kind> add <name> --<field> <value> ... --as <friend>
nova-config <kind> set <name> --<field> <value> ... --as <friend>
nova-config <kind> remove <name> --as <friend>
nova-config <kind> list
nova-config <kind> show <name>
nova-config <kind> history <name>
```

Every kind's table has the same shape: `name text PRIMARY KEY` (the row key,
`^[a-z0-9][a-z0-9-]*$`), the kind's fields as columns of the same names,
`created_at` and `updated_at`. There is no `note` column on any kind: notes
are history, and history lives in git. The CLI's flags, help, refusals, SQL,
typed lines and apply diff are all generated from the descriptor, so every
kind has identical verbs and a new kind adds no verb code.

### Singleton kinds

A kind descriptor may declare `Singleton: true`: a kind of exactly one row,
named as the kind is: there is one fleet and one coordinator at a time. Its
migration creates the row (`INSERT ... ON CONFLICT DO NOTHING`), so the
grammar has no `add`, `remove` or `list`, and its `set`, `show` and `history`
take no name:

```
nova-config fleet set --store <machine> --coordinator <machine> --as <friend>
nova-config fleet show
nova-config fleet history
```

A singleton's `history` with no change yet prints the count line alone and
exits 0 (the row exists; nobody added it). `kinds` says `rows=one`. `status`
prints its revision and no count. Apply reads its one view always (the row
exists on both sides), so its plan is a `SET` of the fields that differ,
never an `ADD` or a `REMOVE`.

### Field types

| type | value | column |
| --- | --- | --- |
| `text` | one line of free text | `text` |
| `int` | a non-negative integer | `integer` |
| `enum` | one word of the field's list | `text` |
| `list` | a comma list of words from the field's list, deduplicated and sorted | `text` |
| `names` | a comma list of names (letters, digits, dashes), deduplicated and sorted | `text` |
| `ref` | the name of a row of another kind; an optional one may be empty | `text` with a foreign key, `NULL` for empty |

A value is canonicalised before it is stored (`Field.Canonical`), so a row
compares equal to its Redis view field by field. `add` refuses a row missing
a required field, a value outside its type, or a flag the kind has not, and
names every problem in one line. `set` changes the fields named and no
other. A `ref` field naming no row is refused (`--store space names no
machine row`), and a row a `ref` field of another kind names cannot be
removed (`machine studio is the --coordinator of the fleet`, `friend rowan
is the --coordinator of the sprint`): the structure enforces it (a foreign
key), the tool names it.

### The kinds of this cut

**`machine`** (`config.machines`): a machine of the fleet, named by its
tailnet host. Every fleet machine is on the tailnet and reachable by ssh
over it, so `ssh <name>` reaches the machine and there is no address field.
The row holds exactly the fields something reads, one reader each, and
nothing invented.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `user` | text | yes | the plays and the seals: `ssh <user>@<name>` | `machine:<m>` |
| `seat` | text | yes | nova-secrets: the seat on that machine (studio, swarm-hulk, ...) | `machine:<m>` |
| `slots` | int | yes | the deal: how many cards it may run; 0 runs none | `machine:<m>:ceiling` (`ns_capacity_machine`) and `machine:<m>` |
| `runners` | int | (0) | the CI play: how many runners it hosts; 0 hosts none | `machine:<m>` |

**Declared and measured.** Measured facts (os, arch, cores, memory) are
never typed and never columns: they come live from the machine's own
heartbeat, `bench:<name>:beat` (`host`, `at`, `load1`, `ncpu`, `cpu`). `machine list` and `machine show` print them
after the declared fields when a Redis is named (`--redis`, else
`NOVA_SPRINT_REDIS`, else `NOVA_REDIS_ADDR`; never the seat: a list that
dials a store nobody named would be a surprise): `os`, `arch`, `cores`
(the beat's `ncpu`), `memory_gb`, and `beat=<rfc3339>`; each `-` when the
beat does not carry it, and `beat=none` alone for a machine with no
beat. Nothing is stored, nothing is typed; what the beat carries is the
beat writer's, not this tool's.

**`fleet`** (`config.fleet`, singleton): the one row of fleet-wide facts.
The coordinator machine is one machine; which friend drives it is the sprint
row's.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `store` | ref machine | | the plays: the machine that runs Redis and Postgres | `fleet:store` |
| `coordinator` | ref machine | | the plays: where the coordinator's loops run; apply: the machine a friend with no beat is charged to | `fleet:coordinator` |

**`friend`** (`config.friends`): what someone decides for a friend. Anything
a friend would just know is runtime Redis data. Where she runs, her harness, her logins and her wake path are hers: her own
presence reports them (`friend:<f>:beat`, `friends:login`,
`friend:<f>:wakepath`), and this tool never writes or reads them as
configuration. Who coordinates is not her field either: it is the sprint's.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `slots` | int | yes | the deal: her desired slots, under the ceiling of the machine she is charged to | `friend:<f>:desired` slots (`ns_capacity_desired`) |
| `tiers` | list: flash, frontier, pro | yes | the deal's tier filter (capacity.lua `filter_ok`): which she can do | `friend:<f>:desired` tiers (`ns_capacity_desired`) |
| `roles` | list: builder, may-hold, reader | | the deal and the routing: what she may hold | `friend:<f>:roles` (`ns_friend_roles`) |

**`sprint`** (`config.sprint`, singleton): the one row of sprint-global
facts.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `coordinator` | ref friend | | the deal and the routing: who holds the coordinator role; `sprint set --coordinator <friend>` is the handover | `sprint:coordinator`, and the `coordinator` word in that friend's `friend:<f>:roles` |

## The schema

Migrations are numbered SQL files compiled into the binary
(`internal/config/migrations/NNNN_<what>.sql`), applied in order, each in its
own transaction with its row in `config.schema_migrations`. A version applied
is never applied again, so `nova-config migrate` on a migrated database
applies nothing and says so (`applied=0`). `0001_schema.sql` makes the
schema, the ledger and the history table; every later file is one kind.

After every migrate the `nova_read` role, when it exists, is granted `USAGE`
on the schema and `SELECT` on every table: a runtime tool that reads
configuration straight from Postgres does it as that role.

```
config.schema_migrations (version integer PK, applied_at timestamptz)
config.history           (id bigserial PK, kind, name, op add|set|remove,
                          before jsonb, after jsonb, actor, at timestamptz)
config.machines          (name PK, "user", seat, slots, runners,
                          created_at, updated_at)
config.fleet             (name PK = 'fleet', store -> machines.name,
                          coordinator -> machines.name, created_at, updated_at;
                          the one row inserted by the migration)
config.friends           (name PK, slots, tiers, roles, created_at, updated_at)
config.sprint            (name PK = 'sprint', coordinator -> friends.name,
                          created_at, updated_at; the one row inserted by
                          the migration)
```

No database has applied `0002_machine.sql` or `0003_friend.sql` in their
first shape (the fleet Postgres on space is not migrated yet), so this cut
rewrote both in place rather than adding an alter.

## History

**Every write is also a history row, in the same transaction.** `add`
records `before = null, after = the row`; `set` records the row before and
after; `remove` records `before = the row, after = null`. `actor` is `--as`
(or `NOVA_FRIEND`), required on every write. There is no path that changes
a row without a record, and no path that edits history.

**A kind's revision** is the greatest `history.id` of its rows, 0 for a
kind never written. It is what `apply` stamps into Redis. Revisions are
global ids, so they rise across kinds and never repeat.

`nova-config <kind> history <name>` prints the rows oldest first, one
`HISTORY` line each, with what changed.

## Apply

`nova-config apply [--kind <k>] [--check]` runs per kind, in kind order:
machines (the ceilings), the fleet row (a friend with no beat is charged to
its coordinator machine), friends, the sprint row:

1. read the kind's rows and revision from Postgres, then the kind's
   `Derive` when it has one (the friend kind adds the `coordinator` word to
   the roles of the friend the sprint row names, so Redis holds it and the
   stored row does not);
2. read the kind's rows from Redis (`Applier.Read`, plain commands, no
   write) and Redis's stamp `config:decl rev:<kind>`;
3. **refuse `CONFLICT`** when the stamp is greater than the Postgres
   revision: a newer Postgres applied it, and this process would roll it
   back. Nothing is written;
4. **plan** the difference: `ADD` for a row Redis lacks, `SET` for one that
   differs in any field (naming the fields), `REMOVE` for a name in Redis that
   Postgres has not. Adds and sets come in the kind's apply order (friends:
   the coordinator first, so `ns_friend_roles` has one to bootstrap and a
   handover writes the new coordinator before the old), then removes by
   name;
5. with `--check`, print the plan as `CHECK` lines and stop; nothing is
   written, not even the function library;
6. install the `nova_sprint` function library only when the store has none
   (`fn.LoadMissing`; a deployed one is never replaced);
7. write every op in order through the runtime's own functions, each write
   carrying `--as` and the idempotency marker `config:<kind>:<rev>` into
   `cap:log`. A refusal from a function (`CEILING`, an actor without the
   coordinator role) stops the apply there, is printed with its remedy, and the
   stamp is not moved;
8. **stamp** `config:decl rev:<kind> = <rev>, at:<kind> = <server ms>`,
   compare-and-set: inside `WATCH config:decl`, the stamp is written only
   while it still reads the value step 2 read; a stamp that moved is
   `CONFLICT`.

A second apply of the same Postgres is a no-op: no `APPLY` line, the counts
zero, the stamp rewritten to the same revision.

### What apply writes, per kind

**machine:** `ns_capacity_machine(m, slots)` for the ceiling (refused
`CEILING` when the friends and benches on it already desire more than
`slots`; cores and memory are never declared, so the call carries none and
derives no budget); the hash `machine:<m>` with user, seat, slots, runners,
rev, at; the set `machines`. slots is read back from the ceiling, the key the
runtime guards on, so a ceiling moved by hand is put back by the next apply.
Remove: refused while any friend or bench desired hash names the machine;
else `machine:<m>`, `machine:<m>:ceiling` and `machine:<m>:budget` are
deleted and the name leaves `machines`.

**fleet:** a plain `SET fleet:store <machine>` and `SET fleet:coordinator
<machine>`, `DEL` for a field the row leaves empty. Never removed.

**friend:** `ns_capacity_desired(friend, f, slots, machine, ..., tiers)`
(registers in `friends`, writes `friend:<f>:desired`, refuses `CEILING`).
The machine is the one her slots are charged to: the `host` her own beat
(`friend:<f>:beat`) reports when she has one (friends may run on any bench),
else the fleet's coordinator machine (`fleet:coordinator`, written a moment
before) as the default charge; neither is a refusal naming `nova-config
fleet set --coordinator <machine>`. `ns_friend_roles(f, roles)` when the
roles differ (the actor must hold the coordinator role in Redis, or nobody
does yet and this row makes the first): the roles written are the row's
plus `coordinator` for the friend the sprint row names. Nothing else: her
logins, wake path and harness are her presence's. Remove: refused while
`friend:<f>:cards:working` has a member, naming the copies; else the
registry member, the desired and roles hashes are removed in one
transaction, with a `config-remove` receipt in `cap:log`; her beat, logins
and wake path stay, they are hers.

**sprint:** a plain `SET sprint:coordinator <friend>`, `DEL` when empty.
Never removed. The handover is `nova-config sprint set --coordinator
stella --as rowan` then `apply`: the sprint kind's own revision moves and
the friend kind's plan is two `SET ... changed=roles`, stella's first.

`machine:<m>`, `machines`, `fleet:*` and `sprint:coordinator` are
nova-config's own keys: no function in the library reads or writes them.

## Lines

One typed line per event; values go through `oneline.Field`, an empty value
prints as `-`.

```
CONFIG ADD kind=<k> name=<n> rev=<id>
CONFIG SET kind=<k> name=<n> rev=<id> changed=<f,g>
CONFIG REMOVE kind=<k> name=<n> rev=<id>
<KIND> name=<n> <field>=<v> ...                          (list: one per row)
MACHINE name=<n> <field>=<v> ... os=<v> arch=<v> cores=<n> memory_gb=<n> beat=<t>   (list and show with a Redis: the live facts, - each when the beat lacks it)
MACHINE name=<n> <field>=<v> ... beat=none                (with a Redis: no beat)
CONFIG LIST kind=<k> rows=<n>
<KIND> name=<n> <field>=<v> ... created=<t> updated=<t>  (show)
HISTORY id=<id> kind=<k> name=<n> op=<op> actor=<a> at=<t> <field>=<before>><after> ...
CONFIG HISTORY kind=<k> name=<n> changes=<n>
CHECK ADD|SET|REMOVE kind=<k> name=<n> [changed=<f,g>]
CONFIG CHECK kind=<k> add=<n> set=<n> remove=<n> rev=<r> applied=<redis rev>
APPLY ADD|SET|REMOVE kind=<k> name=<n> [changed=<f,g>]
CONFIG APPLY kind=<k> add=<n> set=<n> remove=<n> rev=<r> ms=<n>
MIGRATION version=<v> file=<f> lines=<n>                 (migrate --print)
CONFIG MIGRATE print=<n> pg=-
CONFIG MIGRATE pg=<user@host:port/db> from=<v> to=<v> applied=<n>
CONFIG STATUS pg=<...> schema=<v> <kind>=<rows> <kind>_rev=<r> ... redis=<addr> <kind>_applied=<r> ...   (a singleton: <kind>_rev alone)
CONFIG KIND name=<k> table=config.<t> fields=<f,...> required=<f,...> rows=many|one
CONFIG KINDS count=<n>
```

Exit codes: 0 done; 1 refused (the store or Redis said no: a duplicate, a
missing row, a ref naming no row, a row another names, a ceiling, working
copies, `CONFLICT`, a status behind); 2 usage (a flag, a value, a name on a
singleton, a store that did not answer). A refusal is
one stderr line, `nova-config <verb>: <why>; run: <next step>`.

## Connecting

`--pg <dsn>` (env `NOVA_PG_DSN`) is `postgres://user@host:port/db` with **no
password on the line**: a `--pg` carrying one is refused, because a `ps`
reads the line. The password is read from the variable `NOVA_PG_PASSWORD_ENV`
names (`NOVA_PG_PASSWORD` when unset) and put into the connection in memory;
a named variable that is empty is refused with its name, the shape
`NOVA_SPRINT_REDIS_PASSWORD_ENV` keeps for Redis. A DSN with no password
anywhere connects with none (a throwaway database trusts).

`--redis <addr>` is the flag, else `NOVA_SPRINT_REDIS`, else
`NOVA_REDIS_ADDR`, else the selected seat's address; the Redis login is the
one `internal/nsprint/store.Open` makes. `machine
list` and `machine show` take the same flag for the live facts but stop at
the environment: with none named they print the declared fields alone and
open no store. `--as` is the flag, else `NOVA_FRIEND`, required on every
write.

## Deliberately not configuration

- runtime state: beats, presence, states, copies, leases, the sprint table;
- history other than the configuration's own: scores, receipts, ledgers,
  `cap:log`;
- secrets: the store holds the name of the variable, never a password;
- the sprint plan (`ns_sprint_plan`): a sprint is bounded work, not the
  fleet.
