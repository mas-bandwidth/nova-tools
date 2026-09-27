# nova-config: the permanent configuration

`nova-config` is the one tool for the fleet's permanent, non-ephemeral
configuration. It owns Postgres (schema `config`, its migrations, its
history) and every registry of the fleet, and it applies that configuration
into Redis so Redis is always a rebuildable copy. The contract is
[SPEC-CONFIG.md](../SPEC-CONFIG.md); this page is how to use it.

Glenn drew the boundary on 2026-09-26: "I don't think redis is an appropriate
place to store non-ephemeral data. It is good as a hot store of data that can
be rebuilt." So Postgres is the permanent store, Redis is a copy of it, and
the runtime tools (`nova-friend`, `nova-sprint`) read configuration and never
write it. History is not configuration: scores, receipts and ledgers stay
with the tools that write them.

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
CONFIG MIGRATE pg=nova_config@space:5432/nova from=0 to=3 applied=3
```

`migrate` creates or upgrades schema `config` from the numbered migrations in
the binary, each in its own transaction, each recorded in
`config.schema_migrations`, and applies nothing twice: run it again and it
prints `applied=0`. Run it as the `nova_config` role, which owns the schema;
the `nova_read` role, when it exists, is granted read on every table.

`status` is where things stand:

```
nova-config status
CONFIG STATUS pg=nova_config@space:5432/nova schema=3 machine=9 machine_rev=9 friend=4 friend_rev=13 redis=space:6380 machine_applied=9 friend_applied=13
```

It exits 1 with the next step on stderr when the schema is not there yet
(`run: nova-config migrate`) or when Redis is behind Postgres for any kind
(`run: nova-config apply`).

## The kinds

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
`set` changes the fields named and no other; `--note ""` clears one. `-h` on
any verb prints its flags with one help line each.

### machine

The fleet registry's row: what a machine is, and so what may be placed on it.

```
nova-config machine add studio --ssh studio --os_arch darwin/arm64 --slots 64 --cores 32 --roles bench,coordination,runner --seat studio --user glenn --note "Glenn's Mac Studio" --as rowan
CONFIG ADD kind=machine name=studio rev=1
```

`--ssh` is the host alias (or `user@host`) that reaches it; `--os_arch` is
one of darwin/amd64, darwin/arm64, linux/arm64, linux/x64; `--slots` is the
machine ceiling, the most desired slots its friends and benches may sum to
(`machine:<m>:ceiling`); `--cores` feeds the CI budget the ceiling derives
(0 writes none); `--roles` is a comma list of bench, coordination, ingress,
runner, services; `--seat` is the nova-secrets seat on the machine; `--user`
the account the bench runs as.

### friend

An AI friend: where it runs, how wide, how it is woken, its roles and the
logins that are it.

```
nova-config friend add rowan --machine studio --slots 64 --harness claude --wake unit:rowan@studio --roles coordinator,builder --logins rowan-claude --as rowan
CONFIG ADD kind=friend name=rowan rev=2
nova-config friend set rowan --slots 32 --note "half width tonight" --as rowan
CONFIG SET kind=friend name=rowan rev=3 changed=note,slots
nova-config friend list
FRIEND name=rowan machine=studio slots=32 harness=claude wake=unit:rowan@studio roles=builder,coordinator logins=rowan-claude note=half\x20width\x20tonight
CONFIG LIST kind=friend rows=1
nova-config friend history rowan
HISTORY id=2 kind=friend name=rowan op=add actor=rowan at=2026-09-27T02:10:00Z harness=claude logins=rowan-claude machine=studio note=- roles=builder,coordinator slots=64 wake=unit:rowan@studio
HISTORY id=3 kind=friend name=rowan op=set actor=rowan at=2026-09-27T02:11:00Z note=->half\x20width\x20tonight slots=64>32
CONFIG HISTORY kind=friend name=rowan changes=2
```

`--machine` must name a machine row (a friend's slots are guarded by its
machine's ceiling, so a friend on no machine is refused by the structure);
`--wake` is `unit:<label>@<host>`, `human:<channel>` or empty; `--roles` is
a comma list of builder, coordinator, may-hold, reader; `--logins` are the
GitHub logins that are this friend, unique across friends and never a
friend's name.

### Refusals

One stderr line each, naming the next step:

```
nova-config machine add: machine studio exists; run: nova-config machine set studio --<field> <value>
nova-config friend add: --machine hulk names no machine row; run: nova-config friend set rowan --<field> <value>
nova-config friend set: friend nobody not found; run: nova-config friend add nobody --<field> <value> ...
nova-config machine remove: machine studio is the --machine of friend rowan; run: nova-config machine list
nova-config friend add: --logins rowan-claude is friend rowan's login; run: ...
```

Exit 1 is the store saying no; exit 2 is an invocation that could not run
(a missing flag, a bad value, a store that did not answer), and its line
ends `run: nova-config help`.

## Apply: Redis as a copy

```
nova-config apply --check --as rowan
CHECK ADD kind=machine name=studio
CONFIG CHECK kind=machine add=1 set=0 remove=0 rev=1 applied=0
CHECK ADD kind=friend name=rowan
CONFIG CHECK kind=friend add=1 set=0 remove=0 rev=3 applied=0
nova-config apply --as rowan
APPLY ADD kind=machine name=studio
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=1 ms=4
APPLY ADD kind=friend name=rowan
CONFIG APPLY kind=friend add=1 set=0 remove=0 rev=3 ms=6
```

`apply` reads Postgres and writes Redis, one kind at a time, machines before
friends. For every row it writes exactly what the nova-sprint verbs used to
write by hand, through the same Redis Functions: `capacity friend`
(`ns_capacity_desired`, the desired hash under the machine ceiling), `friend
roles` (`ns_friend_roles`), `capacity friend --wake` (`ns_friend_wakepath`)
and a hello's `--login` (`friends:login`); for a machine, `capacity machine`
(`ns_capacity_machine`) and its registry hash `machine:<m>`. A name in Redis
that Postgres has not is removed. `--check` prints the plan and writes
nothing. `--kind friend` applies one kind.

Every apply is compare-and-set on a revision: `config:decl` in Redis holds
`rev:<kind>`, the Postgres revision last applied, and `apply` refuses
`CONFLICT` when Redis is ahead of the Postgres it read (a newer Postgres
applied it) and stamps the new revision after writing, only if the stamp has
not moved. A second apply of the same Postgres is a no-op: `add=0 set=0
remove=0`, the revision unchanged.

What Redis refuses, apply reports and stops at, stamping nothing:

- `CEILING studio: friend stella makes the sum 65 over the machine ceiling 64`:
  raise the machine's `--slots` or lower a friend's;
- `friend stella holds 2 working copies (card:4410,card:4414)`: a removed
  friend still holding work stays in Redis until the copies finish or move;
- `roles of rowan: --as stella does not hold the coordinator role in Redis`:
  roles are written by a coordinator (or by the first coordinator, when none
  is set yet). Apply as the coordinator; the coordinator's own row is applied
  first;
- `machine mini still carries friend:emma in Redis`: a removed machine keeps
  its keys while a desired hash names it.

**Lose Redis: run `nova-config apply`.** The function library is installed
when the store has none, every registry is written from Postgres, and the
stamps are set. Nothing about the fleet's configuration lives only in Redis.

## What is deliberately not here

Runtime state (beats, states, copies, leases, the table), history other than
the configuration's own (scores, receipts, ledgers, `cap:log`), secrets (the
store holds the name of a variable, never a password), and the sprint plan.
The kinds still to come, loop, runner, bench, route and setting, are listed
in [SPEC-CONFIG.md](../SPEC-CONFIG.md) as planned, not built.
