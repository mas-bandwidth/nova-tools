# nova-config

## What it is

nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis

## Why use it

Keep fleet configuration durable.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-config@latest
nova-config version
```

## First run

The transcript below is copied from the first run in [docs/TESTS.md](../../docs/TESTS.md#nova-config), which `cmd/nova-config/firstrun_test.go` executes line for line; a docs test holds this copy to that record.

No database: the first run keeps its rows in `./try.json` (`--file`), the
same kinds, refusals and history as PostgreSQL, and
`cmd/nova-config/firstrun_test.go` runs each `$` line in `t.TempDir()`. The
history's `at=` is the instant of the run, the one value that differs on a
second run. The real runs need a Postgres (`nova-config migrate`) and a Redis
(`nova-config apply`); `docs/nova-config/README.md` walks them, and
`cmd/nova-config/config_functional_test.go` runs them against a throwaway
Postgres and a throwaway Redis.

```text
$ nova-config migrate --file try.json
CONFIG MIGRATE file=try.json from=0 to=37 applied=37

$ nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --actor a1 --file try.json
CONFIG ADD kind=machine name=m1 rev=1

$ nova-config machine set m1 --width 6 --actor a1 --file try.json
CONFIG SET kind=machine name=m1 rev=2 changed=width

$ nova-config machine list --file try.json
MACHINE name=m1 user=nova seat=s1 slots=8 runners=0 width=6 tla=false harnesses=opencode note=-
CONFIG LIST kind=machine rows=1

$ nova-config machine history m1 --file try.json
HISTORY id=1 kind=machine name=m1 op=add actor=a1 at=2026-10-02T03:18:20Z harnesses=opencode note=- runners=0 seat=s1 slots=8 tla=false user=nova width=4
HISTORY id=2 kind=machine name=m1 op=set actor=a1 at=2026-10-02T03:18:20Z width=4>6
CONFIG HISTORY kind=machine name=m1 changes=2
```

## Verbs

The [nova-config section of the command reference](../../docs/CLI.md#nova-config) documents every verb's flags, effect and exit codes.

- `help`
- `version`
- `kinds`
- `migrate`
- `status`
- `apply`
- `inventory`
- `machine`
- `login`
- `logout`
- `fleet`
- `sprint`

## Spec

The contract is [docs/SPEC-CONFIG.md](../../docs/SPEC-CONFIG.md).
