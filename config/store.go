package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

// Store is the part of PostgreSQL nova-config uses: the rows of the schema
// config's tables (schema.sql) and the history every write appends to.
type Store interface {
	// List returns a kind's rows in name order.
	List(ctx context.Context, k *kind) ([]Row, error)
	// Write adds (before empty), sets or removes (after empty) one row and
	// appends its history row, in one transaction; it returns the history id.
	Write(ctx context.Context, k *kind, op string, before, after Row, actor, reason string) (int64, error)
	// History returns history rows, newest first.
	History(ctx context.Context, q HistoryQuery) ([]HistoryEntry, error)
	Close()
}

// HistoryQuery selects history rows: a kind and a name when not empty, those
// at or after Since when not zero.
type HistoryQuery struct {
	Kind, Name string
	Since      time.Time
	Limit      int // at most this many rows when above zero
}

// HistoryEntry is one history row; Before and After are the row's fields as
// the write recorded them, nil on the side that did not exist.
type HistoryEntry struct {
	ID                        int64
	At, Actor, Kind, Name, Op string
	Before, After             map[string]string
}

// schemaMissing is the refusal of a store the schema was never applied to.
const schemaMissing = `store has no current schema; run: nova-config schema | psql "$NOVA_PG_DSN"`

// oneLine is s on one line, runs of white space as one space.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

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
		return errors.New(schemaMissing)
	case strings.HasPrefix(pe.Code, "28"):
		return fmt.Errorf("postgres refused the login for %s: check NOVA_PG_PASSWORD", user)
	case pe.Code == "42501":
		words := append([]string{"the table"}, strings.Fields(strings.Split(pe.Message, "\n")[0])...) // never empty
		return fmt.Errorf("%s may not %s %s", user, op, cmp.Or(pe.TableName, words[len(words)-1]))
	}
	msg, _, _ := strings.Cut(strings.Split(pe.Message, "\n")[0], ". ")
	return fmt.Errorf("store error (SQLSTATE %s): %s", pe.Code, strings.TrimSuffix(msg, "."))
}

// dsnProblem says which part of a postgres:// URL is wrong; it never quotes the URL, which may hold a password.
func dsnProblem(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "not a postgres:// URL"
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
		return fmt.Errorf("database %s does not exist on %s", cfg.Database, cfg.Host)
	case errors.As(err, &pe):
		return storeError(err, cfg.User, "connect")
	}
	return fmt.Errorf("cannot reach postgres at %s (NOVA_PG_DSN): %s", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), cause(err))
}

// diagnose is the refusal of a write the store's constraints turned down (an
// SQLSTATE 23xxx error): one line that says what to do. machines are the
// machine rows' names and services the services on the machine a remove names.
func diagnose(pe *pgconn.PgError, k *kind, op, name string, after Row, machines, services []string) error {
	switch {
	case pe.Code == "23503" && k.name == "service" && op != "remove":
		return refused("no machine %s; the machines are %s", after.F["machine"], cmp.Or(strings.Join(machines, ","), "(none)"))
	case pe.Code == "23503" && k.name == "machine" && op == "remove" && len(services) > 0:
		return refused("machine %s still runs services %s; remove them first: nova-config service remove %s --actor <you>", name, strings.Join(services, ","), services[0])
	case pe.Code == "23505":
		return refused("%s %s exists; use: nova-config %s set", k.name, name, k.name)
	}
	return refused("%s %s: %s", k.name, name, oneLine(pe.Message))
}

type pgStore struct {
	c    *pgx.Conn
	user string
}

func openPG(ctx context.Context, dsn, password string) (Store, error) {
	if p := dsnProblem(dsn); p != "" {
		return nil, fmt.Errorf("the postgres dsn is wrong: %s", p)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("the postgres dsn could not be parsed") // its text may quote a password
	}
	if password != "" {
		cfg.Password = password
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, connectError(cfg, err)
	}
	return pgStore{c, cfg.User}, nil
}

func (p pgStore) Close() { _ = p.c.Close(context.Background()) } // ignored: the run is over

func ident(s string) string { return `"` + s + `"` }

// listSQL names every column it reads, never *, so a store that carries
// columns this tool no longer knows is read as it is; the columns are the
// name, the kind's fields and the two stamps.
func listSQL(k *kind) string {
	cols := []string{"name"}
	for _, f := range k.fields {
		cols = append(cols, "coalesce("+ident(f.name)+"::text, '')")
	}
	stamp := `to_char(%s AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	cols = append(cols, fmt.Sprintf(stamp, "created_at"), fmt.Sprintf(stamp, "updated_at"))
	return "SELECT " + strings.Join(cols, ", ") + " FROM config." + k.table + " ORDER BY name"
}

// text runs a query whose columns are all text and returns its rows.
func (p pgStore) text(ctx context.Context, query string, args ...any) ([][]string, error) {
	rows, err := p.c.Query(ctx, query, args...)
	if err != nil {
		return nil, storeError(err, p.user, "read")
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, storeError(err, p.user, "read")
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			row[i], _ = v.(string) // ignored: every column is cast to text
		}
		out = append(out, row)
	}
	return out, storeError(rows.Err(), p.user, "read")
}

func (p pgStore) List(ctx context.Context, k *kind) ([]Row, error) {
	got, err := p.text(ctx, listSQL(k))
	var out []Row
	for _, v := range got {
		n := len(v)
		r := Row{Name: v[0], F: map[string]string{}, Created: v[n-2], Updated: v[n-1]}
		for i, f := range k.fields {
			r.F[f.name] = v[i+1]
		}
		out = append(out, r)
	}
	return out, err
}

// historySQL reads the history, newest first; $1 and $2 are a kind and a name
// (” for any), $3 a time or NULL.
const historySQL = `SELECT id::text, to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), actor, kind, name, op, coalesce(before::text, ''), coalesce(after::text, '')
 FROM config.history
 WHERE ($1::text = '' OR kind = $1::text OR ($1::text = 'service' AND kind = 'loop')) AND ($2::text = '' OR name = $2::text) AND ($3::timestamptz IS NULL OR at >= $3::timestamptz)
 ORDER BY at DESC, id DESC LIMIT $4::int`

func (p pgStore) History(ctx context.Context, q HistoryQuery) ([]HistoryEntry, error) {
	var since, limit any
	if !q.Since.IsZero() {
		since = q.Since
	}
	if q.Limit > 0 {
		limit = q.Limit
	}
	got, err := p.text(ctx, historySQL, q.Kind, q.Name, since, limit)
	var out []HistoryEntry
	for _, v := range got {
		id, _ := strconv.ParseInt(v[0], 10, 64) // ignored: the column is a bigint
		out = append(out, HistoryEntry{id, v[1], v[2], v[3], v[4], v[5], historySide(v[6]), historySide(v[7])})
	}
	return out, err
}

// historySide is a history column's JSON as fields: nil for none; a value that
// is not a string (an older tool's) is its JSON.
func historySide(text string) map[string]string {
	var raw map[string]any
	if text == "" || json.Unmarshal([]byte(text), &raw) != nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		} else {
			out[k] = marshal(v)
		}
	}
	return out
}

// names are the first column of a query, best effort: the lists a diagnosis names.
func (p pgStore) names(ctx context.Context, query string, args ...any) []string {
	got, _ := p.text(ctx, query, args...) // ignored: a diagnosis without its list is still a refusal
	var out []string
	for _, v := range got {
		out = append(out, v[0])
	}
	return out
}

// arg is a field's value as its column takes it: NULL for an unset int or an
// empty ref, a number, a boolean, or text.
func arg(f field, v string) any {
	switch {
	case v == "" && (f.null || f.typ == "ref"):
		return nil
	case f.typ == "int":
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case f.typ == "bool":
		return v == "true"
	}
	return v
}

// writeSQL is the statement of a write and its arguments: the fields are
// named, so a column this tool does not know keeps its default.
func writeSQL(k *kind, op, name string, after Row) (string, []any) {
	args := []any{name}
	var cols, marks, sets []string
	for _, f := range k.fields {
		args = append(args, arg(f, after.F[f.name]))
		n := "$" + strconv.Itoa(len(args))
		cols, marks, sets = append(cols, ident(f.name)), append(marks, n), append(sets, ident(f.name)+" = "+n)
	}
	switch op {
	case "add":
		return "INSERT INTO config." + k.table + " (name, " + strings.Join(cols, ", ") + ") VALUES ($1, " + strings.Join(marks, ", ") + ")", args
	case "set":
		return "UPDATE config." + k.table + " SET " + strings.Join(sets, ", ") + ", updated_at = now() WHERE name = $1", args
	}
	return "DELETE FROM config." + k.table + " WHERE name = $1", args[:1]
}

func (p pgStore) Write(ctx context.Context, k *kind, op string, before, after Row, actor, reason string) (int64, error) {
	tx, err := p.c.Begin(ctx)
	if err != nil {
		return 0, storeError(err, p.user, op)
	}
	defer func() { _ = tx.Rollback(ctx) }() // ignored: a no-op after the commit; on failure the failure is returned
	name := after.Name
	if name == "" {
		name = before.Name
	}
	q, args := writeSQL(k, op, name, after)
	if _, err := tx.Exec(ctx, q, args...); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && strings.HasPrefix(pe.Code, "23") { // integrity: unique, foreign key, check
			_ = tx.Rollback(ctx) // ignored: the failed statement aborted it; the lists below need the connection
			machines := p.names(ctx, "SELECT name FROM config.machines ORDER BY name")
			services := p.names(ctx, "SELECT name FROM config.services WHERE machine = $1 ORDER BY name", name)
			return 0, diagnose(pe, k, op, name, after, machines, services)
		}
		return 0, storeError(err, p.user, op)
	}
	b, a := before.F, after.F
	if reason != "" && a != nil {
		a = maps.Clone(a)
		a["reason"] = reason
	} else if reason != "" {
		b = maps.Clone(b)
		b["reason"] = reason
	}
	var id int64
	err = tx.QueryRow(ctx, "INSERT INTO config.history (kind, name, op, before, after, actor) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id",
		k.name, name, op, jsonb(b), jsonb(a), actor).Scan(&id)
	if err != nil {
		return 0, storeError(err, p.user, op)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, storeError(err, p.user, op)
	}
	return id, nil
}

// jsonb is a history side as its column takes it: NULL for none.
func jsonb(m map[string]string) any {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m) // ignored: a map of strings always encodes
	return string(b)
}
