package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

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
	Close()
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
		return nil, fmt.Errorf("postgres %s@%s:%d/%s: %w", cfg.User, cfg.Host, cfg.Port, cfg.Database, err)
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
		return nil, fmt.Errorf("postgres: list %s: %w", k.name, err)
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
			return nil, fmt.Errorf("postgres: list %s: %w", k.name, err)
		}
		r := Row{Name: vals[0], F: map[string]string{}, Created: vals[len(vals)-2], Updated: vals[len(vals)-1]}
		for i, f := range k.fields {
			r.F[f.name] = vals[i+1]
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list %s: %w", k.name, err)
	}
	return out, nil
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
		return 0, fmt.Errorf("postgres: begin: %w", err)
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
			return 0, refused("%s %s: %s", k.name, name, pe.Message)
		}
		return 0, fmt.Errorf("postgres: %s %s %s: %w", op, k.name, name, err)
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
		return 0, fmt.Errorf("postgres: history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: commit: %w", err)
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
