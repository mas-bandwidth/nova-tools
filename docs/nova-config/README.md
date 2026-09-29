# nova-config: the permanent configuration

`nova-config` is the one tool for the fleet's permanent, non-ephemeral
configuration. It owns Postgres (schema `config`, its migrations, its
history) and every registry of the fleet, and it applies that configuration
into Redis so Redis is always a rebuildable copy. The contract is
[SPEC-CONFIG.md](../SPEC-CONFIG.md); this page is how to use it.

Redis is a hot store of data that can be rebuilt, not a place for
non-ephemeral data. Postgres is the permanent store, Redis is a copy of it, and
runtime tools read configuration and never write it. History is not
configuration: scores, receipts and ledgers stay with the tools that write
them.

## Connecting

Three things, each a flag or a variable, none a password on a line:

| flag | variable | what |
| --- | --- | --- |
| `--pg <dsn>` | `NOVA_PG_DSN` | `postgres://nova_config@space:5432/nova`, no password in it |
| | `NOVA_PG_PASSWORD_ENV` | the NAME of the variable holding the password (`NOVA_PG_PASSWORD` when unset) |
| `--redis <addr>` | `NOVA_SPRINT_REDIS`, then `NOVA_REDIS_ADDR`, then the seat | the store apply writes |
| `--as <friend>` | `NOVA_FRIEND` | who is making the change; every write records it |

The password is sealed the way the Redis one is: `nova-secrets exec --only
NOVA_PG_PASSWORD -- nova-config ...` leaves it in the environment, where a
`ps` cannot read it, and the tool puts it into the connection in memory. A
`--pg` that carries a password is refused. A variable named by
`NOVA_PG_PASSWORD_ENV` that is empty is refused with its name. A DSN with no
password anywhere connects with none, which is what a throwaway database
wants.

## First run

Nothing below needs a store:

```
nova-config kinds
nova-config migrate --print
```

`kinds` prints one line per kind with its table, its fields and the fields
`add` requires; `migrate --print` lists the migrations this binary carries.
The executable transcript is in [TESTS.md](../TESTS.md#nova-config).

## The schema

```
nova-config migrate --pg postgres://nova_config@space:5432/nova
CONFIG MIGRATE pg=nova_config@space:5432/nova from=0 to=5 applied=5
```

`migrate` creates or upgrades schema `config` from the numbered migrations in
the binary, each in its own transaction, each recorded in
`config.schema_migrations`, and applies nothing twice: run it again and it
prints `applied=0`. Run it as the `nova_config` role, which owns the schema;
the `nova_read` role, when it exists, is granted read on every table.

`status` is where things stand:

```
nova-config status
CONFIG STATUS pg=nova_config@space:5432/nova schema=5 machine=9 machine_rev=9 friend=4 friend_rev=13 redis=space:6380 machine_applied=9 friend_applied=13
```

It exits 1 with the next step on stderr when the schema is not there yet
(`run: nova-config migrate`) or when Redis is behind Postgres for any kind
(`run: nova-config apply`).

## The kinds

The placement rule: per-machine facts belong to machines, and global fleet
facts belong to the fleet. So a machine's row holds what varies per machine, the
fleet's one row holds what has one value for the whole fleet, a friend's row
holds what someone decides for her, and the sprint's one row holds who
coordinates. Anything else is invented and is not a field.

Every kind has the same six verbs, generated from its descriptor, so what is
true of one is true of all:

```
nova-config <kind> add <name> --<field> <value> ... --as <friend>
nova-config <kind> set <name> --<field> <value> ... --as <friend>
nova-config <kind> remove <name> --as <friend>
nova-config <kind> list
nova-config <kind> show <name>
nova-config <kind> history <name>
nova-config <kind> <verb> -h
```

A name is lower-case letters, digits and dashes. `add` needs every required
field and refuses a value outside its type, naming every problem in one line.
`set` changes the fields named and no other; `--roles ""` clears a list.
`-h` on any verb prints its flags with one help line each. A singleton kind
(the fleet, the sprint) is one row `migrate` creates: `set`, `show` and
`history` take no name, and there is no `add`, `remove` or `list`.

### machine

A machine of the fleet, named by its tailnet host: `ssh <name>` reaches it
("All fleet machines *must* be on the tailnet. This is a hard requirement."),
so there is no address field. The row is exactly the four declared facts
something reads, "not invented rando stuff".

```
nova-config machine add hulk --user gaffer --seat swarm-hulk --slots 40 --runners 0 --as rowan
CONFIG ADD kind=machine name=hulk rev=1
nova-config machine add studio --user glenn --seat studio --slots 64 --runners 1 --as rowan
CONFIG ADD kind=machine name=studio rev=2
nova-config machine list
MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0
MACHINE name=studio user=glenn seat=studio slots=64 runners=1
CONFIG LIST kind=machine rows=2
```

`--user` is the login the plays and the seals use on it; `--seat` its
nova-secrets seat; `--slots` how many cards it may run at once, the machine
ceiling (`machine:<m>:ceiling`; 0 runs none); `--runners` how many CI
runners it hosts (0, the default, hosts none).

Measured facts (os, arch, cores, memory) are never typed: "I like measured
facts coming live ... It's more robust." With a Redis named (`--redis`, or
`NOVA_SPRINT_REDIS`, `NOVA_REDIS_ADDR`), `list` and `show` end each line in
what the machine's own beat (`bench:<name>:beat`) says, `-` for a fact the
beat does not carry yet and `beat=none` for a machine that has never beaten:

```
nova-config machine list --redis space:6380
MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0 os=- arch=- cores=64 memory_gb=- beat=2026-09-27T03:00:00Z
MACHINE name=studio user=glenn seat=studio slots=64 runners=1 beat=none
CONFIG LIST kind=machine rows=2
```

### fleet

The one row of fleet-wide facts: which machine is the store (Redis and
Postgres) and which the coordinator ("the studio is the coordinator.
coordinator can be driven by rowan (you) or stella."). Both name machine
rows; a machine the fleet names cannot be removed.

```
nova-config fleet set --store hulk --coordinator studio --as rowan
CONFIG SET kind=fleet name=fleet rev=3 changed=coordinator,store
nova-config fleet show
FLEET name=fleet store=hulk coordinator=studio created=2026-09-27T02:00:00Z updated=2026-09-27T02:10:00Z
```

### friend

What someone decides for a friend: how wide she runs, which tiers she can
do, her roles. "Anything that a friend would just know, is runtime redis
data": where she runs, her harness, her logins and her wake path are her own
presence's, never here. Who coordinates is the sprint row's.

```
nova-config friend add rowan --slots 64 --tiers frontier,pro --roles builder --as rowan
CONFIG ADD kind=friend name=rowan rev=4
nova-config friend set rowan --slots 32 --roles builder,reader --as rowan
CONFIG SET kind=friend name=rowan rev=5 changed=roles,slots
nova-config friend list
FRIEND name=rowan slots=32 tiers=frontier,pro roles=builder,reader
CONFIG LIST kind=friend rows=1
nova-config friend history rowan
HISTORY id=4 kind=friend name=rowan op=add actor=rowan at=2026-09-27T02:10:00Z roles=builder slots=64 tiers=frontier,pro
HISTORY id=5 kind=friend name=rowan op=set actor=rowan at=2026-09-27T02:11:00Z roles=builder>builder,reader slots=64>32
CONFIG HISTORY kind=friend name=rowan changes=2
```

`--slots` is her desired slots; the friends' slots on a machine fit under
its ceiling together, and are charged to it only while each friend is awake
(the bench's share on that machine is the remainder, live); she is charged
to the machine her beat reports (or the fleet's coordinator machine when
she has no beat);
`--tiers` is a comma list of flash, frontier, pro, which she can do (the
deal's tier filter); `--roles` is a comma list of builder, may-hold, reader.

### sprint

The one row of sprint-global facts: who holds the coordinator role. Setting
it is the handover; a friend the sprint names cannot be removed.

```
nova-config sprint set --coordinator rowan --as rowan
CONFIG SET kind=sprint name=sprint rev=6 changed=coordinator
nova-config sprint show
SPRINT name=sprint coordinator=rowan created=2026-09-27T02:00:00Z updated=2026-09-27T02:12:00Z
```

### Refusals

One stderr line each, naming the next step:

```
nova-config machine add: machine studio exists; run: nova-config machine set studio --<field> <value>
nova-config fleet set: --store space names no machine row; run: nova-config fleet show
nova-config friend set: friend nobody not found; run: nova-config friend add nobody --<field> <value> ...
nova-config machine remove: machine studio is the --coordinator of the fleet; run: nova-config machine list
nova-config friend remove: friend rowan is the --coordinator of the sprint; run: nova-config friend list
nova-config fleet set: fleet takes no name: it is one row; want fleet set --<field> <value> ...; run: nova-config help
```

Exit 1 is the store saying no; exit 2 is an invocation that could not run
(a missing flag, a bad value, a store that did not answer), and its line
ends `run: nova-config help`.

## Apply: Redis as a copy

```
nova-config apply --check
CHECK ADD kind=machine name=hulk
CHECK ADD kind=machine name=studio
CONFIG CHECK kind=machine add=2 set=0 remove=0 rev=2 applied=0
CHECK SET kind=fleet name=fleet changed=store,coordinator
CONFIG CHECK kind=fleet add=0 set=1 remove=0 rev=3 applied=0
CHECK ADD kind=friend name=rowan
CONFIG CHECK kind=friend add=1 set=0 remove=0 rev=5 applied=0
CHECK SET kind=sprint name=sprint changed=coordinator
CONFIG CHECK kind=sprint add=0 set=1 remove=0 rev=6 applied=0
nova-config apply --as rowan
APPLY ADD kind=machine name=hulk
APPLY ADD kind=machine name=studio
CONFIG APPLY kind=machine add=2 set=0 remove=0 rev=2 ms=4
APPLY SET kind=fleet name=fleet changed=store,coordinator
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=3 ms=1
APPLY ADD kind=friend name=rowan
CONFIG APPLY kind=friend add=1 set=0 remove=0 rev=5 ms=6
APPLY SET kind=sprint name=sprint changed=coordinator
CONFIG APPLY kind=sprint add=0 set=1 remove=0 rev=6 ms=1
```

`apply` reads Postgres and writes Redis, one kind at a time: machines, the
fleet row, friends, the sprint row. For a machine it writes its machine ceiling
(`ns_capacity_machine`, the ceiling from `--slots`; cores and memory are never
declared, so none are passed) and its registry hash `machine:<m>`. For the
fleet row, `fleet:store` and `fleet:coordinator`, plain keys. For a friend it
writes her desired capacity and roles (`ns_capacity_desired`, `ns_friend_roles`),
charging her slots to the machine her own beat reports, else to the fleet's
coordinator machine; the friend the sprint row names gets the `coordinator`
role in Redis on top of her row's roles, so a handover (`sprint set
--coordinator stella`, then `apply`) is two `SET ... changed=roles`, hers
first. It never touches her logins or wake path: they are her presence's.
For the sprint row, `sprint:coordinator`. A name in Redis that Postgres has
not is removed. `--check` prints the plan and writes nothing. `--kind friend`
applies one kind.

Every apply is compare-and-set on a revision: `config:decl` in Redis holds
`rev:<kind>`, the Postgres revision last applied, and `apply` refuses
`CONFLICT` when Redis is ahead of the Postgres it read (a newer Postgres
applied it) and stamps the new revision after writing, only if the stamp has
not moved. A second apply of the same Postgres is a no-op: `add=0 set=0
remove=0`, the revision unchanged.

What Redis refuses, apply reports and stops at, stamping nothing:

- `CEILING studio: friend stella makes the sum 65 over the machine ceiling 64`:
  raise the machine's `--slots` or lower a friend's;
- `roles of rowan: --as stella does not hold the coordinator role in Redis`:
  roles are written by a coordinator (or by the first coordinator, when none
  is set yet). Apply as the coordinator; the coordinator's own row is applied
  first;
- `friend emma has no beat naming a machine and the fleet names no
  coordinator machine to charge her slots to`: run `nova-config fleet set
  --coordinator <machine>`, then apply;
- `machine mini still carries friend:emma in Redis`: a removed machine keeps
  its keys while a desired hash names it.

**Lose Redis: run `nova-config apply`.** The function library is installed
when the store has none, every registry is written from Postgres, and the
stamps are set. Nothing about the fleet's configuration lives only in Redis.

## Ansible inventory

```
export NOVA_PG_DSN=postgres://nova_config@127.0.0.1:5432/nova
nova-config inventory
```

`inventory` reads Postgres and prints an Ansible dynamic JSON inventory: the
groups `all` and `benches` (every machine row), `coordinator` and `store`
(the machines the fleet row names; empty when it names none) and `runners`
(every machine with at least one runner), and every host's variables under
`_meta.hostvars`. On a store that is not migrated, or is at an older schema,
the verb exits 1 with `run: nova-config migrate`. The machine rows are the one machine list;
there is no second one. Each host's variables are `ansible_host`,
`ansible_user` (the row's user, the name ansible reads for the login),
`nova_seat` (the row's seat), `slots`, `runners` and `kind=machine`; each value
has one name, and a user or seat that is empty is left out. A deployment maps
`nova_seat` to its own variable name in its `group_vars`. The machine rows and
the fleet row are read in one transaction.

The machine the command runs on is named by the env `NOVA_MACHINE`, matched by
exact machine name; when no machine row has that name the verb exits 1 with
the known names. When `NOVA_MACHINE` is unset, the first label of the hostname
is matched the same way, and nothing is marked local when no row has it. The
matched row gets `ansible_connection=local`, so ansible reaches it without
ssh.

`--list` prints all of it and is the default when no flag is given. `--host
<name>` prints one machine's variables; a name with no machine row exits 1 with
the known names, and `--list` with `--host` is refused.
`--timeout` (a Go duration, default `10s`) bounds the wait for the store, so an
unattended ansible run never blocks on a locked table: on expiry, at the connection,
the schema check or the read, the verb exits 2 with `timed out after <d>
waiting for the store while <stage>`, what to check, and the command to repeat
with a longer timeout; a connection the store refuses outright keeps the
generic refusal.

Ansible's `-i` wants an executable file whose first line, `#!/bin/sh`, is at
column one. These two commands write the two-line wrapper and make it
executable, and the third lets ansible read the inventory:

```
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > nova-inventory
chmod +x nova-inventory
ansible-inventory -i ./nova-inventory --list
```

Ansible starts the script with `--list`. Every host's variables are in the
`_meta.hostvars` of that output, so ansible does not call `--host <name>`. The
script reads `NOVA_PG_DSN` and `NOVA_PG_PASSWORD_ENV` from the environment
ansible passes it, and `NOVA_MACHINE`.

## What is deliberately not here

Runtime state (beats, states, copies, leases, the table), what a friend
would just know (her machine, harness, logins, wake path: her presence's),
measured facts (a machine's os, arch, cores, memory: its beat's), history
other than the configuration's own (scores, receipts, ledgers, `cap:log`),
secrets (the store holds the name of a variable, never a password), and the
sprint plan. What is still to come (more fleet and sprint fields, loops,
routes, and the wake path "later, when we know what we are doing") is listed
in [SPEC-CONFIG.md](../SPEC-CONFIG.md) as planned, not built.
