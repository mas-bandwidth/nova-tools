package record

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schemaSQL string

// PostgresStore is the durable Store over Postgres. One connection pool, one writer.
type PostgresStore struct {
	db  *sql.DB
	now func() time.Time
}

// OpenPostgres connects, verifies the server answers, and returns the store. The Ping is
// the one network call, and it is made by `nova-work`, never by a unit test: the unit tests
// use FakeStore.
func OpenPostgres(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres %s: %w", redactDSN(dsn), err)
	}
	return &PostgresStore{db: db, now: time.Now}, nil
}

// Migrate applies schema.sql statement by statement in one transaction and records the
// schema version. Running it twice is a no-op, which is how `--migrate` is safe to call on
// every start.
func (p *PostgresStore) Migrate(ctx context.Context) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, stmt := range splitSQL(schemaSQL) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// Insert writes one row and reports whether it created it. ON CONFLICT DO NOTHING makes a
// redelivered stream id a no-op rather than a second row.
func (p *PostgresStore) Insert(ctx context.Context, r Row) (bool, error) {
	if r.RecordedAt.IsZero() {
		r.RecordedAt = p.now()
	}
	var pr any
	if r.PR != "" {
		pr = r.PR
	}
	res, err := p.db.ExecContext(ctx, `INSERT INTO card_results
        (stream_id, label, bench, exit, result_line, job_path, commit, branch, pr, pushed_at, done_at, recorded_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
        ON CONFLICT (stream_id) DO NOTHING`,
		r.StreamID, r.Label, r.Bench, r.Exit, r.ResultLine, r.JobPath, r.Commit, r.Branch, pr, r.PushedAt, r.DoneAt, r.RecordedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// List returns the rows the filter names, newest first. The order is COALESCE(done_at,
// recorded_at) DESC, stream_id DESC so a result with no done_at is not silently hidden.
func (p *PostgresStore) List(ctx context.Context, f Filter) ([]Row, error) {
	q := `SELECT stream_id, label, bench, exit, result_line, job_path, commit, branch, pr, pushed_at, done_at, recorded_at
        FROM card_results`
	var where []string
	var args []any
	if !f.Since.IsZero() {
		args = append(args, f.Since)
		where = append(where, fmt.Sprintf("COALESCE(done_at, recorded_at) >= $%d", len(args)))
	}
	if f.Bench != "" {
		args = append(args, f.Bench)
		where = append(where, fmt.Sprintf("bench = $%d", len(args)))
	}
	if f.Failed {
		where = append(where, "exit <> 0")
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY COALESCE(done_at, recorded_at) DESC, stream_id DESC"

	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var pr sql.NullString
		var pushed, done sql.NullTime
		if err := rows.Scan(&r.StreamID, &r.Label, &r.Bench, &r.Exit, &r.ResultLine, &r.JobPath,
			&r.Commit, &r.Branch, &pr, &pushed, &done, &r.RecordedAt); err != nil {
			return nil, err
		}
		if pr.Valid {
			r.PR = pr.String
		}
		if pushed.Valid {
			r.PushedAt = &pushed.Time
		}
		if done.Valid {
			r.DoneAt = &done.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Close releases the pool.
func (p *PostgresStore) Close() error { return p.db.Close() }

// splitSQL strips line comments and splits a script on semicolons. The schema holds no
// string literal containing a semicolon, which is the one case this would need to know
// about; keeping the schema simple is cheaper than trusting a parser.
func splitSQL(script string) []string {
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

// redactDSN keeps a password out of an error line. A DSN may be a URL with user:password@;
// the record refuses to print the secret it was handed.
func redactDSN(dsn string) string {
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
