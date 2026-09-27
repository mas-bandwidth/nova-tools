# SPEC-CONFIG: the permanent configuration store

*Normative for `nova-config`. Where the code and this page disagree, one of
them has a bug and the tests decide which.*

## The boundary

Glenn, 2026-09-26: "I don't think redis is an appropriate place to store
non-ephemeral data. It is good as a hot store of data that can be rebuilt."
And: "a new nova-config tool that manages this permanent (non-ephemeral)
configuration. Thus it is kept cleanly separate from runtime, ephemeral
state verbs."

So there are two stores with two jobs:

- **Postgres is the permanent store.** Schema `config`, owned by the
  `nova_config` role, holds every registry of the fleet and the history of
  every change to it. `nova-config` is its one writer.
- **Redis is a copy.** `nova-config apply` writes the configuration into the
  keys the runtime tools read, through the runtime's own Redis Functions, and
  removes what Postgres no longer has. Lose Redis: run `nova-config apply`.
  The runtime tools (`nova-friend`, `nova-sprint`) read configuration from
  Redis and never write it.

**History is not configuration.** Scores, receipts, ledgers, beats, copies,
leases and every other thing a tool writes as it runs stay with the tool that
writes them. `config.history` is the history of the configuration itself,
nothing else.

## Kinds

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
`^[a-z0-9][a-z0-9-]*$`), the kind's fields as columns of the same names, a
`note text` column, `created_at` and `updated_at`. The CLI's flags, help,
refusals, SQL, typed lines and apply diff are all generated from the
descriptor, so every kind has identical verbs and a new kind adds no verb
code.

### Field types

| type | value | column |
| --- | --- | --- |
| `text` | one line of free text | `text` |
| `int` | a non-negative integer | `integer` |
| `enum` | one word of the field's list | `text` |
| `list` | a comma list of words from the field's list, deduplicated and sorted | `text` |
| `names` | a comma list of names (letters, digits, dashes), deduplicated and sorted | `text` |
| `ref` | the name of a row of another kind | `text` with a foreign key |
| `wake` | `unit:<label>@<host>`, `human:<channel>` or empty | `text` |

A value is canonicalised before it is stored (`Field.Canonical`), so a row
compares equal to its Redis view field by field. `add` refuses a row missing
a required field, a value outside its type, or a flag the kind has not, and
names every problem in one line. `set` changes the fields named and no
other. A `ref` field naming no row is refused (`--machine hulk names no
machine row`), and a row a `ref` field of another kind names cannot be
removed (`machine studio is the --machine of friend rowan`): the structure
enforces it (a foreign key), the tool names it.

### The kinds of this cut

**`machine`** (`config.machines`): the fleet registry's row, what a machine
is and therefore what may be placed on it.

| field | type | required | Redis |
| --- | --- | --- | --- |
| `ssh` | text | yes | `machine:<m>` |
| `os_arch` | enum: darwin/amd64, darwin/arm64, linux/arm64, linux/x64 | yes | `machine:<m>` |
| `slots` | int | yes | `machine:<m>:ceiling` slots (`ns_capacity_machine`) |
| `cores` | int | | `machine:<m>:ceiling` cores (0 writes none) |
| `roles` | list: bench, coordination, ingress, runner, services | | `machine:<m>` |
| `seat` | text | | `machine:<m>` |
| `user` | text | | `machine:<m>` |
| `note` | text | | `machine:<m>` |

**`friend`** (`config.friends`): an AI friend, where it runs, how wide, how
it is woken, its roles and the logins that are it.

| field | type | required | Redis |
| --- | --- | --- | --- |
| `machine` | ref machine | yes | `friend:<f>:desired` machine (`ns_capacity_desired`) |
| `slots` | int | yes | `friend:<f>:desired` slots (`ns_capacity_desired`) |
| `harness` | text | | `friend:<f>:config` |
| `wake` | wake | | `friend:<f>:wakepath` (`ns_friend_wakepath`) |
| `roles` | list: builder, coordinator, may-hold, reader | | `friend:<f>:roles` (`ns_friend_roles`) |
| `logins` | names | | `friends:login` alias -> friend |
| `note` | text | | `friend:<f>:config` |

A friend's logins are unique across friends and never a friend's name
(presence.lua's LOGIN-TAKEN and LOGIN-IS-FRIEND, refused in Postgres before
Redis sees them).

### Planned kinds, not built

Glenn, 2026-09-27: "I would very much like for us to move ALL of this
configuration data into postgres with nova-config." Each is a later pull
request: one descriptor, one migration, one Redis writer, no new verb code.

| kind | what it holds today | fields |
| --- | --- | --- |
| `loop` | the supervised loops table | name, where (coordinator or every-bench), argv, user, description |
| `runner` | the CI runners | host, count, labels, user |
| `bench` | per-bench facts now in ansible host_vars | user, seat, roles, os, limits |
| `route` | model routes and provider lists per tier and per bench | tier, bench, providers |
| `setting` | the loose scalars now in group_vars: store address, holds, versions | name, value, note |

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
config.machines          (name PK, ssh, os_arch, slots, cores, roles, seat,
                          "user", note, created_at, updated_at)
config.friends           (name PK, machine -> machines.name, slots, harness,
                          wake, roles, logins, note, created_at, updated_at)
```

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

`nova-config apply [--kind <k>] [--check]` runs per kind, in kind order
(machines before friends, because a friend's desired slots are guarded by
its machine's ceiling):

1. read the kind's rows and revision from Postgres;
2. read the kind's rows from Redis (`Applier.Read`, plain commands, no
   write) and Redis's stamp `config:decl rev:<kind>`;
3. **refuse `CONFLICT`** when the stamp is greater than the Postgres
   revision: a newer Postgres applied it, and this process would roll it
   back. Nothing is written;
4. **plan** the difference: `ADD` for a row Redis lacks, `SET` for one that
   differs in any field (naming the fields), `REMOVE` for a name in Redis that
   Postgres has not. Adds and sets come in the kind's apply order (friends:
   the coordinator first, so `ns_friend_roles` has one to bootstrap), then
   removes by name;
5. with `--check`, print the plan as `CHECK` lines and stop; nothing is
   written, not even the function library;
6. install the `nova_sprint` function library only when the store has none
   (`fn.LoadMissing`; a deployed one is never replaced);
7. write every op in order through the runtime's own functions, each write
   carrying `--as` and the idempotency marker `config:<kind>:<rev>` into
   `cap:log`. A refusal from a function (`CEILING`, an actor without the
   coordinator role, a friend with working copies) stops the apply there, is
   printed with its remedy, and the stamp is not moved;
8. **stamp** `config:decl rev:<kind> = <rev>, at:<kind> = <server ms>`,
   compare-and-set: inside `WATCH config:decl`, the stamp is written only
   while it still reads the value step 2 read; a stamp that moved is
   `CONFLICT`.

A second apply of the same Postgres is a no-op: no `APPLY` line, the counts
zero, the stamp rewritten to the same revision.

### What apply writes, per kind

**machine:** `ns_capacity_machine(m, slots, cores)` for the ceiling (refused
`CEILING` when the friends and benches on it already desire more than
`slots`); the hash `machine:<m>` with ssh, os_arch, roles, seat, user, note,
rev, at; the set `machines`. Remove: refused while any friend or bench
desired hash names the machine; else `machine:<m>`, `machine:<m>:ceiling` and
`machine:<m>:budget` are deleted and the name leaves `machines`.

**friend:** `ns_capacity_desired(friend, f, slots, machine)` (registers in
`friends`, writes `friend:<f>:desired`, refuses `CEILING` and `NOCEILING`);
`ns_friend_roles(f, roles)` when the roles differ (the actor must hold the
coordinator role in Redis, or nobody does yet and this row makes the first);
`ns_friend_wakepath(f, ...)` when the wake path differs (`clear` when the row
has none); `friends:login` aliases added and stale ones removed; the hash
`friend:<f>:config` with harness, note, rev, at. Remove: refused while
`friend:<f>:cards:working` has a member, naming the copies; else the
registry member, the desired, roles, wakepath and config hashes and the
friend's aliases are removed in one transaction, with a `config-remove`
receipt in `cap:log`.

`machine:<m>`, `machines` and `friend:<f>:config` are nova-config's own keys:
no function in the library wrote a machine's registry row or a friend's
harness before this tool.

## Lines

One typed line per event; values go through `oneline.Field`, an empty value
prints as `-`.

```
CONFIG ADD kind=<k> name=<n> rev=<id>
CONFIG SET kind=<k> name=<n> rev=<id> changed=<f,g>
CONFIG REMOVE kind=<k> name=<n> rev=<id>
<KIND> name=<n> <field>=<v> ...                          (list: one per row)
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
CONFIG STATUS pg=<...> schema=<v> <kind>=<rows> <kind>_rev=<r> ... redis=<addr> <kind>_applied=<r> ...
CONFIG KIND name=<k> table=config.<t> fields=<f,...> required=<f,...>
CONFIG KINDS count=<n>
```

Exit codes: 0 done; 1 refused (the store or Redis said no: a duplicate, a
missing row, a login taken, a ceiling, working copies, `CONFLICT`, a status
behind); 2 usage (a flag, a value, a store that did not answer). A refusal is
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
one every nova-sprint verb uses (`internal/nsprint/store.Open`). `--as` is
the flag, else `NOVA_FRIEND`, required on every write.

## Deliberately not configuration

- runtime state: beats, presence, states, copies, leases, the sprint table;
- history other than the configuration's own: scores, receipts, ledgers,
  `cap:log`;
- secrets: the store holds the name of the variable, never a password;
- the sprint plan (`ns_sprint_plan`): a sprint is bounded work, not the
  fleet.
