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

## Command surface

| command | inputs and behavior |
| --- | --- |
| `help [<verb> ...]`, `<verb> -h` | print help without opening a store |
| `version`, `--version` | print the shared version line; no arguments |
| `kinds` | print each descriptor and `CONFIG KINDS`; no store or arguments |
| `migrate [--pg <dsn>] [--print]` | apply schema migrations; `--print` lists embedded migrations without connecting |
| `status [--pg <dsn>] [--redis <addr>]` | report schema, row counts, revisions and, when Redis resolves, applied revisions |
| `apply [--pg <dsn>] [--redis <addr>] [--as <friend>] [--kind <kind>] [--check]` | plan or apply the Postgres-to-Redis differences; `--check` requires no actor |
| `inventory [--pg <dsn>] [--list \| --host <name>] [--timeout <duration>]` | print Ansible JSON; see Inventory |
| `machine self [--check] [--pg <dsn>]` | resolve this machine's name; optionally check its row |
| `machine width <name> [--pg <dsn>] [--redis <addr>] [--json]` | derive the machine's static sprint share |

The row verbs below also take `--pg`; row writes take `--as`. Only machine
`list` and `show` add `--redis` for live facts. Singleton row verbs take no name.
`status` prints `redis=-` when no Redis address resolves. It exits 1 for an
absent schema or any applied-revision mismatch; an address that resolves but
cannot be read is an error, not a request to omit the Redis check.

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

The declared fields are:

| side | fields |
| --- | --- |
| machine (varies per machine) | `user`, `seat`, `slots`, `runners` |
| fleet (one value for the whole fleet) | `store`, `coordinator` (both machines) |
| friend (decided for her) | `slots`, `tiers`, `roles` |
| sprint (one value for the whole sprint) | `coordinator` (a friend) |

A kind is one registry: one table under schema `config`, one Go descriptor
(`internal/config/kind.go`: `Kind`), one migration, one Redis writer. The
row grammar is shared by the non-singleton kinds (`machine` and `friend`):

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
kind shares the same row operations. Machine discovery and width are
additional read-only queries.

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

### Registered kinds

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

**The sprint's width.** One ceiling per machine, shared by the friends and the
sprint: the machine's `slots` is the ceiling, each friend's `slots` is charged
to the machine her own beat reports (else the fleet row's coordinator machine,
the charge apply makes), and what remains is the sprint member's width:
`width = max(0, slots - the friend slots charged to the machine)`. A machine with a
width of 1 or more is a member of the sprint's fleet; a machine with `slots` 0,
or whose friends take the whole ceiling, is not. No field holds it: the width
is derived on every read, so the inventory is the one place a machine's
capacity is written. It is a static share for the same rows and friend-to-machine
attribution: the CI legs running on the machine and every other child hold slots of the ceiling moment
by moment, and they are taken off at the take, by a lease from the machine's
one slot store, never in the width. `nova-config machine width <name>` prints it, reading the friends' beats from a
Redis when a friend row carries slots (`Widths`, `internal/config/width.go`);
with no friend beats on the store, every friend is charged to the coordinator
machine. `--json` returns `machine`, `slots`, `charged`, `width` and `member`
as one JSON object; otherwise the verb prints `CONFIG WIDTH`. `--pg` supplies
the rows. The Redis address comes from `--redis`, `NOVA_SPRINT_REDIS`, then
`NOVA_REDIS_ADDR`, without a seat fallback. When no friend has positive slots,
Redis may be omitted. An explicitly supplied Redis is still opened.

**A machine's own name.** `nova-config machine self` prints the name this
machine has in the inventory, so no name is typed on the machine it names:
`NOVA_MACHINE` when set, else the first label of the host's DNS name on the
tailnet when a tailnet is running (asked of `tailscale status --json`, only
when the program is installed, with a five-second timeout), else the first
label of the hostname, always lower-case. An unavailable, stopped or invalid
tailnet response falls back to the hostname. An invalid nonempty `NOVA_MACHINE`
is refused rather than replaced by a fallback. Without `--check`, it opens no store. `--check` reads the machine rows and
exits 2
when the name is none of them, 3 when the name or the rows cannot be read
(`SelfName`, `internal/config/self.go`). Usage errors also exit 2. Success
prints only the resolved name and a newline; a failed check prints no name.

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

## History

**Every row write is also a history row, in the same transaction.** `add`
records `before = null, after = the row`; `set` records the row before and
after; `remove` records `before = the row, after = null`. `actor` is `--as`
(or `NOVA_FRIEND`), required on each row-verb write. No `add`, `set` or `remove`
changes a row without a history record, and no path edits history. Schema
migrations create the initial `fleet` and `sprint` singleton rows without a
history record.

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
   write) and Redis's stamp `config:decl rev:<kind>`. Every batched read is
   checked: an absent value never hides another command's error. A failed
   read stops before its results are used or cached;
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
zero, the revision unchanged.

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
the friend holds working copies, naming them; else the
registry member, the desired and roles hashes are removed in one
transaction, with a `config-remove` receipt in `cap:log`; her beat, logins
and wake path stay, they are hers.

**sprint:** a plain `SET sprint:coordinator <friend>`, `DEL` when empty.
Never removed. The handover is `nova-config sprint set --coordinator
stella --as rowan` then `apply`: the sprint kind's own revision moves and
the friend kind's plan is two `SET ... changed=roles`, stella's first.

`machine:<m>`, `machines`, `fleet:*` and `sprint:coordinator` are
nova-config's own keys: no function in the library reads or writes them.

## Inventory

`inventory` reads machine and fleet rows in one read-only, repeatable-read
transaction. It prints Ansible JSON with `_meta.hostvars` and the groups `all`,
`benches`, `coordinator`, `store`, `runners`. `all` and `benches` contain every
machine; the fleet row selects coordinator/store; positive `runners` selects
the runners group. Host variables are `ansible_host`, `slots`, `runners`,
`kind=machine`, and nonempty `ansible_user`/`nova_seat` values.

`--list` is the default. `--host <name>` prints one host's variables; an empty
name or combining it with `--list` is usage error. An unknown host exits 1,
naming up to 20 known machines and the number remaining. An absent/older
schema exits 1 with a migrate remedy; a newer schema exits 1 requiring a
binary with matching migrations.

For the local host, nonempty `NOVA_MACHINE` matches exactly; an unknown
explicit name exits 1. Otherwise the lower-case first hostname label matches,
and no match simply leaves every host remote. The matched host gains
`ansible_connection=local`. This inventory rule does not consult the tailnet
or lowercase an explicit override, unlike `machine self`.

`--timeout` is a positive Go duration, default `10s`, covering connection,
schema inspection and row reading. Expiry exits 2 with the stage and a retry
command. No Redis connection or actor is required. When Ansible invokes an
inventory wrapper, `ANSIBLE_INVENTORY_UNPARSED_FAILED=true` makes script
failure fail the Ansible invocation instead of yielding an empty inventory.

## Lines

The row, status, apply, migrate and width verbs print typed lines; values go
through `oneline.Field`, and an empty value prints as `-`. `inventory` prints
JSON, while successful `machine self` prints only the resolved name.

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
CONFIG WIDTH machine=<n> width=<n> slots=<n> charged=<n> member=true|false
```

Exit codes: 0 done; 1 refused (the store or Redis said no: a duplicate, a
missing row, a ref naming no row, a row another names, a ceiling, working
copies, `CONFLICT`, a status behind); 2 usage (a flag, a value, a name on a
singleton, a store that did not answer). A refusal is
one stderr line, `nova-config <verb>: <why>; run: <next step>`.
`machine self` has the explicit exception above: 0 printed, 2 usage or no
matching row, 3 name/config unreadable. Inventory and width retain the general
0/1/2 mapping.

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
open no Redis connection. `--as` is the flag, else `NOVA_FRIEND`, required
on row writes and non-check apply. Schema migration takes no actor.

## Deliberately not configuration

- runtime state: beats, presence, states, copies, leases, the sprint table;
- history other than the configuration's own: scores, receipts, ledgers,
  `cap:log`;
- secrets: the store holds the name of the variable, never a password;
- the sprint plan: a sprint is bounded work, not the fleet.
