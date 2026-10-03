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
fleet row holds the store endpoints and machines (the store, the coordinator machine); the
sprint row holds who coordinates; a friend's roles are what the deal reads.
A loop's row is a process someone decides runs on one machine: its command,
the seat and secret names it opens, and how it runs. A route's row is one way
someone decides to run a model tier: the provider and model, the budget and
deadline. A tier's row is the order someone decides the deal takes a tier's
routes in.

Where each field of this cut sits:

| side | fields |
| --- | --- |
| machine (varies per machine) | `user`, `seat`, `slots`, `runners`, `width`, `tla`, `note` |
| fleet (one value for the whole fleet) | `store`, `coordinator` (both machines), `redis_port`, `pg_dsn` |
| friend (decided for her) | `slots`, `tiers`, `roles`, `width` |
| sprint (one value for the whole sprint) | `coordinator` (a friend) |
| loop (decided per supervised process) | `machine`, `argv`, `seat`, `keys`, `every`, `keepalive`, `width`, `enabled` |
| route (decided per way to run a tier) | `tier`, `provider`, `model`, `tokens`, `deadline`, `enabled`, and the price sheet: `price_input`, `price_cache_read`, `price_cache_write`, `price_output`, `reasoning_as_output`, `long_context`, `price_input_long`, `price_output_long`, `price_request`, `billing`, `gateway_percent`, `price_source`, `price_as_of`, `note` |
| tier (decided per tier) | `routes` |

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
`created_at` and `updated_at`. Two kinds, the two an operator decides about
by hand, carry a `note` column, the reason a choice was made (see "The note"
below); no other kind has one. The CLI's flags, help, refusals, SQL,
typed lines and apply diff are all generated from the descriptor, so every
kind has identical verbs and a new kind adds no verb code.

### Singleton kinds

A kind descriptor may declare `Singleton: true`: a kind of exactly one row,
named as the kind is: there is one fleet and one coordinator at a time. Its
migration creates the row (`INSERT ... ON CONFLICT DO NOTHING`), so the
grammar has no `add`, `remove` or `list`, and its `set`, `show` and `history`
take no name:

```
nova-config fleet set --store <machine> --coordinator <machine> --redis_port <port> --pg_dsn <uri> --as <friend>
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
| `bool` | `true` or `false` (any spelling `strconv.ParseBool` reads, stored in that one) | `boolean` |
| `keys` | a comma list of environment variable names (letters, digits, underscores, not starting with a digit), deduplicated and sorted: the names of secrets, never a value | `text` |
| `argv` | a command as a JSON array of strings, the program first and not empty, no line break or NUL in a word, at most 64 words and 4096 bytes; stored in its compact JSON spelling | `text` |

A value is canonicalised before it is stored (`Field.Canonical`), so a row
compares equal to its Redis view field by field. A field add is not given is
stored as its `Default` when the descriptor declares one, else its type's
zero (0, false, empty). A rule across a row's fields that no one field can
say is the kind's `Check`: `add` runs it on the new row before any store is
opened (exit 2), and every store runs it on the row a `set` would leave and
refuses the set (exit 1, `ErrInvalid`), writing nothing. `add` refuses a row missing
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
| `slots` | int | yes | apply: the machine ceiling the friends' desired slots must fit under (`ns_capacity_machine`, `ns_capacity_desired`); not the sprint's width | `machine:<m>:ceiling` (`ns_capacity_machine`) and `machine:<m>` |
| `runners` | int | (0) | the CI play: how many runners it hosts; 0 hosts none | `machine:<m>` |
| `width` | int | (unset) | `nova-sprint fleet sync`: the most work cards the sprint's member on it runs at once; unset is the default, half the machine's cores as its beat reports them; 0 is no member | `machine:<m>` |
| `tla` | bool | (false) | the inventory's `tla` group and `nova_tla`, so the tools play's tla play holds the pinned TLC jar there; `tlacheck run --bench any` picks among these (tla/README.md, "The record machines") | `machine:<m>` |
| `note` | text | (empty) | a reader: why the machine is as it is, a hold, a rest, the load that was measured (see "The note") | `machine:<m>` |

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

**The sprint's width.** The machine row's `width` is the sprint member's
width, set directly (`nova-config machine set <m> --width <n>`; the owner,
2026-10-01: "we should just be able to set width specifically in nova-config
and it just works"). Nothing else takes part: not the machine's `slots`, not
any friend row, not any beat, and no Redis is read. A machine with a width of
1 or more is a member of the sprint's fleet; a machine with width 0 is not.
The width is optional: a row with none (unset on add, or cleared with
`nova-config machine set <m> --width default`; -1 is refused) has the default
width, half the machine's logical cores as its `nova-sprint fleet beat`
reports them, which `nova-sprint fleet sync` resolves and writes to the fleet
table as a number (the owner, 2026-10-02: "width=-1 in config means default and
default is CPUs/2"; the shape: unset means default, never a literal in the
config); until a beat reports the cores, the machine is not a member and the
sync says so. `machine width` prints `width=default` for it (`--json`:
`"default":true`). Migration 0019 makes the column nullable; every width a row
held stays. It is a static share, the same on every read of the same row:
the CI legs running on the machine and every other child are taken off at the
take, by a lease from the machine's one slot store, never in the width.
`nova-config machine width <name>` prints it (`Widths`,
`internal/config/width.go`), and `nova-sprint fleet sync` moves it to the
fleet table. Until migration 0012 the width was derived (the machine's `slots`
less the `slots` of the friends charged to it: the machine a friend's beat
named, else the fleet row's coordinator machine); 0012 filled `width` with
the rule's beat-free part, every friend charged to the coordinator machine
(never below 0) and every other machine its `slots`
(docs/nova-config/README.md, "Migrating to a set width").

**A machine's own name.** `nova-config machine self` prints the name this
machine has in the inventory, so no name is typed on the machine it names:
`NOVA_MACHINE` when set, else the first label of the host's DNS name on the
tailnet when a tailnet is running (asked of `tailscale status --json`, only
when the program is installed), else the first label of the hostname, always
lower-case. It opens no store. `--check` reads the machine rows and exits 2
when the name is none of them, 3 when the name or the rows cannot be read
(`SelfName`, `internal/config/self.go`).

**`fleet`** (`config.fleet`, singleton): the one row of fleet-wide facts.
The coordinator machine is one machine; which friend drives it is the sprint
row's.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `store` | ref machine | | the plays: the machine that runs Redis | `fleet:store` |
| `coordinator` | ref machine | | the plays: where the coordinator's loops run; apply: the machine a friend with no beat is charged to | `fleet:coordinator` |
| `redis_port` | nullable int (no default) | | the inventory and plays: explicit Redis TCP port, 1 through 65535; unset until declared | `fleet:redis_port` |
| `pg_dsn` | text | | the inventory and tools play: the explicit password-free Postgres URI; empty until set, never derived from `store` | `fleet:pg_dsn` |
| `loops_dir` | text | | the inventory and plays: the directory a loop's log is written under; seeded to `~/nova-bench/loops` by migration 0020, never a literal in code | `fleet:loops_dir` |

The kind's `Check` bounds `redis_port` and accepts only a password-free
`postgres://user@host[:port]/database` URI for a nonempty `pg_dsn`. A refusal
never reproduces a password from the input. `loops_dir` must be non-empty and
is refused where it is read, not in `Check`: a store's fleet row carries it
empty until a migration or a set gives it one, so apply refuses an empty
applied directory, naming `nova-config fleet set --loops_dir <path>`, and
never substitutes a path of its own. Fleet apply and inventory refuse either endpoint
unset, naming one `nova-config fleet set --redis_port <port> --pg_dsn <dsn>`
command. Migration 0014 (`0014_fleet_endpoints.sql`) leaves the port NULL and
the DSN empty. Full apply checks both before writing any kind; applying
another kind alone does not require these endpoints.

**`friend`** (`config.friends`): what someone decides for a friend. Anything
a friend would just know is runtime Redis data. Where she runs, her harness, her logins and her wake path are hers: her own
presence reports them (`friend:<f>:beat`, `friends:login`,
`friend:<f>:wakepath`), and this tool never writes or reads them as
configuration. Who coordinates is not her field either: it is the sprint's.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `slots` | int | yes | apply: her desired slots, under the ceiling of the machine she is charged to; no machine's width | `friend:<f>:desired` slots (`ns_capacity_desired`) |
| `tiers` | list: flash, frontier, pro | yes | the deal's tier filter (capacity.lua `filter_ok`): which she can do | `friend:<f>:desired` tiers (`ns_capacity_desired`) |
| `roles` | list: builder, may-hold, reader | | the deal and the routing: what she may hold | `friend:<f>:roles` (`ns_friend_roles`) |
| `width` | int, at least 1, default 8 | | nova-sprint friend sync: the jobs she works at once, her friends-table width (the owner, 2026-10-02: "6/1 seems a bit wrong -- need to setup width for friends? Start at 8 for each?") | `friend:<f>:desired` width |

**`sprint`** (`config.sprint`, singleton): the one row of sprint-global
facts.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `coordinator` | ref friend | | the deal and the routing: who holds the coordinator role; `sprint set --coordinator <friend>` is the handover | `sprint:coordinator`, and the `coordinator` word in that friend's `friend:<f>:roles` |

**`loop`** (`config.loops`): a supervised process on one machine. Every
value is data in the row: the code names no machine, seat, secret or
program. A secret is never part of the command; the loop names the secrets
it needs in `keys`, and the unit opens them from `seat` on that machine
(`nova-secrets exec --only <keys>`), so they reach the process's environment
only. The plays render one unit per row from the Redis view apply writes.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `machine` | ref machine | yes | the plays: the machine the unit is installed on | `loop:<l>` |
| `argv` | argv | yes | the plays: the unit's command, word for word; a `nova-swarm member` argv spells no `--width`, its width is its machine row's (`Check`) | `loop:<l>` |
| `seat` | text | | the plays: the nova-secrets seat on that machine the unit opens its secrets from; empty when it needs none | `loop:<l>` |
| `keys` | keys | | the plays: the names of the secrets the unit opens from the seat; empty when none, and a non-empty list needs a seat | `loop:<l>` |
| `every` | int | (0) | the plays: seconds between runs of a periodic unit | `loop:<l>` |
| `keepalive` | bool | (false) | the plays: a long-running unit, restarted when it exits | `loop:<l>` |
| `enabled` | bool | (true) | the plays: false writes the unit and does not start it | `loop:<l>` |

The kind's `Check`: exactly one of `every` above 0 and `keepalive` true (a
loop runs every n seconds or is kept alive), `keys` only with a `seat`, and a
`nova-swarm member` argv (a reader's too) that spells no `--width` before any
`--`: a worker's width is its machine row's, moved to the fleet row by `fleet
sync` and read with its queue every tick, a member's own row and a reader's
the row of the machine it is named for (`reader-<m>`, docs/SPEC-SPRINT.md
section 6); the refusal names the rule and `machine set <m> --width <n>`.
Migration 0013 had made a loop's width a field its command ran with;
migration 0017 removed the field, took `--width` out of every member argv that
carried one and removed the second reader rows (`reader-<m>-2`), one reader
per machine. The log path is derived from the fleet row's `loops_dir` and the
loop's name, `<loops_dir>/<name>.log` (`LoopLog`), and is never typed. A
machine a loop names cannot be removed (`machine m1 is the --machine of loop
member-m1`); `machine show <m>` names the machine's loops (`loops=<a,b>`, `-`
for none).

**`route`** (`config.routes`): one way to run a model tier, the provider
and model a card of that tier runs on, its token budget and deadline. A
tier has several routes so the deal spreads its cards across providers and
models, in the order of the tier's array (the `tier` kind below); a card's
`model:` header pins it instead. Frontier cards are never dealt from
routes: they escalate to the coordinator, so `frontier` is no route's tier. Every value is data in the
row: the code names no provider or model.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `tier` | enum `flash`, `pro` | yes | the deal: the cards of this tier are dealt on it | `route:<r>` |
| `provider` | text | yes | the deal: the provider word of the model id `<provider>/<model>` the harness is launched with; one word, no slash | `route:<r>` |
| `model` | text | yes | the deal: the model name after the provider; it may hold slashes (`x-ai/grok-4`) | `route:<r>` |
| `tokens` | int | (0) | the deal: the token budget per card; 0 is unmetered and the deadline is the only stop | `route:<r>` |
| `deadline` | int | yes | the deal: the seconds a card on this route may run, above 0 | `route:<r>` |
| `enabled` | bool | (true) | the deal: false takes it out of the deal, and needs a `note` | `route:<r>` |
| `price_input` | decimal | (empty) | a card's cost: USD per million uncached input tokens | `route:<r>` |
| `price_cache_read` | decimal | (empty) | a card's cost: USD per million cached input tokens read | `route:<r>` |
| `price_cache_write` | decimal | (empty) | a card's cost: USD per million tokens written to the cache | `route:<r>` |
| `price_output` | decimal | (empty) | a card's cost: USD per million output tokens | `route:<r>` |
| `reasoning_as_output` | bool | (true) | a card's cost: reasoning tokens billed at the output price; false when not billed apart | `route:<r>` |
| `long_context` | int | (0) | a card's cost: the prompt size in tokens above which a request is priced long; 0 is none | `route:<r>` |
| `price_input_long` | decimal | (empty) | a card's cost: USD per million input tokens above `long_context` | `route:<r>` |
| `price_output_long` | decimal | (empty) | a card's cost: USD per million output tokens above `long_context` | `route:<r>` |
| `price_request` | decimal | (empty) | a card's cost: USD per request | `route:<r>` |
| `billing` | enum `metered`, `plan` | (metered) | a card's cost: paid per token, or by a subscription (the predicted cost is then the metered price of the tokens) | `route:<r>` |
| `gateway_percent` | decimal | (empty) | a card's cost: the percent a gateway adds on top | `route:<r>` |
| `price_source` | text | (empty) | a reader: where the prices were read (a URL) | `route:<r>` |
| `price_as_of` | text, `YYYY-MM-DD` | (empty) | a reader: the date the prices were read | `route:<r>` |
| `note` | text | (empty) | a reader: why the route is as it is; a disabled route carries its reason (see "The note") | `route:<r>` |

The price sheet is optional: the owner, 2026-10-01, "the pricing
configuration saved per-tuple, so it is known and easily look upable". A
decimal is digits with an optional fraction after one point, no sign and
no exponent, kept as text in its one spelling (`internal/cardcost`,
`Canonical`: `0.30` is `0.3`), never a float; empty is not set, never 0.

The kind's `Check`: `provider` is one word with no slash or blank, `model`
is not empty and has no blank, and `deadline` is above 0; `long_context`
above 0 comes with both long prices, and a long price with a threshold;
`price_as_of` is a date; a route that is disabled has a note. A route names no row of another kind.

### The note

The owner, 2026-10-02, on the route and width choices the first real sprint made
by hand: "your choices, these should be saved somewhere permanent with notes
(ideally, nova-config)" (nova-tools#5101). The route row and the machine row
carry a `note`: free text on one line, empty by default, the last field of the
row, so the reason a route was taken out of the deal or a machine held is kept
with the row and no longer in a comment.

- `--note '<text>'` sets it on `route add`, `route set`, `machine add` and
  `machine set`; `--note ''` clears it.
- `route show` and `machine show` print it whole; the list verbs cut it to
  `ListNoteRunes` (60) characters and end it in `...`, so a row stays one short
  line (`ListLine`; a field's `Cut` flag in the descriptor); `--json` prints it
  whole in both.
- It is a field like any other, so `route history` and `machine history` show
  `note=<before>><after>` with the actor and the time of every change: who
  wrote which note when.
- Apply writes it into the hash `route:<r>` and `machine:<m>` beside the other
  fields, so the sprint can show it later; nothing reads it yet.
- **A disabled route carries its reason.** `route set --enabled false` with no
  `--note` in the same set is refused, before any store is opened, with
  `say why: --note '<the measured reason>'` (`Kind.CheckChanges`); so is a
  `route add --enabled false` with no note, and a set that would leave a
  disabled route with an empty note, as `--note ''` on one (the kind's
  `Check`, which every store runs). `--enabled true` needs none. A route
  disabled before migration 0015 has an empty note until someone writes one;
  setting its note is the way, and any other set of it that leaves it disabled
  is refused until then.

Migration 0015 (`0015_row_notes.sql`) adds the column to `config.routes` and
`config.machines`, `text NOT NULL DEFAULT ''`: every old row backfills to
empty, and the file run again keeps a note written since. A binary with the
note refuses every machine verb on a store older than 0015 as it refuses
every route verb on one older than 0007 (`behindSchema`).

**`tier`** (`config.tiers`): a model tier's route array (the owner,
2026-10-01: "the per-tier provider/model array should be specified in
nova-config"). It has two rows, `flash` and `pro`, made by migrate, so `set`
takes them on a new store and there is nothing to add. The deal takes
`routes[index mod len]` for each card of the tier, the index a uint64
counter on the fleet table (`route_index_flash`, `route_index_pro`), moved
by one a card dealt; a redeal moves past the entries it leaves out
(internal/sprint/route.go, tla/RouteIndex.tla). A route named twice takes
two turns: the array is how a route gets more of the tier's cards.

| field | type | required | who reads it | Redis |
| --- | --- | --- | --- | --- |
| `routes` | ordered comma list of route names, a name repeated as given | (empty) | the deal: the tier's array, read with the routes in one round trip; empty takes the tier's enabled routes in name order | `tier:<t>` |

`set` refuses a name that is no route row, a disabled route, or a route of
another tier (the store's check beside the kind's), and `remove` refuses
both rows: the deal reads them. A route disabled or removed after the array
is set is skipped by the deal. `tier list` prints the arrays.

## The schema

Migrations are numbered SQL files compiled into the binary
(`internal/config/migrations/NNNN_<what>.sql`), applied in order, each in its
own transaction with its row in `config.schema_migrations`. A version applied
is never applied again, so `nova-config migrate` on a migrated database
applies nothing and says so (`applied=0`). `0001_schema.sql` makes the
schema, the ledger and the history table; every later file is one kind.

The role that runs migrate must own every table in schema config and be
able to create in it: migrations alter and fill tables, which only their
owner may do. Before applying anything migrate reads the owners in one
catalog query (`Store.Ownership`) and decides with `MigrateGaps` (the role,
the owners, the pending migrations; the SQL is never read): with a migration
pending and a table another role owns, it refuses before applying any and
prints one `ALTER TABLE config."<table>" OWNER TO "<role>";` per table for a role
with the owners' rights to run once. Table and role identifiers are always
quoted, with an embedded quote doubled. It never changes an owner or a grant. A
`--file` store has no roles (the zero `Ownership`), so the check passes there.

After every migrate the `nova_read` role, when it exists, is granted `USAGE`
on the schema and `SELECT` on every table: a runtime tool that reads
configuration straight from Postgres does it as that role.

```
config.schema_migrations (version integer PK, applied_at timestamptz)
config.history           (id bigserial PK, kind, name, op add|set|remove,
                          before jsonb, after jsonb, actor, at timestamptz)
config.machines          (name PK, "user", seat, slots, runners,
                          created_at, updated_at; width added by 0012,
                          filled with slots less the friends' slots on the
                          coordinator machine, slots elsewhere; note added
                          by 0015, text NOT NULL DEFAULT ''; tla added by
                          0016, false; width nullable by 0019, NULL the
                          default)
config.fleet             (name PK = 'fleet', store -> machines.name,
                          coordinator -> machines.name, redis_port, pg_dsn,
                          loops_dir, created_at, updated_at;
                          the one row inserted by the migration; loops_dir
                          added by 0020, text NOT NULL DEFAULT '~/nova-bench/loops')
config.friends           (name PK, slots, tiers, roles, created_at, updated_at;
                          width added by 0018, integer NOT NULL DEFAULT 8, which fills every row there
                          CHECK (width >= 1))
config.sprint            (name PK = 'sprint', coordinator -> friends.name,
                          created_at, updated_at; the one row inserted by
                          the migration; reader_tier added by 0010, dropped
                          by 0011)
config.loops             (name PK, machine -> machines.name, argv, seat, keys,
                          every, keepalive boolean, width, enabled boolean,
                          created_at, updated_at; CHECK exactly one of
                          every > 0 and keepalive, keys only with a seat,
                          argv a JSON array: the kind's Check again, as a
                          wall behind the tool)
config.routes            (name PK, tier flash|pro, provider, model, tokens,
                          deadline, enabled boolean, created_at,
                          updated_at; CHECK provider one word with no slash,
                          model with no blank, deadline > 0, tokens >= 0:
                          the kind's Check again; weight dropped by 0008;
                          the price sheet added by 0009: the decimals as
                          text with a CHECK on their shape,
                          reasoning_as_output boolean, long_context,
                          billing metered|plan, price_source, price_as_of;
                          note added by 0015, text NOT NULL DEFAULT '')
config.tiers             (name PK flash|pro, routes, created_at, updated_at;
                          CHECK routes a comma list of names; the two rows
                          inserted by the migration)
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
its coordinator machine), friends, the sprint row, loops (each names a
machine), routes:

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
zero, the revision unchanged (steady apply is 8 Redis round trips down from 18, two of them the loop
and route kinds' reads of a store with none of their rows: each kind's set
and stamp in one trip, its hashes in a second only when it has rows;
first run across the seed kinds takes 32 trips down from the 42 baseline, with
each machine running its ceiling check before its write transaction).

### What apply writes, per kind

**machine:** `ns_capacity_machine(m, slots)` for the ceiling (refused
`CEILING` when the friends and benches on it already desire more than
`slots`; cores and memory are never declared, so the call carries none and
derives no budget); the hash `machine:<m>` with user, seat, slots, runners,
width, tla, note, rev, at; the set `machines`. slots is read back from the ceiling, the key the
runtime guards on, so a ceiling moved by hand is put back by the next apply.
Remove: refused while any friend or bench desired hash names the machine;
else `machine:<m>`, `machine:<m>:ceiling` and `machine:<m>:budget` are
deleted and the name leaves `machines`.

**fleet:** one plain `SET fleet:<field> <value>` per declared field, `DEL` for
a field the row leaves empty. Never removed.

**friend:** `ns_capacity_desired(friend, f, slots, machine, ..., tiers)`
(registers in `friends`, writes `friend:<f>:desired`, refuses `CEILING`).
The machine is the one her slots are charged to: the `host` her own beat
(`friend:<f>:beat`) reports when she has one (friends may run on any bench),
else the fleet's coordinator machine (`fleet:coordinator`, written a moment
before) as the default charge; neither is a refusal naming `nova-config
fleet set --coordinator <machine>`. Her width, when it differs, is a plain
`HSET friend:<f>:desired width <n>`, a field no function reads or writes.
`ns_friend_roles(f, roles)` when the
roles differ (the actor must hold the coordinator role in Redis, or nobody
does yet and this row makes the first): the roles written are the row's
plus `coordinator` for the friend the sprint row names. Nothing else: her
logins, wake path and harness are her presence's. Remove: refused while
`friend:<f>:cards:working` has a member, naming the copies; else the
registry member, the desired and roles hashes are removed in one
transaction, with a `config-remove` receipt in `cap:log`; her beat, logins
and wake path stay, they are hers.

**sprint:** a plain `SET sprint:<field>` for each field (`sprint:coordinator <friend>`), `DEL` when empty.
Never removed. The handover is `nova-config sprint set --coordinator
stella --as rowan` then `apply`: the sprint kind's own revision moves and
the friend kind's plan is two `SET ... changed=roles`, stella's first.

**loop:** the hash `loop:<l>` with every field of the row, `name`, `log`
(the derived path), `rev` and `at`, written whole in one transaction with
the name added to the set `loops`; this is the view the plays read. A field
the hash lacks or holds out of shape reads back as the type's zero, so a
hash changed by hand is put back by the next apply. Remove: the hash and the
name in `loops` go in one transaction, with a `config-remove` receipt in
`cap:log`.

**route:** the hash `route:<r>` with every field of the row, `name`, `rev`
and `at`, written whole in one transaction with the name added to the set
`routes`; this is the view the deal reads. It reads, writes and removes as
a loop does (one code path, `hashKinds`), with no derived field.

`machine:<m>`, `machines`, `fleet:*`, `sprint:coordinator`, `loop:<l>`,
`loops`, `route:<r>` and `routes` are nova-config's own keys: no function in
the library reads or writes them.

## Lines

One typed line per event; values go through `oneline.Field`, an empty value
prints as `-`.

```
CONFIG ADD kind=<k> name=<n> rev=<id>
CONFIG SET kind=<k> name=<n> rev=<id> changed=<f,g>
CONFIG REMOVE kind=<k> name=<n> rev=<id>
<KIND> name=<n> <field>=<v> ...                          (list: one per row; a note is cut to 60 characters and ends in ...)
MACHINE name=<n> <field>=<v> ... os=<v> arch=<v> cores=<n> memory_gb=<n> beat=<t>   (list and show with a Redis: the live facts, - each when the beat lacks it)
MACHINE name=<n> <field>=<v> ... beat=none                (with a Redis: no beat)
MACHINE name=<n> <field>=<v> ... created=<t> updated=<t> loops=<l,...> [live facts]   (show: the machine's loops, - for none)
CONFIG LIST kind=<k> rows=<n>
<KIND> name=<n> <field>=<v> ... created=<t> updated=<t>  (show)
HISTORY id=<id> kind=<k> name=<n> op=<op> actor=<a> at=<t> <field>=<before>><after> ...
CONFIG HISTORY kind=<k> name=<n> changes=<n>
CHECK ADD|SET|REMOVE kind=<k> name=<n> [changed=<f,g>]
CONFIG CHECK kind=<k> add=<n> set=<n> remove=<n> rev=<r> applied=<redis rev>
APPLY ADD|SET|REMOVE kind=<k> name=<n> [changed=<f,g>]
CONFIG APPLY kind=<k> add=<n> set=<n> remove=<n> rev=<r> ms=<n>
MIGRATION version=<v> file=<f> lines=<n>                 (migrate --print)
MIGRATION version=<v> file=<f> lines=<n> state=applied|pending|missing   (migrate --dry-run: the ledger; missing is below the greatest recorded and not in the ledger, which migrate will not apply)
CONFIG MIGRATE print=<n> pg=-
MIGRATE NOT-OWNED table=config.<t> owner=<role> role=<role>   (migrate --dry-run: a table the role does not own)
MIGRATE WOULD-REFUSE <the refusal migrate would print>; run: ALTER TABLE config."<t>" OWNER TO "<role>"; ...   (migrate --dry-run, ready=no)
CONFIG MIGRATE pg=<user@host:port/db>|file=<path> from=<v> to=<v> applied=<n> [dry_run=true pending=<n> missing=<n> role=<role> ready=yes|no]
CONFIG STATUS pg=<...>|file=<path> schema=<v> <kind>=<rows> <kind>_rev=<r> ... redis=<addr> <kind>_applied=<r> ...   (a singleton: <kind>_rev alone)
CONFIG DRY-RUN op=<op> kind=<k> name=<n> actor=<a> wrote=nothing <field>=<v>|<field>=<before>><after> ...   (add, set, remove --dry-run)
NOTE machine=<m> width=0: no sprint member, ...; run: nova-config machine set <m> --width <n> ...   (machine add with no --width)
LOOP name=<n> <field>=<v> ... created=<t> updated=<t>   (loop show; the argv is the words the unit runs)
CONFIG KIND name=<k> table=config.<t> fields=<f,...> required=<f,...> rows=many|one
CONFIG KINDS count=<n>
```

Exit codes: 0 done; 1 refused (the store or Redis said no: a duplicate, a
missing row, a ref naming no row, a row another names, a set that breaks
the kind's `Check`, a ceiling, working copies, `CONFLICT`, a status behind;
and `migrate --dry-run` ending `ready=no`, which prints its lines and no
refusal line, since nothing was attempted); 2 usage (a flag, a value, a name on a
singleton, a store that did not answer). A refusal is
one stderr line, `nova-config <verb> REFUSED: <why>; run: <next step>`; a
usage refusal's next step is the verb's own `-h`. `--json` prints the same
result as one object in `internal/tool`'s shape (`result`, `facts`, `items`,
`notes`). `--file <path>` stands in for `--pg`: the same store, kept in a
local JSON file (`config.FileStore`), for trying the tool with no database.

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
