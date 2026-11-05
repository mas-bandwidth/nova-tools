# nova-config

nova-config keeps a fleet's machines and loops as rows in PostgreSQL, every write with a history row, and prints
them as an Ansible dynamic inventory. It knows machine, loop and fleet, and nothing else: no Redis, no scheduler,
no reports from the machines. A fact about a machine is a field someone set.

## Tables

All in the schema `config` (`config/schema.sql`; a fresh store: `psql <dsn> -f schema.sql`). Every kind has
`name`, `created_at`, `updated_at` besides the fields below.

| table | fields |
|---|---|
| `machines` | `user` (required, the ssh user), `address` (what ssh dials; else the name), `os`, `arch`, `cores` (`default` unsets it), `runners` (CI runners on it), `roles` (comma list of `bench`, `runner`, `services`; default `bench`), `note` |
| `loops` | `machine` (a machine row), `argv` (JSON array, the program first), `seat` and `keys` (the names a unit opens its secrets by; never values; `keys` needs `seat`), `every` (seconds) or `keepalive true`, `enabled` (default true) |
| `fleet` | the one row, named `fleet`: `pg_dsn` (no password) |
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
nova-config inventory [--list | --host <name>]
nova-config help
```

```
$ nova-config machine add m1 --user nova --os linux --arch amd64 --cores 16 --roles bench,services --actor a1
CONFIG ADD kind=machine name=m1 rev=1
$ nova-config loop add r-m1 --machine m1 --argv '["prog","--flag"]' --every 60 --actor a1 --reason "first"
CONFIG ADD kind=loop name=r-m1 rev=2
$ nova-config loop set r-m1 --enabled false --actor a1 --dry-run
CONFIG DRY-RUN op=set kind=loop name=r-m1 actor=a1 wrote=nothing enabled=true>false
$ nova-config machine list
MACHINE name=m1 user=nova address=- os=linux arch=amd64 cores=16 runners=0 roles=bench,services note=-
CONFIG LIST kind=machine rows=1
```

A write is one transaction, the row and its history row; the printed `rev` is the history id. A value is one token
(spaces, `=` and control characters as `\xNN`; `-` when empty). `show` adds `created=`, `updated=` and, for a
machine, `loops=`. The store is `--pg <dsn>` or `NOVA_PG_DSN` (no password in it); the password comes from the
variable `NOVA_PG_PASSWORD_ENV` names, else `NOVA_PG_PASSWORD`. Exit codes: 0 done, 1 refused, 2 could not run.

## Inventory

`inventory` prints Ansible's dynamic inventory JSON (`--list`, the default, or `--host <name>`). Use it as a
script: `#!/bin/sh` then `exec nova-config inventory "$@"`.

- groups: `all` (every machine), `benches`, `services` (by role) and `runners` (role `runner`, or `runners` above 0)
- `all.vars.nova_pg_dsn`: the fleet row's `pg_dsn`, when set
- per machine in `_meta.hostvars`: `ansible_host` (address, else the name), `ansible_user`, `runners`, `nova_os`,
  `nova_arch` and `cores` (each left out when unset), `ansible_connection: local` on the machine the inventory
  runs on (`NOVA_MACHINE`, else the host name's first label, lower-cased), and `nova_loops`
- `nova_loops`: the machine's loops, always a list, each with `name`, `argv`, `seat`, `keys` (a list), `every`,
  `keepalive`, `enabled` and `log` (`~/nova-bench/loops/<name>.log`)

## Old columns

An older store has columns this tool no longer knows: on `machines` `seat`, `slots`, `width`, `gates_only`, `tla`;
on `fleet` `store`, `coordinator`, `redis_port`, `bus`, `loops_dir`. They are ignored: every statement names its
columns and none reads `*`, so they are neither read nor written, and they may be dropped by hand later
(`ALTER TABLE config.machines DROP COLUMN seat`, and so on). `schema.sql` drops nothing; run on an older store it
only adds the machine columns `address`, `os`, `arch` and `cores`. Until an old column that is `NOT NULL` without a
default (`machines.seat`) is dropped or given one, `machine add` is refused by the store; `set`, `remove`, `list`
and `show` are not affected.
