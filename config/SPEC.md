# nova-config: tests and models

The [guide](README.md) is how to use the tool. This page is for changing it.
Build and test commands for every tool are in [TESTING.md](../docs/TESTING.md).

Unit tests use an in-memory store. Functional tests start a
throwaway PostgreSQL and skip when `initdb` is absent. They check that a row
and its history are written in one transaction. The schema file also upgrades
an older store in place; `TestFunctionalOldColumns` covers that, and applying
the schema twice must change nothing. Config has no model: a write is one
PostgreSQL transaction.
