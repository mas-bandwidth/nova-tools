package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	config "github.com/mas-bandwidth/nova-tools/v2/modules/config"
)

// Store is the part of PostgreSQL nova-config uses: the rows of the schema
// config's tables (schema.sql) and the history every write appends to.
type Store interface {
	// List returns a kind's rows in name order.
	List(ctx context.Context, k *kind) ([]Row, error)
	// Write adds (before empty), sets or removes (after empty) one row and
	// appends its history row, in one transaction; it returns the history id.
	// With dry it runs the row's statement and rolls it back, returning 0.
	Write(ctx context.Context, k *kind, op string, before, after Row, actor, reason string, dry bool) (int64, error)
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

// oneLine is s on one line, runs of white space as one space.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// diagnose is the refusal of a write the store's constraints turned down (an
// SQLSTATE 23xxx error): one line that says what to do. machines are the
// machine rows' names and services the services on the machine a remove names.
func diagnose(pe *pgconn.PgError, k *kind, op, name string, after Row, machines, services []string) error {
	switch {
	case pe.Code == "23503" && k.name == "service" && op != "remove":
		return refused("no machine %s; the machines are %s; run: nova-config machine add %s --user <user>", after.F["machine"], cmp.Or(strings.Join(machines, ","), "(none)"), after.F["machine"])
	case pe.Code == "23503" && k.name == "machine" && op == "remove" && len(services) > 0:
		return refused("machine %s still runs services %s; remove them first: nova-config service remove %s --actor <you>", name, strings.Join(services, ","), services[0])
	case pe.Code == "23503" && op == "remove":
		return refused("%s %s is still named by config.%s; change that row first, then: nova-config %s remove %s --actor <you>", k.name, name, cmp.Or(pe.TableName, "another table"), k.name, name)
	case pe.Code == "23505":
		return refused("%s %s exists; use: nova-config %s set %s", k.name, name, k.name, name)
	}
	return refused("%s %s: the store refused the row: %s; read the constraint, then give a value it takes; run: psql \"$NOVA_PG_DSN\" -c '\\d config.%s'", k.name, name, oneLine(pe.Message), k.table)
}

func ident(s string) string { return `"` + s + `"` }

// pgStore is the store through the config package: List is its Rows, the
// query behind Machines, Friends and Services.
type pgStore struct{ *config.Store }

func openPG(ctx context.Context, dsn, password string) (Store, error) {
	s, err := config.Open(ctx, dsn, password)
	if err != nil {
		return nil, err
	}
	return pgStore{s}, nil
}

func (p pgStore) List(ctx context.Context, k *kind) ([]Row, error) {
	var cols []string
	for _, f := range k.fields {
		cols = append(cols, f.name)
	}
	return p.Rows(ctx, k.table, cols)
}

// historySQL reads the history, newest first; $1 and $2 are a kind and a name
// (empty for any), $3 a time or NULL, $4 a limit or NULL for all.
const historySQL = `SELECT id::text, to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), actor, kind, name, op, coalesce(before::text, ''), coalesce(after::text, '')
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
	got, err := p.Text(ctx, historySQL, q.Kind, q.Name, since, limit)
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
	got, _ := p.Text(ctx, query, args...) // ignored: a diagnosis without its list is still a refusal
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
// named, so a column this tool does not know keeps its default. A set or a
// remove touches the row only while its fields read as before, as List reads them.
func writeSQL(k *kind, op, name string, before, after Row) (string, []any) {
	args := []any{name}
	var cols, marks, sets, olds []string
	for _, f := range k.fields {
		args = append(args, arg(f, after.F[f.name]))
		n := "$" + strconv.Itoa(len(args))
		cols, marks, sets = append(cols, ident(f.name)), append(marks, n), append(sets, ident(f.name)+" = "+n)
		olds = append(olds, "'"+f.name+"', coalesce("+ident(f.name)+"::text, '')")
	}
	same := " AND jsonb_build_object(" + strings.Join(olds, ", ") + ") = $%d::jsonb"
	switch op {
	case "add":
		return "INSERT INTO config." + k.table + " (name, " + strings.Join(cols, ", ") + ") VALUES ($1, " + strings.Join(marks, ", ") + ")", args
	case "set":
		return "UPDATE config." + k.table + " SET " + strings.Join(sets, ", ") + ", updated_at = now() WHERE name = $1" + fmt.Sprintf(same, len(args)+1), append(args, jsonb(before.F))
	}
	return "DELETE FROM config." + k.table + " WHERE name = $1" + fmt.Sprintf(same, 2), []any{name, jsonb(before.F)}
}

func (p pgStore) Write(ctx context.Context, k *kind, op string, before, after Row, actor, reason string, dry bool) (int64, error) {
	tx, err := p.Conn().Begin(ctx)
	if err != nil {
		return 0, p.Err(err, op)
	}
	defer func() { _ = tx.Rollback(ctx) }() // ignored: a no-op after the commit; on failure the failure is returned
	name := after.Name
	if name == "" {
		name = before.Name
	}
	q, args := writeSQL(k, op, name, before, after)
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && strings.HasPrefix(pe.Code, "23") { // integrity: unique, foreign key, check
			_ = tx.Rollback(ctx) // ignored: the failed statement aborted it; the lists below need the connection
			machines := p.names(ctx, "SELECT name FROM config.machines ORDER BY name")
			services := p.names(ctx, "SELECT name FROM config.services WHERE machine = $1 ORDER BY name", name)
			return 0, diagnose(pe, k, op, name, after, machines, services)
		}
		return 0, p.Err(err, op)
	}
	if tag.RowsAffected() == 0 {
		return 0, refused("%s %s was changed or removed by another write since it was read; nothing written; run: nova-config %s show %s, then repeat the %s", k.name, name, k.name, name, op)
	}
	b, a := withReason(before.F, after.F, reason)
	if dry {
		return 0, nil // the deferred rollback undoes the statement
	}
	var id int64
	err = tx.QueryRow(ctx, "INSERT INTO config.history (kind, name, op, before, after, actor) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id",
		k.name, name, op, jsonb(b), jsonb(a), actor).Scan(&id)
	if err != nil {
		return 0, p.Err(err, op)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, p.Err(err, op)
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
