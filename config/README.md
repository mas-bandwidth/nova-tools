# nova-config

nova-config keeps a fleet's machines and loops as rows in PostgreSQL, every write with a history row, and prints
them as an Ansible dynamic inventory. The tables, verbs and inventory format are in `docs/SPEC-CONFIG.md`;
`nova-config help` prints the verbs.

## Build and test

```
go build -o nova-config .
go test .                      # unit tests, an in-memory store
go test -tags functional .     # a PostgreSQL of the test's own (skipped without initdb)
```

There is no state machine here: each write is one row and one history row in one transaction, so there is no
TLA+ model.
