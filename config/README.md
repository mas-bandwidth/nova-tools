# nova-config

nova-config keeps a fleet's machines and services as rows in PostgreSQL, every write with a history row, and prints
them as an Ansible dynamic inventory. The tables, verbs and inventory format are in `docs/SPEC-CONFIG.md`;
`nova-config help` prints the verbs.

In the inventory, argv, env values and log are printed marked unsafe, so Ansible never templates them.

## Schema

`nova-config schema` prints the embedded `schema.sql`; a fresh or older store is made current with
`nova-config schema | psql "$DSN"`. Apply it before running this binary against an older store: nothing migrates
on start, and a read fails on the columns the old store lacks. It drops no column.

## Build and test

```
go build -o nova-config .
go test .                      # unit tests, an in-memory store
go test -tags functional .     # a PostgreSQL of the test's own (skipped without initdb)
```

There is no state machine here: each write is one row and one history row in one transaction, so there is no
TLA+ model.
