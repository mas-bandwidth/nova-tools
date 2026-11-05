package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"strconv"
	"strings"
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
// at or after Since when not zero, at most Limit when above zero.
type HistoryQuery struct {
	Kind, Name string
	Since      time.Time
	Limit      int
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

// rootCause is the innermost error of err.
func rootCause(err error) error {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err
		}
		err = inner
	}
}

// storeError is a failure of the store as one line: a store the schema was not
// applied to says how to apply it; anything else is its cause, never pgx's text.
func storeError(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "42P01", "42703", "3F000": // undefined table, undefined column, no such schema
			return errors.New(schemaMissing)
		}
		return fmt.Errorf("postgres: %s", oneLine(pe.Error()))
	}
	return fmt.Errorf("postgres: %s", oneLine(rootCause(err).Error()))
}

// diagnose is the refusal of a write the store's constraints turned down (an
// SQLSTATE 23xxx error): one line that says what to do. machines are the
// machine rows' names and loops the loops on the machine a remove names.
func diagnose(pe *pgconn.PgError, k *kind, op, name string, after Row, machines, loops []string) error {
	switch {
	case pe.Code == "23503" && k.name == "loop" && op != "remove":
		return refused("no machine %s; the machines are %s", after.F["machine"], listOf(machines))
	case pe.Code == "23503" && k.name == "machine" && op == "remove" && len(loops) > 0:
		return refused("machine %s still runs loops %s; remove them first: nova-config loop remove %s --actor <you>", name, strings.Join(loops, ","), loops[0])
	case pe.Code == "23505":
		return refused("%s %s exists; use: nova-config %s set", k.name, name, k.name)
	}
	return refused("%s %s: %s", k.name, name, oneLine(pe.Message))
}

func listOf(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ",")
}

type pgStore struct{ c *pgx.Conn }

func openPG(ctx context.Context, dsn, password string) (Store, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("the postgres dsn could not be parsed (%T)", err) // its text may quote a password
	}
	if password != "" {
		cfg.Password = password
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot reach postgres at %s (NOVA_PG_DSN): %s", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), oneLine(rootCause(err).Error()))
	}
	return pgStore{c}, nil
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

func (p pgStore) List(ctx context.Context, k *kind) ([]Row, error) {
	rows, err := p.c.Query(ctx, listSQL(k))
	if err != nil {
		return nil, storeError(err)
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		vals := make([]string, len(k.fields)+3)
		dest := make([]any, len(vals))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, storeError(err)
		}
		r := Row{Name: vals[0], F: map[string]string{}, Created: vals[len(vals)-2], Updated: vals[len(vals)-1]}
		for i, f := range k.fields {
			r.F[f.name] = vals[i+1]
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, storeError(err)
	}
	return out, nil
}

// historySQL reads the history, newest first; $1 and $2 are a kind and a name
// (” for any), $3 a time or NULL, $4 a limit or NULL.
const historySQL = `SELECT id, to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), actor, kind, name, op, coalesce(before::text, ''), coalesce(after::text, '')
 FROM config.history
 WHERE ($1::text = '' OR kind = $1::text) AND ($2::text = '' OR name = $2::text) AND ($3::timestamptz IS NULL OR at >= $3::timestamptz)
 ORDER BY at DESC, id DESC LIMIT $4::int`

func (p pgStore) History(ctx context.Context, q HistoryQuery) ([]HistoryEntry, error) {
	var since, limit any
	if !q.Since.IsZero() {
		since = q.Since
	}
	if q.Limit > 0 {
		limit = q.Limit
	}
	rows, err := p.c.Query(ctx, historySQL, q.Kind, q.Name, since, limit)
	if err != nil {
		return nil, storeError(err)
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var before, after string
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Kind, &e.Name, &e.Op, &before, &after); err != nil {
			return nil, storeError(err)
		}
		e.Before, e.After = historySide(before), historySide(after)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, storeError(err)
	}
	return out, nil
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

// names are the rows' names of a query, best effort: the lists a diagnosis names.
func (p pgStore) names(ctx context.Context, query string, args ...any) []string {
	rows, err := p.c.Query(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
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
		return 0, storeError(err)
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
			loops := p.names(ctx, "SELECT name FROM config.loops WHERE machine = $1 ORDER BY name", name)
			return 0, diagnose(pe, k, op, name, after, machines, loops)
		}
		return 0, storeError(err)
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
		return 0, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, storeError(err)
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
