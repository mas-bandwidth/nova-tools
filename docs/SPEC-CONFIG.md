# nova-config

nova-config keeps a fleet's machines and services as rows in PostgreSQL, every write with a history row, and prints
them as an Ansible dynamic inventory. It knows machine and service, and nothing else: no Redis, no scheduler,
no reports from the machines. A fact about a machine is a field someone set.

## Tables

All in the schema `config` (`config/schema.sql`; `nova-config schema` prints it, so a fresh or older store is made current with `nova-config schema | psql "$NOVA_PG_DSN"`). Every kind has
`name`, `created_at`, `updated_at` besides the fields below.

| table | fields |
|---|---|
| `machines` | `address` (what ssh dials; else the name), `user` (required, the ssh user), `os`, `arch`, `cores` (`default` unsets it), `roles` (comma list of `bench`, `runner`, `services`; default `bench`), `runners` (CI runners on it), `note` |
| `services` | `machine` (a machine row), `argv` (JSON array, the program first), `seat` and `keys` (the names a unit opens its secrets by; never values; `keys` needs `seat`), `every` (seconds) or `keepalive true`, `enabled` (default true), `env` (a JSON object of `NAME` to value strings, names `[A-Z_][A-Z0-9_]*`, refused otherwise; default `{}`), `log` (a path, absolute or `~/`; empty means `~/.local/state/nova/services/<name>.log`; **the default log path moved there** from `~/nova-bench/loops/<name>.log`) |
| `history` | one row per write: `kind`, `name`, `op` (add, set, remove), `before`, `after` (the row as JSON text; `reason` rides in the written side), `actor`, `at` |

A machine name is a tailnet host (`^[a-z0-9][a-z0-9-]*$`). A service is a program a machine keeps running; with `--every N` it runs every N seconds instead. It is one or the other,
never both and never neither.

## Verbs

```
nova-config machine|service list [--json]
nova-config machine|service show <name> [--json]
nova-config machine|service add <name> --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
nova-config machine|service set <name> --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
nova-config machine|service remove <name> --actor <name> [--reason <line>] [--dry-run]
nova-config history [<kind> [<name>]] [--since <duration|RFC3339>] [--max <n>]
nova-config inventory [--list | --host <name>]
nova-config schema
nova-config help [<verb>]
```

```
$ nova-config machine add m1 --user nova --os linux --arch amd64 --cores 16 --roles bench,services --actor a1
CONFIG ADD kind=machine name=m1 rev=1
$ nova-config service add r-m1 --machine m1 --argv '["prog","--flag"]' --every 60 --actor a1 --reason "first"
CONFIG ADD kind=service name=r-m1 rev=2
$ nova-config service set r-m1 --enabled false --actor a1 --dry-run
CONFIG DRY-RUN op=set kind=service name=r-m1 actor=a1 wrote=nothing
  enabled: true -> false
$ nova-config machine list
MACHINE name=m1 address=- user=nova os=linux arch=amd64 cores=16 roles=bench,services runners=0 note=-
CONFIG LIST kind=machine rows=1
$ nova-config history service
HISTORY at=2026-01-02T00:00:02Z actor=a1 kind=service name=r-m1 op=add rev=2
  machine: "m1"
  argv: ["prog","--flag"]
  ...
  reason: "first"
CONFIG HISTORY rows=1
```

A write is one transaction, the row and its history row; the printed `rev` is the history id. A `set` that changes nothing writes nothing: it prints `CONFIG SET ... changed=none rev=unchanged` and adds no history row; `changed=` otherwise names only the fields that differ. A printed value is
`-` when empty, bare when it is one word of printable characters (`=` is never escaped), and a JSON string
otherwise; `argv` and `env` are printed as their JSON, and free text (`note`, a `reason`) is always a JSON string, so
what is printed can be pasted back. `--json` prints one object with typed values (`argv` an array, `env` an object, `every` a number, `keepalive` a boolean, an unset `cores` null). A dry run prints the event line, then one line per field: `field: old -> new`
for a set (`note: "" -> "the first"`), `field: value` for an add or a remove, values as JSON. `show` adds `created=`, `updated=` and, for a
machine, `services=`. The store is `--pg <dsn>` or `NOVA_PG_DSN` (no password in it: a dsn that carries one is refused, exit 2); the password comes from the
variable `NOVA_PG_PASSWORD_ENV` names, else `NOVA_PG_PASSWORD`. Exit codes: 0 done, 1 refused (the store or the row said no),
2 could not run (a usage error, no store, a store that cannot be reached).

Every verb path takes `-h` and `--help` (and `nova-config help <verb path>`) and prints its usage, exit 0. A usage error
(an unknown flag or verb, a missing required flag, all of them named at once) is one line on stderr, exit 2, and names the
kind's fields: `nova-config machine add: missing required flag --user, --actor; machine fields: name, address, user, os,
arch, cores, roles, runners, note; run: nova-config machine add -h`. A refusal is one line and says what to do:

| cause | line |
|---|---|
| the schema was never applied (SQLSTATE 42P01, 42703, 3F000) | `store has no current schema; run: nova-config schema \| psql "$NOVA_PG_DSN"` (exit 2) |
| a service names no machine | `no machine <x>; the machines are a,b,c` |
| a machine still runs services | `machine <x> still runs services l1,l2; remove them first: nova-config service remove l1 --actor <you>` |
| a row exists | `machine <x> exists; use: nova-config machine set` |
| a row is not found | `machine <x> not found; run: nova-config machine list` |
| postgres cannot be reached | `cannot reach postgres at <host:port> (NOVA_PG_DSN): connection refused`, `no such host`, `timed out` or `connect failed` (never Go's dial text) (exit 2) |
| no such database (3D000) | `database <x> does not exist on <host>` (exit 2) |
| a refused login (28xxx) | `postgres refused the login for <user>: check NOVA_PG_PASSWORD` (exit 2) |
| a denied grant (42501) | `<user> may not <op> <table>` (exit 2) |
| a bad dsn | `the postgres dsn is wrong: port 99999 out of range` or `not a postgres:// URL`; never the dsn |
| any other server error | `store error (SQLSTATE xxxxx): <the server's first sentence>` (exit 2) |

pgx's multi-line errors and Go type names are never printed. A flag value the row will not take (a bad `--env`, `--log`,
`--cores`, `--roles`, `--argv`, a bad name) is a usage error, exit 2; a row that is valid but wrong against the stored one
(`--every 5` on a kept-alive service) is a refusal, exit 1.

## History

`history [<kind> [<name>]]` reads the `history` table, newest first: `HISTORY at= actor= kind= name= op= rev=`, then the
fields that changed (a set), or all of them (an add or a remove), and the `reason`. `kind` is `machine`, `service` or
`fleet` (a retired kind whose old history still reads). `--since` is a duration back from now (`90m`, `24h`, `7d`) or an RFC 3339 time. `--max` is the rows shown (default 20, 0 for all); `more=true` on the closing `CONFIG HISTORY rows=N` line says rows were left out. There is no `--json`.

## Inventory

`inventory` prints Ansible's dynamic inventory JSON (`--list`, the default, or `--host <name>`). Use it as a
script: `#!/bin/sh` then `exec nova-config inventory "$@"`.

- groups: `all` (every machine), `bench`, `runner` and `services`. **Each group is named exactly as its role** (the
  roles are `bench`, `runner`, `services`; the role and group `services` name what a machine is for, and are not the service rows); `benches` and `runners` are gone, so a play that says `hosts: benches` or
  `hosts: runners` says `bench` or `runner`. `runner` also holds a machine whose `runners` is above 0
- per machine in `_meta.hostvars`: `ansible_host` (address, else the name), `ansible_user`, `runners`, `nova_os`,
  `nova_arch` and `cores` (each left out when unset), `ansible_connection: local` on the machine the inventory
  runs on (`NOVA_MACHINE`, else the host name's first label, lower-cased), and `nova_services`
- `nova_services`: the machine's services, always a list, each with `name`, `argv`, `seat`, `keys` (a list), `every`,
  `keepalive`, `enabled`, `env` (an object, `{}` when none) and `log` (the row's `log`, else
  `~/.local/state/nova/services/<name>.log`)

## Old columns

The `services` table was `loops`. The schema renames it when it finds `config.loops` (and not `config.services`), with its
index and name check; a fresh store and a second run skip that. History rows written before the rename keep their kind
text `loop`; `history service` reads them and prints `service`.

Apply the schema before running this binary against an older store (`nova-config schema | psql "$DSN"`); nothing
migrates on start, and a read of an unmigrated store fails on the missing columns.

An older store has columns this tool no longer knows: on `machines` `seat`, `slots`, `width`, `gates_only`, `tla`;
on the `fleet` table, a kind this tool dropped (nothing read its `pg_dsn`; it is left as it is, unread, on old stores and not created on fresh ones), `store`, `coordinator`, `redis_port`, `bus`, `loops_dir`. They stay, with their values, and are ignored:
every statement names its columns and none reads `*`. They may be dropped by hand later
(`ALTER TABLE config.machines DROP COLUMN seat`, and so on). The schema drops no column. It adds the machine
columns `address`, `os`, `arch` and `cores`, the service columns `env` and `log`, and lifts two constraints that would block work on the new rows: the
foreign keys `fleet_store_fkey` and `fleet_coordinator_fkey` (so a machine the old fleet row names can be removed)
and `NOT NULL` on `machines.seat` (so `machine add` works). The schema is idempotent: run it twice and nothing changes.

An old `roles` value the tool no longer knows (`coordination`) is read, listed and ignored by the inventory. A
`set --roles` checks only the value it is given against `bench`, `runner` and `services`, and replaces the whole
list, so setting roles on such a row drops the old role; leave `--roles` out of a `set` to keep it.
