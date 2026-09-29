package config

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// Migrations are the numbered SQL files under migrations/, applied in order,
// each in one transaction, each recorded in config.schema_migrations. A file
// is NNNN_<what>.sql; a number applied is never applied again, so migrate is
// idempotent (docs/SPEC-CONFIG.md, "The schema").
//
//go:embed migrations/*.sql
var migrations embed.FS

// Migration is one embedded migration.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations lists the embedded migrations in version order. A file whose
// name does not begin with its number, or a number declared twice, is an
// error: the ledger keys on the number.
func Migrations() ([]Migration, error) {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	seen := map[int]string{}
	var out []Migration
	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		num, _, ok := strings.Cut(base, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration %s: want NNNN_<what>.sql", base)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("migrations %s and %s share version %d", prev, base, v)
		}
		seen[v] = base
		body, err := migrations.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: base, SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// readRole is the fleet's read-only role (fleet play: nova_read). After
// every migrate the schema's tables are readable by it, when it exists;
// a throwaway database has no such role and skips the grant.
const readRole = "nova_read"

// PG is the Postgres Store. Open it with OpenPG; the DSN carries no password
// on a command line (cmd/nova-config resolves it from the environment).
type PG struct {
	db *sql.DB
}

// OpenPG opens the store and pings it once, so a wrong address or login is
// refused here rather than on the first verb.
func OpenPG(ctx context.Context, dsn string) (*PG, error) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres dsn: %w", err)
	}
	_ = cfg
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	db.SetMaxOpenConns(2)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres at %s: %w", Redact(dsn), err)
	}
	return &PG{db: db}, nil
}

// Close closes the pool.
func (p *PG) Close() error { return p.db.Close() }

// Redact returns the DSN with any password replaced, for a line.
func Redact(dsn string) string {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return "(unparsed dsn)"
	}
	host := cfg.Host
	if cfg.Port != 0 {
		host = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	}
	if cfg.User != "" {
		return cfg.User + "@" + host + "/" + cfg.Database
	}
	return host + "/" + cfg.Database
}

// Version is the greatest applied migration, 0 before the first migrate
// (the ledger table itself does not exist yet).
func (p *PG) Version(ctx context.Context) (int, error) {
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'config' AND table_name = 'schema_migrations')`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("postgres: read schema version: %w", err)
	}
	if !exists {
		return 0, nil
	}
	var v sql.NullInt64
	if err := p.db.QueryRowContext(ctx, `SELECT max(version) FROM config.schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("postgres: read schema version: %w", err)
	}
	return int(v.Int64), nil
}

// Migrate applies every embedded migration the ledger lacks, each in its own
// transaction with its ledger row, and returns the version before, the
// version after and the versions applied. Running it twice applies nothing
// the second time.
func (p *PG) Migrate(ctx context.Context) (from, to int, applied []int, err error) {
	all, err := Migrations()
	if err != nil {
		return 0, 0, nil, err
	}
	from, err = p.Version(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	to = from
	for _, m := range all {
		if m.Version <= from {
			continue
		}
		if err := p.applyOne(ctx, m); err != nil {
			return from, to, applied, err
		}
		applied = append(applied, m.Version)
		to = m.Version
	}
	if err := p.grantRead(ctx); err != nil {
		return from, to, applied, err
	}
	return from, to, applied, nil
}

func (p *PG) applyOne(ctx context.Context, m Migration) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %d: begin: %w", m.Version, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO config.schema_migrations (version) VALUES ($1)`, m.Version); err != nil {
		return fmt.Errorf("migration %d: record: %w", m.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %d: commit: %w", m.Version, err)
	}
	return nil
}

// grantRead lets the read role read the schema when the role exists.
func (p *PG) grantRead(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+readRole+`') THEN
    EXECUTE 'GRANT USAGE ON SCHEMA config TO `+readRole+`';
    EXECUTE 'GRANT SELECT ON ALL TABLES IN SCHEMA config TO `+readRole+`';
  END IF;
END $$`)
	if err != nil {
		return fmt.Errorf("grant %s: %w", readRole, err)
	}
	return nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// columns is `"name", "f1", "f2", ...` for the kind.
func columns(k *Kind) string {
	cols := []string{quoteIdent("name")}
	for _, f := range k.Fields {
		cols = append(cols, quoteIdent(f.Name))
	}
	return strings.Join(cols, ", ")
}

// scanRow reads one row of the kind from a query of columns(k), created_at,
// updated_at.
func scanRow(k *Kind, scan func(dest ...any) error) (Row, error) {
	row := Row{Fields: map[string]string{}}
	dest := []any{&row.Name}
	texts := make([]sql.NullString, len(k.Fields))
	ints := make([]int64, len(k.Fields))
	for i, f := range k.Fields {
		if f.Type == TypeInt {
			dest = append(dest, &ints[i])
		} else {
			dest = append(dest, &texts[i])
		}
	}
	var created, updated time.Time
	dest = append(dest, &created, &updated)
	if err := scan(dest...); err != nil {
		return Row{}, err
	}
	for i, f := range k.Fields {
		if f.Type == TypeInt {
			row.Fields[f.Name] = strconv.FormatInt(ints[i], 10)
		} else {
			// A NULL (an optional ref naming no row) is the empty value.
			row.Fields[f.Name] = texts[i].String
		}
	}
	row.CreatedAt = created.UTC().Format(time.RFC3339)
	row.UpdatedAt = updated.UTC().Format(time.RFC3339)
	return row, nil
}

// values binds a row's fields in column order after name.
func values(k *Kind, row Row) []any {
	args := []any{row.Name}
	for _, f := range k.Fields {
		args = append(args, fieldArg(f, row.Fields[f.Name]))
	}
	return args
}

// fieldArg is one field's value as the column takes it: an int as a
// number, an empty optional ref as NULL (the foreign key allows no ”), any
// other value as text.
func fieldArg(f Field, v string) any {
	switch {
	case f.Type == TypeInt:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case f.Type == TypeRef && v == "":
		return nil
	}
	return v
}

func kindOf(kind string) (*Kind, error) {
	k, ok := Lookup(kind)
	if !ok {
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	return k, nil
}

// queryer is what *sql.DB and *sql.Tx share, so one read runs on either.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (p *PG) Get(ctx context.Context, kind, name string) (Row, bool, error) {
	return getRow(ctx, p.db, kind, name)
}

func getRow(ctx context.Context, q queryer, kind, name string) (Row, bool, error) {
	k, err := kindOf(kind)
	if err != nil {
		return Row{}, false, err
	}
	stmt := `SELECT ` + columns(k) + `, created_at, updated_at FROM config.` + quoteIdent(k.Table) + ` WHERE name = $1`
	row, err := scanRow(k, q.QueryRowContext(ctx, stmt, name).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, fmt.Errorf("postgres: read %s %s: %w", kind, name, err)
	}
	return row, true, nil
}

func (p *PG) List(ctx context.Context, kind string) ([]Row, error) {
	return listRows(ctx, p.db, kind)
}

func listRows(ctx context.Context, q queryer, kind string) ([]Row, error) {
	k, err := kindOf(kind)
	if err != nil {
		return nil, err
	}
	stmt := `SELECT ` + columns(k) + `, created_at, updated_at FROM config.` + quoteIdent(k.Table) + ` ORDER BY name`
	rows, err := q.QueryContext(ctx, stmt)
	if err != nil {
		return nil, fmt.Errorf("postgres: list %s: %w", kind, err)
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		row, err := scanRow(k, rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("postgres: list %s: %w", kind, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list %s: %w", kind, err)
	}
	return out, nil
}

// MachinesAndFleet reads every machine row and the fleet row in one
// read-only repeatable-read transaction: both come from one snapshot, so a
// write between them cannot show one revision of the machines and another of
// the fleet row.
func (p *PG) MachinesAndFleet(ctx context.Context) ([]Row, Row, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, Row{}, fmt.Errorf("postgres: begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	machines, err := listRows(ctx, tx, KindMachine)
	if err != nil {
		return nil, Row{}, err
	}
	fleet, _, err := getRow(ctx, tx, KindFleet, KindFleet)
	if err != nil {
		return nil, Row{}, err
	}
	return machines, fleet, nil
}

// record appends the history row inside the write's transaction.
func record(ctx context.Context, tx *sql.Tx, kind, name, op string, before, after map[string]string, actor string) (int64, error) {
	var b, a []byte
	var err error
	if before != nil {
		if b, err = json.Marshal(before); err != nil {
			return 0, err
		}
	}
	if after != nil {
		if a, err = json.Marshal(after); err != nil {
			return 0, err
		}
	}
	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO config.history (kind, name, op, before, after, actor) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		kind, name, op, nullJSON(b), nullJSON(a), actor).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("record history: %w", err)
	}
	return id, nil
}

func nullJSON(b []byte) any {
	if b == nil {
		return nil
	}
	return string(b)
}

// sqlState is the SQLSTATE of a pgx error, "" for any other.
func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func (p *PG) Insert(ctx context.Context, kind string, row Row, actor string) (int64, error) {
	k, err := kindOf(kind)
	if err != nil {
		return 0, err
	}
	if err := checkRefs(ctx, p, k, row); err != nil {
		return 0, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	args := values(k, row)
	marks := make([]string, len(args))
	for i := range marks {
		marks[i] = "$" + strconv.Itoa(i+1)
	}
	q := `INSERT INTO config.` + quoteIdent(k.Table) + ` (` + columns(k) + `) VALUES (` + strings.Join(marks, ", ") + `)`
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		switch sqlState(err) {
		case "23505":
			return 0, &RefusedError{Err: ErrExists, Detail: fmt.Sprintf("%s %s exists", kind, row.Name)}
		case "23503":
			return 0, &RefusedError{Err: ErrNoRef, Detail: fmt.Sprintf("%s %s names a row that is not there", kind, row.Name)}
		}
		return 0, fmt.Errorf("postgres: add %s %s: %w", kind, row.Name, err)
	}
	id, err := record(ctx, tx, kind, row.Name, OpAdd, nil, row.Fields, actor)
	if err != nil {
		return 0, fmt.Errorf("postgres: add %s %s: %w", kind, row.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("postgres: add %s %s: commit: %w", kind, row.Name, err)
	}
	return id, nil
}

func (p *PG) Update(ctx context.Context, kind, name string, changes map[string]string, actor string) (Row, int64, error) {
	k, err := kindOf(kind)
	if err != nil {
		return Row{}, 0, err
	}
	cur, found, err := p.Get(ctx, kind, name)
	if err != nil {
		return Row{}, 0, err
	}
	if !found {
		return Row{}, 0, &RefusedError{Err: ErrNotFound, Detail: fmt.Sprintf("%s %s not found", kind, name)}
	}
	next := cur.Clone()
	for f, v := range changes {
		next.Fields[f] = v
	}
	if err := checkRefs(ctx, p, k, next); err != nil {
		return Row{}, 0, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Row{}, 0, fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var sets []string
	args := []any{name}
	for _, f := range k.Fields {
		v, ok := changes[f.Name]
		if !ok {
			continue
		}
		args = append(args, fieldArg(f, v))
		sets = append(sets, quoteIdent(f.Name)+" = $"+strconv.Itoa(len(args)))
	}
	sets = append(sets, "updated_at = now()")
	q := `UPDATE config.` + quoteIdent(k.Table) + ` SET ` + strings.Join(sets, ", ") + ` WHERE name = $1`
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		if sqlState(err) == "23503" {
			return Row{}, 0, &RefusedError{Err: ErrNoRef, Detail: fmt.Sprintf("%s %s names a row that is not there", kind, name)}
		}
		return Row{}, 0, fmt.Errorf("postgres: set %s %s: %w", kind, name, err)
	}
	id, err := record(ctx, tx, kind, name, OpSet, cur.Fields, next.Fields, actor)
	if err != nil {
		return Row{}, 0, fmt.Errorf("postgres: set %s %s: %w", kind, name, err)
	}
	if err := tx.Commit(); err != nil {
		return Row{}, 0, fmt.Errorf("postgres: set %s %s: commit: %w", kind, name, err)
	}
	after, _, err := p.Get(ctx, kind, name)
	if err != nil {
		return Row{}, 0, err
	}
	return after, id, nil
}

func (p *PG) Delete(ctx context.Context, kind, name string, actor string) (int64, error) {
	k, err := kindOf(kind)
	if err != nil {
		return 0, err
	}
	cur, found, err := p.Get(ctx, kind, name)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, &RefusedError{Err: ErrNotFound, Detail: fmt.Sprintf("%s %s not found", kind, name)}
	}
	if err := checkReferenced(ctx, p, kind, name); err != nil {
		return 0, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM config.`+quoteIdent(k.Table)+` WHERE name = $1`, name); err != nil {
		if sqlState(err) == "23503" {
			return 0, &RefusedError{Err: ErrReferenced, Detail: fmt.Sprintf("%s %s is named by another row", kind, name)}
		}
		return 0, fmt.Errorf("postgres: remove %s %s: %w", kind, name, err)
	}
	id, err := record(ctx, tx, kind, name, OpRemove, cur.Fields, nil, actor)
	if err != nil {
		return 0, fmt.Errorf("postgres: remove %s %s: %w", kind, name, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("postgres: remove %s %s: commit: %w", kind, name, err)
	}
	return id, nil
}

func (p *PG) History(ctx context.Context, kind, name string) ([]Change, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, kind, name, op, before, after, actor, at FROM config.history WHERE kind = $1 AND name = $2 ORDER BY id`, kind, name)
	if err != nil {
		return nil, fmt.Errorf("postgres: history %s %s: %w", kind, name, err)
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		var before, after sql.NullString
		var at time.Time
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.Op, &before, &after, &c.Actor, &at); err != nil {
			return nil, fmt.Errorf("postgres: history %s %s: %w", kind, name, err)
		}
		if before.Valid {
			if err := json.Unmarshal([]byte(before.String), &c.Before); err != nil {
				return nil, fmt.Errorf("postgres: history %d before: %w", c.ID, err)
			}
		}
		if after.Valid {
			if err := json.Unmarshal([]byte(after.String), &c.After); err != nil {
				return nil, fmt.Errorf("postgres: history %d after: %w", c.ID, err)
			}
		}
		c.At = at.UTC().Format(time.RFC3339)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: history %s %s: %w", kind, name, err)
	}
	return out, nil
}

func (p *PG) Rev(ctx context.Context, kind string) (int64, error) {
	var v sql.NullInt64
	if err := p.db.QueryRowContext(ctx, `SELECT max(id) FROM config.history WHERE kind = $1`, kind).Scan(&v); err != nil {
		return 0, fmt.Errorf("postgres: rev %s: %w", kind, err)
	}
	return v.Int64, nil
}

func (p *PG) Counts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for _, k := range Kinds {
		if k.Singleton {
			continue
		}
		var n int
		if err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM config.`+quoteIdent(k.Table)).Scan(&n); err != nil {
			return nil, fmt.Errorf("postgres: count %s: %w", k.Name, err)
		}
		out[k.Name] = n
	}
	return out, nil
}

// register the pgx stdlib driver name once; the blank import form is what
// database/sql documents, and stdlib.GetDefaultDriver keeps the reference
// explicit for a reader.
var _ = stdlib.GetDefaultDriver
