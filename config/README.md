# nova-config

nova-config keeps your machines, the programs they run, and the friends (the
AIs that work on the fleet) as rows in a PostgreSQL database, with a history row
for every change. It prints the machines and programs as an Ansible inventory
that plays can use, and Go programs read the same rows through its library.

## Install

```sh
git clone -q https://github.com/mas-bandwidth/nova-tools
go build -C nova-tools -o "$PWD/bin/nova-config" ./config
export PATH="$PWD/bin:$PATH"
```

## Set up a database

Pass the connection string (DSN) with `--pg <dsn>` or `NOVA_PG_DSN`. Put the
password in `NOVA_PG_PASSWORD`, or in the variable `NOVA_PG_PASSWORD_ENV` names,
never in the DSN. `nova-config schema` prints the SQL to apply with psql
(`nova-config schema | PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN"`), connected as the
database owner: the SQL creates schema `config`, so a role that owns only that schema (`nova_config` in a
fleet) is refused with `permission denied for database`.

This block starts a throwaway server on port 54329 (it needs `initdb`,
`pg_ctl` and `createdb` on PATH). Run all the blocks in one shell.

```sh
work=$(mktemp -d)
initdb -D "$work/pg" -U nova -A trust >/dev/null
pg_ctl -D "$work/pg" -o "-p 54329 -k $work -c listen_addresses=127.0.0.1" -l "$work/pg.log" -w start
createdb -h 127.0.0.1 -p 54329 -U nova nova
export NOVA_PG_DSN=postgres://nova@127.0.0.1:54329/nova
nova-config schema | PGOPTIONS="-c client_min_messages=warning" psql -q "$NOVA_PG_DSN"
```

## Machines

Verbs: `list`, `show`, `add`, `set`,
`remove`. A write takes `--actor <name>` (default `NOVA_ACTOR`, else login name); `--reason <line>` and `--dry-run` are
optional (`--dry-run` prints `CONFIG DRY-RUN ... wrote=nothing` and the changes). Names use lower-case letters, digits and dashes.

| Field | Meaning |
|---|---|
| `user` | SSH login user; required on add. |
| `address` | SSH address (the tailnet/MagicDNS address Ansible and the fleet use); the machine name is used when empty. |
| `public` | Public address for services that face the internet: an IP literal or a DNS name of lower-case labels. A tailnet (100.64.0.0/10), private, loopback or link-local address is refused; `--public ''` unsets it. |
| `os`, `arch`, `cores` | Facts you set; `--cores default` unsets it. |
| `runners`, `note` | CI runner count (default 0); one line of notes. |
| `roles` | Comma-separated `bench`, `runner`, `services`; default `bench`. |

```sh
nova-config machine add bench-a --user nova --os linux --arch amd64 --cores 16 --roles bench,services --actor ops --reason 'First machine'
nova-config machine set bench-a --note 'Example host' --actor ops
nova-config machine set bench-a --note 'Example host' --actor ops
nova-config machine show bench-a
```

```text
CONFIG ADD kind=machine name=bench-a rev=1
next: nova-fleet join bench-a
CONFIG SET kind=machine name=bench-a rev=2 changed=note
CONFIG SET kind=machine name=bench-a changed=none rev=unchanged
MACHINE name=bench-a address=- public=- user=nova os=linux arch=amd64 cores=16 roles=bench,services runners=0 note="Example host" created=2026-11-05T23:51:58Z updated=2026-11-05T23:51:58Z services=-
```

`rev` is the history ID; `changed=` lists only the fields whose value differs.
The repeated `set` changed nothing, so it wrote nothing. Empty values print as
`-`; times vary.

## Services

A service is a program on a machine, run every N seconds or kept running. Set
`--every <seconds>` or `--keepalive true`, one of them. Same verbs as machines.
A service never fetches on a clock: an argv that names a fetcher (`git fetch|pull|clone`,
`rsync`, `curl`, `wget`, `mirror-refresh`) with a loop (`--loop`, `--every`, `watch`, `while`),
or with `--every` above 0, is refused. Fetch on an event (a push) or once per job.

| Field | Meaning |
|---|---|
| `machine` | An existing machine; required. |
| `argv` | JSON array of strings, program first; required. |
| `every`, `keepalive` | Interval in seconds (0 means none); keep it running, default false. |
| `enabled`, `env` | Default true; JSON object of string values, default `{}`. |
| `log` | Absolute path or `~/...`; empty uses `~/.local/state/nova/services/<name>.log` in the inventory. |
| `seat`, `keys` | For example a nova-secrets seat and secret names (never values) to hand the program. `keys` needs `seat`. Config only records them. |

```sh
nova-config service add hello --machine bench-a --argv '["/usr/bin/printf","hello\n"]' --every 60 --env '{"LANG":"C"}' --actor ops
nova-config service show hello
```

```text
CONFIG ADD kind=service name=hello rev=3
next: nova-fleet services --check
SERVICE name=hello machine=bench-a argv=["/usr/bin/printf","hello\n"] seat=- keys=- every=60 keepalive=false enabled=true env={"LANG":"C"} log=- created=2026-11-05T23:51:58Z updated=2026-11-05T23:51:58Z
```

Config starts nothing; the rows are data for your own tooling. `--json` on
`list`, `show` or `history` prints one JSON object (`result`, `facts`, `items`).

## Friends

A friend is an AI that works on the fleet, named by its bus name. Same verbs as
machines.

| Field | Meaning |
|---|---|
| `harness` | What it runs in: `claude`, `codex`, `antigravity`, `grok`, `muse`, ...; required. |
| `model`, `note` | A label; one line of notes. |
| `width` | How many cards it works at once; default 1, at least 1. |
| `tiers` | Comma-separated `flash`, `pro`, `heavy`, `frontier`; default all four, at least one. |
| `pronouns` | Text; empty (stored as NULL) when unstated. |
| `enabled` | Default true. |

```text
nova-config friend add <friend> --harness <harness> --model <model> --width 4 --tiers pro,heavy --pronouns she/her --actor ops
nova-config friend show <friend>
```

```text
CONFIG ADD kind=friend name=<friend> rev=4
FRIEND name=<friend> harness=<harness> model=<model> width=4 tiers=heavy,pro pronouns=she/her note=- enabled=true created=2026-11-07T01:55:27Z updated=2026-11-07T01:55:27Z
```

A store with the older friends table (columns such as `slots`, `mode` and
`session`) keeps it: `nova-config schema` adds `harness`, `model`, `note` and
`enabled`, and nova-config never reads or changes the older columns.

## Reading the rows from Go

Package `github.com/mas-bandwidth/nova-tools/v2/config/lib` (package `config`)
reads the rows through the same query the `list` verbs run: `Open(ctx, dsn,
password)`, then `Machines`, `Friends` or `Services` on the `*Store`. The row
types carry the field names `--json` prints. It only reads; write through
nova-config, which keeps the history.

```go
st, err := config.Open(ctx, os.Getenv("NOVA_PG_DSN"), os.Getenv("NOVA_PG_PASSWORD"))
if err != nil {
	return err
}
defer st.Close()
friends, err := st.Friends(ctx) // []config.Friend, in name order
```

## History

```sh
nova-config history machine bench-a --max 1
```

```text
HISTORY at=2026-11-05T23:51:58Z actor=ops kind=machine name=bench-a op=set rev=2
  note: "" -> "Example host"
CONFIG HISTORY rows=1 more=true
```

Filter by `machine`, `service` or `friend`, then by name. `--since` takes `90m`, `7d` or
an RFC 3339 time. `--max` defaults to 20; 0 means all. `more=true` means the cap
left some writes out.

## Inventory

```sh
nova-config inventory --host bench-a
```

`--list` (the default) prints Ansible JSON: every machine is in `all`, and its
roles put it in `bench` or `services`. A `runner` role or a positive runner
count adds `runner`. `--host <name>` prints one host's variables:
`ansible_host`, `ansible_user`, `runners`, `nova_services` (always a list of the
service fields) and, when set, `nova_os`, `nova_arch`, `nova_public` and `cores`.
`argv`, `env` values and `log` are printed as `{"__ansible_unsafe": ...}`, so
Ansible reads them as data and never as a template. `NOVA_MACHINE` names the local host (else the first label of the hostname,
lower-cased), which gets `ansible_connection: local`. As an Ansible script, use
a file containing `exec nova-config inventory "$@"`.

## Refusals

Exit 0 is done (and `-h`), 1 a refused row operation, 2 a bad invocation or
connection failure. Missing or invalid flags and the store DSN are checked together before connecting.

| You see | Do this |
|---|---|
| `machine add: machine bench-a exists; use: nova-config machine set bench-a` (1) | Use `set`. |
| `machine remove: machine bench-a still runs services hello; ...` (1) | Remove or move its services first. |
| `service add: service x has neither --every nor --keepalive; ...` (2) | Give exactly one of them. |
| `service add: service x names secrets (--keys A) and no --seat ...` (2) | Add `--seat` or drop `--keys`. |
| `service add: service x fetches on a clock (git pull with --loop); fetch on an event (a push) or once per job, never on a clock ...` (2; `set` 1) | Drop the loop flag and `--every`; run the fetch from the job or the push hook that needs it. |
| `friend set: friend <friend> has --width 0; ...` (1) | Give `--width 1` or more. |
| `friend remove: friend <friend> is still named by config.sprint; ...` (1) | Change the row that names it first. |
| `machine list: NOVA_PG_DSN carries a password; ...` (2) | Move the password to `NOVA_PG_PASSWORD`. |
| `inventory: takes --list or --host <name>, not both; ...` (2) | Give one. |
| `inventory: no machine row named nope; ...` or `NOVA_MACHINE nope names no machine row` (1) | Check the name. |
| `inventory: service x has corrupt argv (...); run: nova-config service set x --argv <value>` (1) | Fix the corrupt field. |
| Connection, login or missing-table error (2) | Follow the `psql`, `createdb` or `nova-config schema` command printed on the line. |

## Guarantees and limits

- A row write and its history row happen in one transaction.
- A `set` that changes nothing writes no history.
- Every command gives up after 30 seconds.

Clean up:

```text
nova-config friend remove <friend> --actor ops
```

```sh
nova-config service remove hello --actor ops
nova-config machine remove bench-a --actor ops
pg_ctl -D "$work/pg" -m fast stop
rm -rf "$work"
```

How the tool is tested is in [SPEC.md](SPEC.md).
