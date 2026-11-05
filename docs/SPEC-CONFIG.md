# nova-config

nova-config keeps a fleet's machines and loops as rows in PostgreSQL, every write with a history row, and prints
them as an Ansible dynamic inventory. It knows machine, loop and fleet, and nothing else: no Redis, no scheduler,
no reports from the machines. A fact about a machine is a field someone set.

## Tables

All in the schema `config` (`config/schema.sql`; `nova-config schema` prints it, so a fresh or older store is made current with `nova-config schema | psql "$NOVA_PG_DSN"`). Every kind has
`name`, `created_at`, `updated_at` besides the fields below.

| table | fields |
|---|---|
| `machines` | `address` (what ssh dials; else the name), `user` (required, the ssh user), `os`, `arch`, `cores` (`default` unsets it), `roles` (comma list of `bench`, `runner`, `services`; default `bench`), `runners` (CI runners on it), `note` |
| `loops` | `machine` (a machine row), `argv` (JSON array, the program first), `seat` and `keys` (the names a unit opens its secrets by; never values; `keys` needs `seat`), `every` (seconds) or `keepalive true`, `enabled` (default true), `env` (a JSON object of `NAME` to value strings, names `[A-Z_][A-Z0-9_]*`, refused otherwise; default `{}`), `log` (a path, absolute or `~/`; empty means `~/.local/state/nova/loops/<name>.log`) |
| `fleet` | the one row, named `fleet`: `pg_dsn` (no password). Nothing under `fleet/` reads it today; the inventory emits it as `all.vars.nova_pg_dsn` for a play that needs to reach the store, and it is the one place the store's address is kept as a fleet fact. Set it, or leave it empty (the inventory then leaves the variable out) |
| `history` | one row per write: `kind`, `name`, `op` (add, set, remove), `before`, `after` (the row as JSON text; `reason` rides in the written side), `actor`, `at` |

A machine name is a tailnet host (`^[a-z0-9][a-z0-9-]*$`). A loop is periodic or kept alive, never both and never
neither.

## Verbs

```
nova-config machine|loop list [--json]
nova-config machine|loop show <name> [--json]
nova-config machine|loop add <name> --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
nova-config machine|loop set <name> --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
nova-config machine|loop remove <name> --actor <name> [--reason <line>] [--dry-run]
nova-config fleet show [--json]
nova-config fleet set --pg_dsn <dsn> --actor <name>
nova-config history [<kind> [<name>]] [--since <duration|RFC3339>] [--max <n>] [--json]
nova-config inventory [--list | --host <name>]
nova-config schema
nova-config help [<verb>]
```

```
$ nova-config machine add m1 --user nova --os linux --arch amd64 --cores 16 --roles bench,services --actor a1
CONFIG ADD kind=machine name=m1 rev=1
$ nova-config loop add r-m1 --machine m1 --argv '["prog","--flag"]' --every 60 --actor a1 --reason "first"
CONFIG ADD kind=loop name=r-m1 rev=2
$ nova-config loop set r-m1 --enabled false --actor a1 --dry-run
CONFIG DRY-RUN op=set kind=loop name=r-m1 actor=a1 wrote=nothing
  enabled: true -> false
$ nova-config machine list
MACHINE name=m1 address=- user=nova os=linux arch=amd64 cores=16 roles=bench,services runners=0 note=-
CONFIG LIST kind=machine rows=1
$ nova-config history loop
HISTORY at=2026-01-02T00:00:02Z actor=a1 kind=loop name=r-m1 op=add rev=2
  machine: "m1"
  argv: ["prog","--flag"]
  ...
  reason: "first"
CONFIG HISTORY rows=1
```

A write is one transaction, the row and its history row; the printed `rev` is the history id. A printed value is
`-` when empty, bare when it is one word of printable characters (`=` is never escaped), and a JSON string
otherwise; `argv` and `env` are printed as their JSON, and free text (`note`, a `reason`) is always a JSON string, so
what is printed can be pasted back. A dry run prints the event line, then one line per field: `field: old -> new`
for a set (`note: "" -> "the first"`), `field: value` for an add or a remove, values as JSON. `show` adds `created=`, `updated=` and, for a
machine, `loops=`. The store is `--pg <dsn>` or `NOVA_PG_DSN` (no password in it); the password comes from the
variable `NOVA_PG_PASSWORD_ENV` names, else `NOVA_PG_PASSWORD`. Exit codes: 0 done, 1 refused (the store or the row said no),
2 could not run (a usage error, no store, a store that cannot be reached).

Every verb path takes `-h` and `--help` (and `nova-config help <verb path>`) and prints its usage, exit 0. A usage error
(an unknown flag or verb, a missing required flag, all of them named at once) is one line on stderr, exit 2, and names the
kind's fields: `nova-config machine add: missing required flag --user, --actor; machine fields: name, address, user, os,
arch, cores, roles, runners, note; run: nova-config machine add -h`. A refusal is one line and says what to do:

| cause | line |
|---|---|
| the schema was never applied (SQLSTATE 42P01, 42703, 3F000) | `store has no current schema; run: nova-config schema \| psql "$NOVA_PG_DSN"` (exit 2) |
| a loop names no machine | `no machine <x>; the machines are a,b,c` |
| a machine still runs loops | `machine <x> still runs loops l1,l2; remove them first: nova-config loop remove l1 --actor <you>` |
| a row exists | `machine <x> exists; use: nova-config machine set` |
| a row is not found | `machine <x> not found; run: nova-config machine list` |
| postgres cannot be reached | `cannot reach postgres at <host:port> (NOVA_PG_DSN): <one-line cause>` (exit 2) |

pgx's multi-line errors are never printed.

## History

`history [<kind> [<name>]]` reads the `history` table, newest first: `HISTORY at= actor= kind= name= op= rev=`, then the
fields that changed (a set), or all of them (an add or a remove), and the `reason`. `kind` is `machine`, `loop` or
`fleet`. `--since` is a duration back from now (`90m`, `24h`, `7d`) or an RFC 3339 time; `--max` is the rows shown
(default 20, 0 for all; `more=true` on `CONFIG HISTORY rows=N` says rows were left out); `--json` prints one JSON object
with each row's `changes` as `field: {before, after}`.

## Inventory

`inventory` prints Ansible's dynamic inventory JSON (`--list`, the default, or `--host <name>`). Use it as a
script: `#!/bin/sh` then `exec nova-config inventory "$@"`.

- groups: `all` (every machine), `bench`, `runner` and `services`. **Each group is named exactly as its role** (the
  roles are `bench`, `runner`, `services`); `benches` and `runners` are gone, so a play that says `hosts: benches` or
  `hosts: runners` says `bench` or `runner`. `runner` also holds a machine whose `runners` is above 0
- `all.vars.nova_pg_dsn`: the fleet row's `pg_dsn`, when set
- per machine in `_meta.hostvars`: `ansible_host` (address, else the name), `ansible_user`, `runners`, `nova_os`,
  `nova_arch` and `cores` (each left out when unset), `ansible_connection: local` on the machine the inventory
  runs on (`NOVA_MACHINE`, else the host name's first label, lower-cased), and `nova_loops`
- `nova_loops`: the machine's loops, always a list, each with `name`, `argv`, `seat`, `keys` (a list), `every`,
  `keepalive`, `enabled`, `env` (an object, `{}` when none) and `log` (the row's `log`, else
  `~/.local/state/nova/loops/<name>.log`)

## Old columns

Apply the schema before running this binary against an older store (`nova-config schema | psql "$DSN"`); nothing
migrates on start, and a read of an unmigrated store fails on the missing columns.

An older store has columns this tool no longer knows: on `machines` `seat`, `slots`, `width`, `gates_only`, `tla`;
on `fleet` `store`, `coordinator`, `redis_port`, `bus`, `loops_dir`. They stay, with their values, and are ignored:
every statement names its columns and none reads `*`. They may be dropped by hand later
(`ALTER TABLE config.machines DROP COLUMN seat`, and so on). The schema drops no column. It adds the machine
columns `address`, `os`, `arch` and `cores`, the loop columns `env` and `log`, and lifts two constraints that would block work on the new rows: the
foreign keys `fleet_store_fkey` and `fleet_coordinator_fkey` (so a machine the old fleet row names can be removed)
and `NOT NULL` on `machines.seat` (so `machine add` works). The schema is idempotent: run it twice and nothing changes.

An old `roles` value the tool no longer knows (`coordination`) is read, listed and ignored by the inventory. A
`set --roles` checks only the value it is given against `bench`, `runner` and `services`, and replaces the whole
list, so setting roles on such a row drops the old role; leave `--roles` out of a `set` to keep it.
