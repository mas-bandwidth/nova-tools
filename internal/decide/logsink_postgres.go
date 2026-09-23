package decide

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"time"

	// The Postgres driver is the one #1307 linked for the card results
	// (internal/record), so the decision log and the card results are opened by
	// the same driver against the same database.
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var logMigrationFS embed.FS

// LogSchemaVersion is the migration version the files under migrations/ install.
// Bump it when a migration changes the shape.
const LogSchemaVersion = 1

// logDriverName is the database/sql driver the pgx stdlib registers.
const logDriverName = "pgx"

// PostgresLogSink is the decision and escalation log as a table, beside the
// card results in the same database (docs/SPEC-STATE.md). One pool, one writer,
// append-only: a decision is a row and nothing rewrites one.
type PostgresLogSink struct {
	db *sql.DB
}

// OpenPostgresLog connects and verifies the server answers. The Ping is the one
// network call and it is made by the tool, never by a unit test: the unit tests
// run on FakeLogSink and the tagged integration test is what keeps it honest.
func OpenPostgresLog(dsn string) (*PostgresLogSink, error) {
	db, err := sql.Open(logDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("decide: open the decision log: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("decide: the decision log at %s did not answer: %w", RedactDSN(dsn), err)
	}
	return &PostgresLogSink{db: db}, nil
}

// Migrate applies every migration file in name order, in one transaction, and
// records the version. Running it twice is a no-op, which is what makes
// `nova-decide log migrate` safe to run on every start.
func (p *PostgresLogSink) Migrate(ctx context.Context) error {
	stmts, err := logMigrations()
	if err != nil {
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("decide: migrate the decision log: %w", err)
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("decide: migrate the decision log: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("decide: migrate the decision log: %w", err)
	}
	return nil
}

// Append writes one decision. It is the same row the JSONL sink writes, in the
// table's own columns, with an unreported token counter as SQL NULL.
func (p *PostgresLogSink) Append(e Entry) error {
	row, err := logRowFor(e)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := p.db.ExecContext(ctx, insertLog(), row.args()...); err != nil {
		return fmt.Errorf("decide: append to the decision log: %w", err)
	}
	return nil
}

// Entries reads the rows back in append order, which is the order the JSONL log
// has, so the summary over the table is the summary over the file.
func (p *PostgresLogSink) Entries() ([]Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, selectLog())
	if err != nil {
		return nil, fmt.Errorf("decide: read the decision log: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var r logRow
		if err := rows.Scan(r.scanTargets()...); err != nil {
			return nil, fmt.Errorf("decide: read the decision log: %w", err)
		}
		e, err := r.entry()
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("decide: read the decision log: %w", err)
	}
	return out, nil
}

// Close releases the pool.
func (p *PostgresLogSink) Close() error { return p.db.Close() }

// logMigrations reads the embedded migration files in name order and splits
// them into the statements the transaction applies.
func logMigrations() ([]string, error) {
	entries, err := logMigrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("decide: read the migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	var out []string
	for _, name := range sortedNames(names) {
		raw, err := logMigrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("decide: read the migration %s: %w", name, err)
		}
		out = append(out, splitLogSQL(string(raw))...)
	}
	return out, nil
}

// splitLogSQL strips line comments and splits a script on semicolons. The
// migrations hold no string literal containing a semicolon, which is the one
// case this would have to know about; keeping the SQL simple is cheaper than
// trusting a parser (the same rule internal/record's schema follows).
func splitLogSQL(script string) []string {
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		lines = append(lines, line)
	}
	var out []string
	for _, part := range strings.Split(strings.Join(lines, "\n"), ";") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// RedactDSN keeps a password out of a line the tool prints. A DSN may be a URL
// with user:password@; the log refuses to print the secret it was handed, the
// same way it refuses to take one on argv.
func RedactDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return dsn
	}
	scheme := strings.Index(dsn, "://")
	if scheme < 0 || scheme > at {
		return dsn
	}
	return dsn[:scheme+3] + "***@" + dsn[at+1:]
}
