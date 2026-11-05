# nova-config

nova-config keeps a fleet's machines and loops as rows in PostgreSQL, every write with a history row, and prints
them as an Ansible dynamic inventory.

## Rows

```
nova-config machine|loop list [--json]
nova-config machine|loop show <name> [--json]
nova-config machine|loop add <name> --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
nova-config machine|loop set <name> --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
nova-config machine|loop remove <name> --actor <name> [--reason <line>] [--dry-run]
nova-config fleet show [--json]
nova-config fleet set --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
```

| kind | fields |
|---|---|
| `machine` | named by its tailnet host; `user`, `seat`, `slots` (required), `runners`, `roles` (bench, coordination, runner, services), `width` (`default` unsets it), `gates_only`, `tla`, `note` |
| `loop` | a command one machine runs under launchd or systemd: `machine`, `argv` (a JSON array, required), `seat`, `keys` (secret names, never values), `every` (seconds) or `keepalive true`, `enabled` |
| `fleet` | the one fleet row: `store` and `coordinator` (machine names), `redis_port`, `pg_dsn` (no password), `bus`, `loops_dir` |

A list prints one line per row, `KIND name=... field=value ...`, then `CONFIG LIST kind=<kind> rows=<n>`; a value
is one token (spaces, `=` and control characters as `\xNN`; `-` when empty); a list cuts a note at 60 characters.
show adds `created=` and `updated=`, and a machine's `loops=`. A write is one transaction, the row and a history row
(`config.history`: kind, name, op, before, after, actor, at; after carries the reason), and prints
`CONFIG ADD|SET|REMOVE kind=<kind> name=<name> rev=<history id>`. `--dry-run` prints the change and writes nothing.

## Inventory

```
nova-config inventory [--list | --host <name>] [--redis <host:port>]
```

prints every machine in `all` and in the groups `adopt` (the first machine that is not the coordinator first, the
canary), `benches`, `coordinator`, `store` (from roles, which must match the fleet row), `store_deployer`,
`runners` and `tla`, with each machine's row, its loops (`nova_loops`) and the fleet's endpoints under
`_meta.hostvars`. With a Redis named it adds what each machine reports of itself (`nova_os`, `nova_arch`,
`cores`); a machine without a report gets a NOTE line on stderr. Ansible runs it as an inventory script:

```
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > inventory && chmod +x inventory
ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-playbook -i ./inventory site.yml
```

## Environment

| Variable | |
|---|---|
| `NOVA_PG_DSN` | the store, `postgres://user@host:port/db` with no password (or `--pg`) |
| `NOVA_PG_PASSWORD_ENV` | the name of the variable that holds the password (default `NOVA_PG_PASSWORD`) |
| `NOVA_SPRINT_REDIS`, `NOVA_REDIS_ADDR` | the Redis the inventory reads reports from (or `--redis`) |
| `NOVA_SPRINT_REDIS_USER`, `NOVA_SPRINT_REDIS_PASSWORD_ENV` | its login: the user, and the name of the variable that holds the password |
| `NOVA_MACHINE` | the machine the inventory runs on (`ansible_connection: local`); else the host name's first label |

Exit codes: 0 done, 1 refused, 2 could not run. A fresh store: `psql <dsn> -f schema.sql`.

## Build and test

```
go build -o nova-config .
go test .                      # unit tests, an in-memory store
go test -tags functional .     # a PostgreSQL and a redis-server of the test's own (skipped without initdb)
```

There is no state machine here: each write is one row and one history row in one transaction, so there is no
TLA+ model.
