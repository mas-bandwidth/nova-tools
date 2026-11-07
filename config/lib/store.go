// Package config reads a fleet's configuration from the PostgreSQL store
// nova-config keeps: its machines, the friends that work on it and the
// services it runs, the rows of the schema config (nova-config schema). It
// reads them through the same query nova-config's list verbs run, so a
// program that imports it sees exactly what nova-config prints; writes go
// through nova-config, which records each one in the history.
package config

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaMissing is the error of a store the current schema was never applied to.
const SchemaMissing = `store has no current schema; run: nova-config schema | psql "$NOVA_PG_DSN"`

// Store is one open connection to the store.
type Store struct {
	c     *pgx.Conn
	user  string
	q     querier                                    // what reads run on: the connection, or a snapshot's transaction
	begin func(context.Context) (transaction, error) // a snapshot's transaction
}

// querier runs a read: a *pgx.Conn or a pgx.Tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// transaction is a snapshot's read-only transaction.
type transaction interface {
	querier
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// snapshotTx is a snapshot's transaction: every read sees the same instant.
var snapshotTx = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

// Row is one row as text: its name, its fields by column name, and its
// stamps (RFC 3339, UTC). An unset value is "".
type Row struct {
	Name             string
	F                map[string]string
	Created, Updated string
}

// cause names why a connection failed in a word or two, never Go's text.
func cause(err error) string {
	var dns *net.DNSError
	var op *net.OpError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &dns):
		return "no such host"
	case os.IsTimeout(err) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &op) && op.Timeout()):
		return "timed out"
	}
	return "connect failed"
}

// storeError is a failure of the store as one line, never pgx's text: a store the
// schema was not applied to says how to apply it, a refused login and a denied
// grant say whose and what, anything else is its SQLSTATE and the server's first
// sentence. user is the login and op what was being done.
func storeError(err error, user, op string) error {
	var pe *pgconn.PgError
	switch {
	case err == nil:
		return nil
	case !errors.As(err, &pe):
		return fmt.Errorf("postgres: %s", cause(err))
	case slices.Contains([]string{"42P01", "42703", "3F000"}, pe.Code): // undefined table, undefined column, no such schema
		return errors.New(SchemaMissing)
	case strings.HasPrefix(pe.Code, "28"):
		pw := cmp.Or(os.Getenv("NOVA_PG_PASSWORD_ENV"), "NOVA_PG_PASSWORD")
		return fmt.Errorf("postgres refused the login for %s: check %s; run: PGPASSWORD=\"$%s\" psql \"$NOVA_PG_DSN\" -c 'select 1'", user, pw, pw)
	case pe.Code == "42501":
		words := append([]string{"the table"}, strings.Fields(strings.Split(pe.Message, "\n")[0])...) // never empty
		return fmt.Errorf("%s may not %s %s", user, op, cmp.Or(pe.TableName, words[len(words)-1]))
	}
	msg, _, _ := strings.Cut(strings.Split(pe.Message, "\n")[0], ". ")
	return fmt.Errorf("store error (SQLSTATE %s): %s", pe.Code, strings.TrimSuffix(msg, "."))
}

// DSNProblem says which part of a postgres:// URL is wrong, "" for none; it
// never quotes the URL, which may hold a password.
func DSNProblem(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "not a postgres:// URL (want postgres://USER@HOST:PORT/DB?sslmode=disable)"
	}
	if n, err := strconv.Atoi(u.Port()); u.Port() != "" && (err != nil || n < 1 || n > 65535) {
		return "port " + u.Port() + " out of range"
	}
	return ""
}

// connectError is the failure to open the store as one line.
func connectError(cfg *pgx.ConnConfig, err error) error {
	var pe *pgconn.PgError
	switch {
	case errors.As(err, &pe) && pe.Code == "3D000":
		return fmt.Errorf("database %s does not exist on %s; run: createdb -h %s -p %d -U %s %s", cfg.Database, cfg.Host, cfg.Host, cfg.Port, cfg.User, cfg.Database)
	case errors.As(err, &pe):
		return storeError(err, cfg.User, "connect")
	}
	return fmt.Errorf("cannot reach postgres at %s (NOVA_PG_DSN): %s; check NOVA_PG_DSN or run: psql \"$NOVA_PG_DSN\" -c 'select 1'", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), cause(err))
}

// Open connects to the store dsn names (postgres://USER@HOST:PORT/DB, no
// password in it), with password when it is not empty. Every error is one
// line that never quotes the dsn.
func Open(ctx context.Context, dsn, password string) (*Store, error) {
	if p := DSNProblem(dsn); p != "" {
		return nil, fmt.Errorf("the postgres dsn is wrong: %s", p)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("the postgres dsn could not be parsed") // its text may quote a password
	}
	cfg.Password = cmp.Or(password, cfg.Password)
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := pgx.ConnectConfig(dctx, cfg)
	if err != nil {
		return nil, connectError(cfg, err)
	}
	begin := func(ctx context.Context) (transaction, error) { return c.BeginTx(ctx, snapshotTx) }
	return &Store{c: c, user: cfg.User, q: c, begin: begin}, nil
}

// Close closes the connection.
func (s *Store) Close() { _ = s.c.Close(context.Background()) } // ignored: the caller is done with it

// Conn is the connection, for nova-config's own writes.
func (s *Store) Conn() *pgx.Conn { return s.c }

// Err is a failure of op (read, add, set, remove) on the store as one line.
func (s *Store) Err(err error, op string) error { return storeError(err, s.user, op) }

// Text runs a query whose columns are all text and returns its rows.
func (s *Store) Text(ctx context.Context, query string, args ...any) ([][]string, error) {
	rows, err := s.q.Query(ctx, query, args...)
	if err != nil {
		return nil, s.Err(err, "read")
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, s.Err(err, "read")
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			row[i], _ = v.(string) // ignored: every column is cast to text
		}
		out = append(out, row)
	}
	return out, s.Err(rows.Err(), "read")
}

func ident(s string) string { return `"` + s + `"` }

// listSQL names every column it reads, never *, so a store that carries
// columns this package does not know is read as it is; the columns are the
// name, the given ones as text (” for NULL) and the two stamps.
func listSQL(table string, columns []string) string {
	cols := []string{"name"}
	for _, c := range columns {
		cols = append(cols, "coalesce("+ident(c)+"::text, '')")
	}
	stamp := `to_char(%s AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	cols = append(cols, fmt.Sprintf(stamp, "created_at"), fmt.Sprintf(stamp, "updated_at"))
	return "SELECT " + strings.Join(cols, ", ") + " FROM config." + table + " ORDER BY name"
}

// Rows reads the given columns of every row of config.<table>, in name order:
// the one query behind nova-config's list verbs and Machines, Friends and Services.
func (s *Store) Rows(ctx context.Context, table string, columns []string) ([]Row, error) {
	got, err := s.Text(ctx, listSQL(table, columns))
	var out []Row
	for _, v := range got {
		n := len(v)
		r := Row{Name: v[0], F: map[string]string{}, Created: v[n-2], Updated: v[n-1]}
		for i, c := range columns {
			r.F[c] = v[i+1]
		}
		out = append(out, r)
	}
	return out, err
}
