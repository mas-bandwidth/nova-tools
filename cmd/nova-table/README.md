# nova-table

## What it is

nova-table: tables whose cells are ordered sets, kept in Redis and drawn as text

## Why use it

Track work in tables and live views.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-table@latest
nova-table version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-table).

Run by `cmd/nova-table/firstrun_test.go` on a throwaway redis-server holding
the nova_sprint function library (`cell move` is one call of `ns_oset_move`),
so it runs in the functional tier. The documented lines name no `--redis`: on
a bench the seat's address is the default (`NOVA_SPRINT_REDIS`, then
`NOVA_REDIS_ADDR`, then the seat's row), and the test appends the throwaway
server's. Every value below reproduces; nothing is normalised. The usage
banner's `example:` block is this same sitting, line for line.

```text
$ nova-table create demo --columns ready,working,done
TABLE CREATE table=demo columns=3 trips=1

$ nova-table row add demo build
TABLE ROW ADD table=demo row=build cols=3 bound=0 trips=1

$ nova-table cell add demo build ready b1
TABLE CELL table=demo row=build col=ready n=1 trips=1

$ nova-table cell add demo build ready b2
TABLE CELL table=demo row=build col=ready n=2 trips=1

$ nova-table cell move demo build ready working b1
TABLE MOVE table=demo row=build member=b1 from=ready to=working n=1 trips=1

$ nova-table show demo
TABLE table=demo columns=3 rows=1 trips=1 epoch=0 revision=5
TABLE ROW table=demo row=build ready=1 working=1 done=0

$ nova-table render demo
demo  | ready | working | done
------+-------+---------+-----
build |     1 |       1 |    0
------+-------+---------+-----
      |     1 |       1 |    0
```

## Verbs

The [nova-table section of the command reference](../../docs/CLI.md#nova-table) documents every verb's flags, effect and exit codes.

- `create`
- `help`
- `set`
- `drop`
- `list`
- `row`
- `col`
- `cell`
- `member`
- `batch`
- `check`
- `clear`
- `show`
- `render`
- `watch`
- `view`
- `shell`
- `version`

## Spec

The contract is [docs/SPEC-NOVA-TABLE.md](../../docs/SPEC-NOVA-TABLE.md).
