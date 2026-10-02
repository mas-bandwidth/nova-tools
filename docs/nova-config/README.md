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
CONFIG MIGRATE pg=nova_config@space:5432/nova from=0 to=7 applied=7
```

`migrate` creates or upgrades schema `config` from the numbered migrations in
the binary, each in its own transaction, each recorded in
`config.schema_migrations`, and applies nothing twice: run it again and it
prints `applied=0`. Run it as the `nova_config` role, which owns the schema;
the `nova_read` role, when it exists, is granted read on every table.

`status` is where things stand:

```
nova-config status
CONFIG STATUS pg=nova_config@space:5432/nova schema=7 machine=9 machine_rev=9 friend=4 friend_rev=13 redis=space:6380 machine_applied=9 friend_applied=13
```

It exits 1 with the next step on stderr when the schema is not there yet
(`run: nova-config migrate`) or when Redis is behind Postgres for any kind
(`run: nova-config apply`).

## The kinds

The placement rule: per-machine facts belong to machines, and global fleet
facts belong to the fleet. So a machine's row holds what varies per machine, the
fleet's one row holds what has one value for the whole fleet, a friend's row
holds what someone decides for her, the sprint's one row holds who
coordinates, a loop's row holds a process someone decides runs on one
machine, and a route's row holds one way someone decides to run a model tier.
Anything else is invented and is not a field.

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
so there is no address field. The row is exactly the five declared facts
something reads, "not invented rando stuff".

```
nova-config machine add hulk --user gaffer --seat swarm-hulk --slots 40 --runners 0 --width 32 --as rowan
CONFIG ADD kind=machine name=hulk rev=1
nova-config machine add studio --user glenn --seat studio --slots 64 --runners 1 --width 16 --as rowan
CONFIG ADD kind=machine name=studio rev=2
nova-config machine list
MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0 width=32
MACHINE name=studio user=glenn seat=studio slots=64 runners=1 width=16
CONFIG LIST kind=machine rows=2
```

`--user` is the login the plays and the seals use on it; `--seat` its
nova-secrets seat; `--slots` the machine ceiling apply writes
(`machine:<m>:ceiling`), which the friends' desired slots must fit under, and
not the sprint's width; `--runners` how many CI runners it hosts (0, the
default, hosts none); `--width` the most work cards the sprint's member on it
runs at once (0, the default, is no member).

Measured facts (os, arch, cores, memory) are never typed: "I like measured
facts coming live ... It's more robust." With a Redis named (`--redis`, or
`NOVA_SPRINT_REDIS`, `NOVA_REDIS_ADDR`), `list` and `show` end each line in
what the machine's own beat (`bench:<name>:beat`) says, `-` for a fact the
beat does not carry yet and `beat=none` for a machine that has never beaten:

```
nova-config machine list --redis space:6380
MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0 width=32 os=- arch=- cores=64 memory_gb=- beat=2026-09-27T03:00:00Z
MACHINE name=studio user=glenn seat=studio slots=64 runners=1 width=16 beat=none
CONFIG LIST kind=machine rows=2
```

**Width and its own name.** A machine's `width` is the sprint member's width
on it, set directly (`machine set <m> --width <n>`) and used as set:
`machine width` prints it, and `nova-sprint fleet sync` moves it to the fleet
table. Nothing else takes part (not its `slots`, not any friend row, no
Redis). A machine with a width of 1 or more is a member of the sprint's fleet;
width 0 is not:

```
nova-config machine width m1
CONFIG WIDTH machine=m1 width=32 member=true
nova-config machine width m1 --json
{"machine":"m1","width":32,"member":true}
```

**Migrating to a set width.** Before migration 0012 the width was derived: a
machine's `slots` less the `slots` of the friends charged to it, a friend
charged to the machine her live beat named, else to the fleet row's
coordinator machine. 0012 adds `width` and fills it with that rule's
beat-free part, since a migration reads no beats: every friend's `slots` are
charged to the coordinator machine (never below 0), every other machine gets
its `slots`. The fill is the old width exactly when no friend with slots had
a beat naming another machine. To see any machine where it is not, compare
with the fleet table the last sync wrote under the old rule, before syncing
again: `nova-sprint fleet sync --check` prints each width that differs and
writes nothing; set each one back with `nova-config machine set <m> --width
<n> --as <name>`.

`machine self` prints this machine's own name, so a process learns it and types
none: `NOVA_MACHINE`, else the tailnet's name for the host when a tailnet is
running, else the first label of the hostname, lower-case. It opens no store.
`--check` reads the machine rows and exits 2 when the name is none of them, 3
when the name or the rows cannot be read:

```
nova-config machine self
m1
nova-config machine self --check --pg postgres://nova_config@127.0.0.1:5432/nova
m1
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
its ceiling together (apply refuses `CEILING` otherwise); she is charged
to the machine her beat reports (or the fleet's coordinator machine when
she has no beat); her slots take nothing off any machine's `width`;
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

### loop

A supervised process on one machine: the command, the seat it opens its
secrets from and the names of those secrets, and how it runs. The command is
a JSON array, the program first, so a word may hold a blank and nothing is
split by a shell; a secret is never in it, it goes by name in `--keys`:

```
nova-config loop add member-m1 --machine m1 --argv '["/opt/bin/nova-swarm","member","--width","2"]' --keepalive true --seat s-m1 --keys A_KEY,B_KEY --width 2 --as a1
CONFIG ADD kind=loop name=member-m1 rev=12
nova-config loop add refresh-m1 --machine m1 --argv '["/opt/bin/refresh","--once"]' --every 60 --as a1
CONFIG ADD kind=loop name=refresh-m1 rev=13
nova-config loop list
LOOP name=member-m1 machine=m1 argv=["/opt/bin/nova-swarm","member","--width","2"] seat=s-m1 keys=A_KEY,B_KEY every=0 keepalive=true width=2 enabled=true
LOOP name=refresh-m1 machine=m1 argv=["/opt/bin/refresh","--once"] seat=- keys=- every=60 keepalive=false width=0 enabled=true
CONFIG LIST kind=loop rows=2
nova-config machine show m1
MACHINE name=m1 user=u1 seat=s-m1 slots=4 runners=0 created=2026-09-30T02:00:00Z updated=2026-09-30T02:00:00Z loops=member-m1,refresh-m1
```

`--every <seconds>` runs it periodically and `--keepalive true` keeps a
long-running one up; a loop has exactly one of the two. `--width` is a member
loop's child cap (0 for any other). `--enabled false` writes the unit and
does not start it. `--keys` needs a `--seat`. Its log is
`~/nova-bench/loops/<name>.log`, derived from the name and never typed. A
machine a loop names cannot be removed until the loop is.

### route

One way to run a model tier: the provider and model a card of that tier runs
on, its token budget per card and its deadline in seconds. A tier with several
routes spreads its cards across providers and models, in the order of the
tier's array. Three pro routes, the pro array (the direct route named twice, so
it takes two turns of every four), then the apply that hands them to the deal:

```
nova-config route add pro-deepseek-opencode --tier pro --provider opencode --model deepseek-v4 --tokens 400000 --deadline 1800
nova-config route add pro-deepseek-direct --tier pro --provider deepseek --model deepseek-v4 --tokens 400000 --deadline 1800
nova-config route add pro-grok-openrouter --tier pro --provider openrouter --model x-ai/grok-4 --tokens 300000 --deadline 1800
nova-config tier set pro --routes pro-deepseek-direct,pro-deepseek-opencode,pro-deepseek-direct,pro-grok-openrouter
nova-config apply
```

The harness is launched with `<provider>/<model>`: `--provider` is one word
with no slash, and `--model` is the rest, which may hold slashes
(`x-ai/grok-4`). `--tokens 0` (the default) is unmetered, the deadline the only
stop; `--deadline` is required and above 0; `--enabled false` takes a route out
of the deal. The deal takes `routes[index mod len]` of the card's tier's array
for each card, the index a counter on the fleet table moved by one a card
dealt, and a redeal moves past the routes already taken for that card when
another remains; a card's `model:` header pins it instead and moves no index.
`tier set` refuses a route that is not a row, is disabled or is of another
tier, and `tier remove` is refused (migrate made both rows and the deal reads
them); a tier with an empty array takes its enabled routes in name order;
`nova-config tier list` prints the arrays. The tier is `flash` or
`pro`: frontier cards are never dealt from routes, they escalate to the
coordinator. apply writes the hashes `route:<name>` and `tier:<name>` and the
sets `routes` and `tiers`, which the deal reads.

A route also holds its price sheet, so what a card cost on it is known and
looked up in one place. Every field is optional, and every price is a
decimal, kept exactly as typed in its one spelling (`0.30` is `0.3`), never a
float; a price not set is empty, and a card on a route with no price has no
predicted cost, never a zero:

```
nova-config route set pro-deepseek-opencode --price_input 0.27 --price_cache_read 0.07 --price_cache_write 0 --price_output 1.10 --price_source https://example.com/pricing --price_as_of 2026-10-01
nova-config route set pro-grok-openrouter --price_input 3 --price_output 15 --long_context 128000 --price_input_long 6 --price_output_long 30 --gateway_percent 5.5
nova-config apply
```

| flag | what it is |
| --- | --- |
| `--price_input` | USD per million uncached input tokens |
| `--price_cache_read` | USD per million cached input tokens read |
| `--price_cache_write` | USD per million tokens written to the cache |
| `--price_output` | USD per million output tokens |
| `--reasoning_as_output` | `true` (the default) bills reasoning tokens at the output price; `false` when the provider does not bill them apart |
| `--long_context` | the prompt size in tokens above which a request is priced at the long prices; `0` (the default) is none |
| `--price_input_long`, `--price_output_long` | USD per million input and output tokens above `--long_context`; given with it, or not at all |
| `--price_request` | USD per request, on top of the tokens |
| `--billing` | `metered` (the default: paid per token) or `plan` (a subscription: the predicted cost is the metered price of the same tokens) |
| `--gateway_percent` | the percent a gateway adds on top, a decimal like `5.5` |
| `--price_source` | where the prices were read, free text (a URL) |
| `--price_as_of` | the date they were read, `YYYY-MM-DD` |

A change of prices is a row in the route's history like any other set
(`nova-config route history <name>`), and apply writes the fields into
`route:<name>` beside the rest.

### Refusals

One stderr line each, naming the next step:

```
nova-config machine add: machine studio exists; run: nova-config machine set studio --<field> <value>
nova-config fleet set: --store space names no machine row; run: nova-config machine list
nova-config friend set: friend nobody not found; run: nova-config friend add nobody --<field> <value> ...
nova-config machine remove: machine studio is the --coordinator of the fleet; run: nova-config machine list
nova-config friend remove: friend rowan is the --coordinator of the sprint; run: nova-config friend list
nova-config fleet set: fleet takes no name: it is one row; want fleet set --<field> <value> ...; run: nova-config help
nova-config loop set: loop refresh-m1 has --every 60 and --keepalive true; a loop runs every n seconds or is kept alive, so set one: --every 0 or --keepalive false; run: nova-config loop show refresh-m1
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
CONFIG CHECK kind=loop add=0 set=0 remove=0 rev=0 applied=0
CONFIG CHECK kind=route add=0 set=0 remove=0 rev=0 applied=0
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
CONFIG APPLY kind=loop add=0 set=0 remove=0 rev=0 ms=0
CONFIG APPLY kind=route add=0 set=0 remove=0 rev=0 ms=0
```

`apply` reads Postgres and writes Redis, one kind at a time: machines, the
fleet row, friends, the sprint row, loops, routes. For a machine it writes its machine ceiling
(`ns_capacity_machine`, the ceiling from `--slots`; cores and memory are never
declared, so none are passed) and its registry hash `machine:<m>`. For the
fleet row, `fleet:store` and `fleet:coordinator`, plain keys. For a friend it
writes her desired capacity and roles (`ns_capacity_desired`, `ns_friend_roles`),
charging her slots to the machine her own beat reports, else to the fleet's
coordinator machine; the friend the sprint row names gets the `coordinator`
role in Redis on top of her row's roles, so a handover (`sprint set
--coordinator stella`, then `apply`) is two `SET ... changed=roles`, hers
first. It never touches her logins or wake path: they are her presence's.
For the sprint row, `sprint:coordinator`. For a loop, the hash `loop:<l>`
with every field, its log path, `rev` and `at`, and its name in the set
`loops`: what the plays read to render one unit per loop. For a route, the
hash `route:<r>` with every field, `rev` and `at`, and its name in the set
`routes`: what the deal reads. A name in Redis that Postgres has
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
nova-config inventory --fixture fleet/testdata/inventory-fixture.yml
export NOVA_SPRINT_REDIS=127.0.0.1:6379
nova-config inventory
```

`inventory` reads the applied state, the Redis view `apply` writes, and
never Postgres: the machines (`machines`, `machine:<m>`, its ceiling), the
fleet row, the loops (`loops`, `loop:<name>`), each machine's beat and
`config:decl`'s revisions, in two round trips. It prints an Ansible dynamic
JSON inventory: the groups `all` and `benches` (every machine),
`coordinator`, `store` and `store_deployer` (the machines the fleet row names;
`store_deployer` is the coordinator machine) and `runners` (every machine with
at least one runner), and every host's variables under `_meta.hostvars`. The
machine rows are the one machine list; there is no second one. Each host's
variables are `ansible_host`, `ansible_user` (the row's user, the name ansible
reads for the login), `nova_seat` (the row's seat), `slots`, `runners`,
`kind=machine`, `nova_os` and `nova_arch` from its beat when it has one, and
`nova_loops`, its loop records, once the loop kind has been applied; each
value has one name, and a user or seat that is empty is left out. A deployment
that wants another name for `nova_seat` maps it in its `group_vars`.
`all.vars` holds `nova_store` and `nova_config_rev`. `--fixture <file>` reads
the same rows from a YAML or JSON file and opens no store.

The machine the command runs on is named by the env `NOVA_MACHINE` (an empty
value counts as unset), matched by exact machine name; when no machine row has
that name the verb exits 1 with the known names. When `NOVA_MACHINE` is unset,
the lower-cased first label of the hostname (machine names are lower-case) is
matched the same way, and nothing is marked local when no row has it. The
matched row gets `ansible_connection=local`, so ansible reaches it without
ssh.

`--list` prints all of it and is the default when no flag is given. `--host
<name>` prints one machine's variables; a name with no machine row exits 1 with
the known names, and `--list` with `--host` is refused. `--timeout` (a Go
duration, default `10s`) bounds the wait for the store, so an unattended
ansible run never blocks: on expiry the verb exits 2 with `timed out after <d>
waiting for the store at <addr> while <stage>` and the command to repeat with a
longer timeout.

Ansible's `-i` wants an executable file whose first line, `#!/bin/sh`, is at
column one. These two commands write the two-line wrapper and make it
executable, and the third lets ansible read the inventory:

```
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > nova-inventory
chmod +x nova-inventory
ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-inventory -i ./nova-inventory --list
```

The variable matters: without it, ansible hides a failing inventory script.
When the wrapper exits non-zero (`nova-config` missing from the PATH, a
`nova-config` without the verb, an unknown `NOVA_MACHINE`, a store that is
down, a timeout), `ansible-inventory` and `ansible-playbook` log a warning,
use an empty inventory and exit 0, so a playbook does nothing.
`ANSIBLE_INVENTORY_UNPARSED_FAILED=true` in the environment, or `[inventory]
unparsed_is_failed = True` in `ansible.cfg`, makes the same run exit non-zero.
Ansible starts the script with `--list`; every host's variables are in its
`_meta.hostvars`, so ansible does not call `--host <name>`. The script reads
`NOVA_SPRINT_REDIS` and `NOVA_MACHINE` from the environment ansible passes it.
The plays that read it are [FLEET.md](../FLEET.md)'s.

## What is deliberately not here

Runtime state (beats, states, copies, leases, the table), what a friend
would just know (her machine, harness, logins, wake path: her presence's),
measured facts (a machine's os, arch, cores, memory: its beat's), history
other than the configuration's own (scores, receipts, ledgers, `cap:log`),
secrets (the store holds the name of a variable, never a password), and the
sprint plan.
