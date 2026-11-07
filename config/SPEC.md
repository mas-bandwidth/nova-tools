# nova-config: tests and models

The [guide](README.md) is how to use the tool. This page is for changing it.
Build and test commands for every tool are in [TESTING.md](../docs/TESTING.md).

Unit tests use an in-memory store. Functional tests start a
throwaway PostgreSQL and skip when `initdb` is absent. They check that a row
and its history are written in one transaction. The schema file also upgrades
an older store in place; `TestFunctionalOldColumns` covers that, and applying
the schema twice must change nothing. `TestFunctionalV1Friends` does the same
for the v1 friends table of the live store (`testdata/v1-friends.sql`), and
`TestFunctionalLibrary` reads what the CLI wrote through `config/lib`.
`TestKindsAreTheLibraryColumns` keeps the kinds' fields and the library's row
types the same columns in the same order. Config has no model: a write is one
PostgreSQL transaction.
