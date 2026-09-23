// The Postgres driver registration.
//
// `PGDriverName` was "postgres" and nothing in any binary that opens a
// decisions table registered it. pgx's database/sql shim registers "pgx", and
// it was imported by internal/record alone; cmd/nova-decide does not import
// internal/record, so a postgres:// DSN failed at sql.Open with an unknown
// driver before it ever reached a query. That is the second half of why the
// Postgres path was unreachable, and why the TSV repair in #1925 stood
// independently of it (Stella, 2026-09-19).
//
// The import is here, alone in its own file, so what it is for is impossible
// to mistake for an accident: a blank import in a file full of other things is
// the kind of line a tidy-up deletes.
package decide

import (
	// Registers the "pgx" database/sql driver that PGDriverName names.
	_ "github.com/jackc/pgx/v5/stdlib"
)
