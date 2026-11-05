# nova-config

Keep machine settings and recurring commands in PostgreSQL. Each write saves a
history row in the same transaction. The Ansible inventory is a list of hosts and their variables, ready for
Ansible plays to use.
The tool reads no Redis state and collects no facts from hosts.

A machine can keep a program running or run it every N seconds. Config calls
that program a **service**. Its row holds the program, arguments, environment,
log path and choice of continuous or scheduled running.

## Connect and create the schema

The DSN is the database connection string. Select it with `--pg <dsn>` or
`NOVA_PG_DSN`. Keep passwords out of
the DSN. Supply the password through `NOVA_PG_PASSWORD`, or name another
variable with `NOVA_PG_PASSWORD_ENV`.

```sh
export NOVA_PG_DSN=postgres://nova@db-host:5432/nova
nova-config schema | psql "$NOVA_PG_DSN"
```

`schema` prints the embedded [SQL](../config/schema.sql). psql uses its own
login settings; the nova-config password variable does not configure psql.
Apply the schema before using a fresh or older database. The binary does not
migrate it on startup.

## Commands

```text
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

A name uses lower-case letters, digits and dashes, starts with a letter or
digit, and has at most 128 characters. Give each write an `--actor`. Use
`--reason` to explain it in one line. `--dry-run` reads and checks the proposed
change, then prints it without writing.

## Machines

Set facts yourself. The tool does not discover an address, OS or core count.
Use `address` when the machine's name differs from its SSH address.

| Field | Meaning |
|---|---|
| `user` | SSH login user; required on add. |
| `address` | SSH host or address; the machine name is used when empty. |
| `os`, `arch` | OS and architecture labels. |
| `cores` | Non-negative core count; `default` unsets it. |
| `runners` | Non-negative CI runner count; default 0. |
| `roles` | Comma-separated `bench`, `runner`, `services`; default `bench`. |
| `note` | One line of notes. |

```sh
nova-config machine add bench-a --user nova --os linux --arch amd64 --cores 16 --roles bench,services --actor ops
nova-config machine list
```

## Services

Give a service a machine and a command. Choose either a positive `every` interval
or `keepalive true`. The tool refuses both together, or neither.

| Field | Meaning |
|---|---|
| `machine` | Existing machine row; required. |
| `argv` | JSON array of strings, with the program first; required. |
| `seat` | Named decryption identity to use when the command needs secrets. |
| `keys` | Comma-separated secret names, never values; requires `seat`. |
| `every` | Non-negative interval in seconds; 0 disables periodic runs. |
| `keepalive` | Keep the command running; default false. |
| `enabled` | Enable the service; default true. |
| `env` | JSON object of environment names and string values; default `{}`. |
| `log` | Absolute path or path starting with `~/`; empty uses the inventory default. |

```sh
nova-config service add hello --machine bench-a --argv '["/usr/bin/printf","hello\n"]' --every 60 --env '{"LANG":"C"}' --log '~/.local/state/nova/services/hello.log' --actor ops
nova-config service set hello --enabled false --actor ops --dry-run
```

Environment names use upper-case letters, digits and underscores, and cannot
start with a digit. Values are strings.

The [fleet plays](FLEET.md#running-programs) turn these rows into units.
Config itself starts no command.

## Read, edit and remove the example rows

These commands continue the `bench-a` and `hello` examples above. They change
settings in PostgreSQL; they do not apply units to machines.

```sh
nova-config machine set bench-a --address bench-a --runners 1 --note 'Example host' --actor ops --reason 'Record connection and CI settings'
nova-config machine show bench-a --json
nova-config machine list --json
nova-config service list --json
nova-config service show hello
nova-config service set hello --every 0 --keepalive true --seat web --keys TOKEN --enabled true --actor ops --reason 'Keep the example running'
nova-config service set hello --enabled false --actor ops --dry-run
nova-config inventory --host bench-a
nova-config history service hello --max 0
nova-config help service add
```

A **seat** is the named decryption identity used by nova-secrets. `keys` lists
the secret names to give the program, not their values. Config records those
settings without opening the encrypted store. When you no longer need these
example rows, remove the service before the machine:

```sh
nova-config service remove hello --actor ops --reason 'Finish the example'
nova-config machine remove bench-a --actor ops --reason 'Finish the example'
```

Every database operation accepts `--pg` instead of `NOVA_PG_DSN`, for example
`nova-config machine list --pg "$NOVA_PG_DSN"`. Writes accept `--actor`,
`--reason` and `--dry-run`; reads do not write history.

## History

Read recent changes, newest first:

```sh
nova-config history
nova-config history service hello --since 7d --max 10
```

Filter by `machine` or `service`, then optionally by name. `--since` accepts a
positive duration such as `90m`, `24h` or `7d`, or an RFC 3339 time. `--max`
defaults to 20; use 0 for all matching writes. The closing line includes
`more=true` when the limit leaves writes out.

Each entry names the time, actor, kind, name, operation and history ID. The
following lines show added or removed values, or the old and new values of
changed fields. A supplied reason follows them. `history fleet` reads writes
left by the retired fleet interface. `history service` also includes older
entries whose recorded kind is `loop`; output labels them `service`.

Machine and service rows have `name`, `created_at` and `updated_at`. The tables
live in the PostgreSQL schema `config`. Every write adds a history row in the
same transaction, with `kind`, `name`, `op`, `before`, `after`, `actor` and `at`.
`op` is `add`, `set` or `remove`. The before and after objects hold field values
as text; the reason is included on the written side.

## Output and exits

A successful write prints its history ID as `rev`. For example, a fresh store
prints:

```text
CONFIG ADD kind=machine name=bench-a rev=1
```

List and show output use `key=value` fields. Empty values print as `-`.
Values with spaces or control characters use JSON string quoting. Notes and
reasons are JSON strings; command arguments and environment objects print as
JSON. `show` adds creation and update times; machine output also lists its
services. List output shortens long notes to 60 characters followed by `...`.

Add `--json` to `list` or `show` for a JSON object with `result`, `facts` and
`items`. Field values retain their types: numbers, booleans, argument arrays
and environment objects. An unset core count is `null`. History has text
output only. A dry run prints the proposed operation and its field changes.

| Output | Meaning |
|---|---|
| `MACHINE` or `SERVICE` | One row, with fields from the tables above. Show includes timestamps; a machine show also names its services. |
| `CONFIG LIST kind=... rows=...` | The list's row count, including zero. |
| `CONFIG ADD`, `SET` or `REMOVE` | The write completed; `rev` is its history ID. `SET changed=` lists the supplied fields. |
| `CONFIG DRY-RUN ... wrote=nothing` | The proposed operation and actor, followed by field values or old-to-new changes. No row or history was written. |
| `HISTORY ...` | One stored write, followed by its field changes and reason. |
| `CONFIG HISTORY rows=... more=true` | Number of writes shown; `more` appears only when the cap left some out. |
| Inventory JSON | Host groups and host variables; it is separate from list/show JSON. |
| Schema SQL | The schema statements to apply through psql; printing them changes no database. |

Root and verb help accept `-h` or `--help` and return 0. A malformed command
returns 2 with a usage hint. Row conflicts and incompatible settings return
1. Connection, login and missing-schema failures return 2.

| Exit | Meaning |
|---|---|
| 0 | Done. |
| 1 | A row or store operation was refused. |
| 2 | The invocation or execution failed. |

## Inventory

Create an executable inventory script:

```sh
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > nova-inventory
chmod +x nova-inventory
./nova-inventory --list
```

`--list` is the default. `--host <name>` prints one host's variables.

Every machine appears in `all`. Roles add it to `bench` or `services`.
A `runner` role or a positive runner count adds it to `runner`.

Each host gets `ansible_host`, `ansible_user`, `runners` and `nova_services`.
Set OS, architecture and core-count values appear as `nova_os`, `nova_arch`
and `cores`; unset values are left out. `NOVA_MACHINE` identifies the local
host. Without it, the tool uses the first label of the hostname, lower-cased.
That host gets `ansible_connection: local`.

`nova_services` is always a list, including when empty. Each entry has `name`,
`argv`, `seat`, `keys`, `every`, `keepalive`, `enabled`, `env` and `log`. Keys
are a list of names and `env` is an object. An empty log setting becomes
`~/.local/state/nova/services/<name>.log`.

## Recover from a refusal

| Problem | Next step |
|---|---|
| Missing flags, bad field values, unknown kind or verb | Supply the inputs named together in stderr. Use the verb's `-h` for its fields and types. |
| No database selected | Set `NOVA_PG_DSN` or pass `--pg`. Keep the password in the named password variable. |
| Connection or login failed | Check the DSN, host availability and login. psql uses its own password settings. |
| Missing schema or column | Apply `nova-config schema` through psql, then retry. The binary does not migrate on startup. |
| Row already exists | Read it with `show`, then use `set` for a change. |
| Row or referenced machine is absent | Check `machine list` or `service list`; add the intended machine before its service. |
| Machine still has services | Move those services with `service set --machine`, or remove them before removing the machine. |
| Both scheduling modes, or neither | Choose positive `--every` with `--keepalive false`, or `--every 0 --keepalive true`. |
| Secret names without a seat | Add the intended `--seat` or clear `--keys`. |
| Store write refused | Read the named constraint or operation, inspect the row, then retry the corrected change. A successful row write and its history share one transaction. |

## Old columns

Apply `nova-config schema | psql "$NOVA_PG_DSN"` before using an older store.
A read against an unmigrated store fails on its missing table or columns.

The schema renames `config.loops` to `config.services` when the old table
exists and the new one does not. Its index and constraints named `loops_*` are renamed
too; existing history is preserved.

The schema adds `address`, `os`, `arch` and `cores` to machines, and `env`
and `log` to services. It removes
`NOT NULL` from the old machine `seat` column so new rows can be added.
It also removes `fleet_store_fkey` and `fleet_coordinator_fkey`, which would
otherwise prevent removal of machines named by the old fleet row.

No column or stored value is dropped. The old machine fields `seat`, `slots`,
`width`, `gates_only` and `tla` remain but are ignored. The old fleet
table remains unread; a fresh schema does not create it. SQL statements
name the columns they use. You can drop unused columns separately. Applying
the schema again makes no further change.

An old `coordination` role is still shown but adds no inventory group.
`set --roles` replaces the whole list and accepts only current roles.
Omit `--roles` from a set to preserve an old value.
