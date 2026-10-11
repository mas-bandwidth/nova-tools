package config

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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

// ConnectTimeout bounds the connection check of OpenPG when the context
// carries no deadline of its own.
const ConnectTimeout = 10 * time.Second

// OpenPG opens the store and pings it once, so a wrong address or login is
// refused here rather than on the first verb. The ping is bounded by the
// context's deadline when it has one and by ConnectTimeout when it has none.
func OpenPG(ctx context.Context, dsn string) (*PG, error) {
	return openPGWithin(ctx, dsn, ConnectTimeout)
}

// openPGWithin is OpenPG with the bound for a context that carries no
// deadline given, so the package's tests can shorten it: a caller's deadline,
// longer or shorter, always governs.
func openPGWithin(ctx context.Context, dsn string, noDeadline time.Duration) (*PG, error) {
	// A refusal never reproduces a password from the input (docs/SPEC-CONFIG.md):
	// the parser's own message runs the DSN through a best-effort redactor that
	// malformed input defeats, so a rejected DSN names only the error's type.
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		// The parser's text masks a password only on a best-effort basis, so the
		// refusal names the defect class and never wraps it (docs/nova-config/README.md, "Connecting").
		return nil, fmt.Errorf("postgres dsn could not be parsed (%T)", err)
	}
	_ = cfg
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres could not open (%T)", err)
	}
	db.SetMaxOpenConns(2)
	pingCtx := ctx
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		pingCtx, cancel = context.WithTimeout(ctx, noDeadline)
		defer cancel()
	}
	if err := db.PingContext(pingCtx); err != nil {
		// ignored: a close on the failure path; the ping error is the one returned
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

// Applied is the ledger: every version recorded in config.schema_migrations,
// in order, none before the first migrate. migrate --dry-run prints it, so a
// version missing below the greatest is seen rather than assumed.
func (p *PG) Applied(ctx context.Context) ([]int, error) {
	if v, err := p.Version(ctx); err != nil || v == 0 {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, `SELECT version FROM config.schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("postgres: read the migration ledger: %w", err)
	}
	// ignored: rows.Err reports a read failure; closing a finished read cannot add one
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("postgres: read the migration ledger: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: read the migration ledger: %w", err)
	}
	return out, nil
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
	for _, m := range Pending(all, from) {
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
	// ignored: a rollback after a commit is a no-op, and on a failure path the failure is the one returned
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

// Ownership reads schema config's owner, whether the connected role may
// create in it, and each table's owner, in one catalog query; the schema's
// absence is an empty SchemaOwner and no tables.
func (p *PG) Ownership(ctx context.Context) (Ownership, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT current_user::text,
       coalesce(pg_get_userbyid(n.nspowner)::text, ''),
       coalesce(has_schema_privilege(n.oid, 'CREATE'), false),
       coalesce(c.relname::text, ''),
       coalesce(pg_get_userbyid(c.relowner)::text, '')
  FROM (SELECT 1) AS one
  LEFT JOIN pg_namespace n ON n.nspname = 'config'
  LEFT JOIN pg_class c ON c.relnamespace = n.oid AND c.relkind IN ('r', 'p')`)
	if err != nil {
		return Ownership{}, fmt.Errorf("postgres: read the owners of schema config: %w", err)
	}
	// ignored: rows.Err reports a read failure; closing a finished read cannot add one
	defer func() { _ = rows.Close() }()
	o := Ownership{Tables: map[string]string{}}
	for rows.Next() {
		var table, owner string
		if err := rows.Scan(&o.Role, &o.SchemaOwner, &o.Create, &table, &owner); err != nil {
			return Ownership{}, fmt.Errorf("postgres: read the owners of schema config: %w", err)
		}
		if table != "" {
			o.Tables[table] = owner
		}
	}
	if err := rows.Err(); err != nil {
		return Ownership{}, fmt.Errorf("postgres: read the owners of schema config: %w", err)
	}
	return o, nil
}

// Sessions is every other backend of this database connected as a nova role
// (usename nova_*), as pg_stat_activity shows it to any role: the role,
// application_name and pid are visible to every connected role; the query and
// client columns are not, so they are not read. The connection's own backend
// is left out.
func (p *PG) Sessions(ctx context.Context) ([]Session, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT usename::text, coalesce(application_name, ''), pid
  FROM pg_stat_activity
 WHERE datname = current_database() AND pid <> pg_backend_pid()
   AND usename LIKE 'nova\_%'
 ORDER BY pid`)
	if err != nil {
		return nil, fmt.Errorf("postgres: read the sessions holding the database: %w", err)
	}
	// ignored: rows.Err reports a read failure; closing a finished read cannot add one
	defer func() { _ = rows.Close() }()
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.Role, &s.Application, &s.PID); err != nil {
			return nil, fmt.Errorf("postgres: read the sessions holding the database: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: read the sessions holding the database: %w", err)
	}
	return out, nil
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
	ints := make([]sql.NullInt64, len(k.Fields))
	bools := make([]bool, len(k.Fields))
	for i, f := range k.Fields {
		switch f.Type {
		case TypeInt:
			dest = append(dest, &ints[i])
		case TypeBool:
			dest = append(dest, &bools[i])
		default:
			dest = append(dest, &texts[i])
		}
	}
	var created, updated time.Time
	dest = append(dest, &created, &updated)
	if err := scan(dest...); err != nil {
		return Row{}, err
	}
	for i, f := range k.Fields {
		switch f.Type {
		case TypeInt:
			if ints[i].Valid {
				row.Fields[f.Name] = strconv.FormatInt(ints[i].Int64, 10)
			} else {
				row.Fields[f.Name] = ""
			}
		case TypeBool:
			row.Fields[f.Name] = strconv.FormatBool(bools[i])
		default:
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
// number, a bool as a boolean, an empty optional ref as NULL (the foreign
// key allows no ”), any other value as text.
func fieldArg(f Field, v string) any {
	switch {
	case f.Nullable && v == "":
		return nil
	case f.Type == TypeInt:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case f.Type == TypeBool:
		return v == "true"
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
	// ignored: rows.Err reports a read failure; closing a finished read cannot add one
	defer func() { _ = rows.Close() }()
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

// historyReasonKey is where a history row's own JSON carries the write's
// reason: the after map (before, for a remove) holds it under this key and
// History moves it back to Change.Reason and out of the map, so a reader never
// sees it as a row field. The history columns are a migration's, and a new one
// would move the schema count a first run's transcript pins, so the reason
// rides in the schemaless JSON (pkg/config/store.go, Change.Reason).
const historyReasonKey = "reason"

// record appends the history row inside the write's transaction. The reason
// of the write is read from the context the verb put it on (WithReason), so
// every existing caller keeps its signature.
func record(ctx context.Context, tx *sql.Tx, kind, name, op string, before, after map[string]string, actor string) (int64, error) {
	if reason := ReasonFrom(ctx); reason != "" {
		// a copy, so the caller's row is not changed: the reason is the
		// write's, not a field of the row
		if after != nil {
			after = maps.Clone(after)
			after[historyReasonKey] = reason
		} else if before != nil {
			before = maps.Clone(before)
			before[historyReasonKey] = reason
		}
	}
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
	// ignored: a rollback after a commit is a no-op, and on a failure path the failure is the one returned
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
	maps.Copy(next.Fields, changes)
	if err := checkRefs(ctx, p, k, next); err != nil {
		return Row{}, 0, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Row{}, 0, fmt.Errorf("postgres: begin: %w", err)
	}
	// ignored: a rollback after a commit is a no-op, and on a failure path the failure is the one returned
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
	// ignored: a rollback after a commit is a no-op, and on a failure path the failure is the one returned
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
	// ignored: rows.Err reports a read failure; closing a finished read cannot add one
	defer func() { _ = rows.Close() }()
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
		// the reason rides in the row JSON (record): hand it back as the
		// write's, never as a row field
		if v, ok := c.After[historyReasonKey]; ok {
			c.Reason = v
			delete(c.After, historyReasonKey)
		}
		if v, ok := c.Before[historyReasonKey]; ok {
			if c.Reason == "" {
				c.Reason = v
			}
			delete(c.Before, historyReasonKey)
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
