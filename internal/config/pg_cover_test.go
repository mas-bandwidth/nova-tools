package config

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit tier reaches pg.go through database/sql alone: a fake driver
// answers each query from the test's handler, so no test here opens a socket,
// starts a process or touches a real Postgres. A function whose only path
// needs a live server (openPGWithin's ping, and the pool it returns) is left
// to the functional tier, and the report names it.

type fakeHandler func(q string, args []driver.NamedValue) ([]string, [][]driver.Value, error)

type fakeStore struct{ h fakeHandler }

type fakeConn struct{ s *fakeStore }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("fake: Prepare") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return c, nil }
func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c, nil
}
func (c *fakeConn) Commit() error              { return nil }
func (c *fakeConn) Rollback() error            { return nil }
func (c *fakeConn) Ping(context.Context) error { return nil }

func (c *fakeConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	cols, rows, err := c.s.h(q, args)
	if err != nil {
		return nil, err
	}
	return &fakeRows{cols: cols, rows: rows}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if _, _, err := c.s.h(q, args); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("fake: Open") }

type fakeConnector struct{ s *fakeStore }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakeConn{s: c.s}, nil
}
func (c fakeConnector) Driver() driver.Driver { return fakeDriver{} }

// fakePG is a *PG whose pool is the fake driver, the handler answering every
// query.
func fakePG(t *testing.T, h fakeHandler) *PG {
	t.Helper()
	db := sql.OpenDB(fakeConnector{s: &fakeStore{h: h}})
	t.Cleanup(func() { _ = db.Close() })
	return &PG{db: db}
}

func reply(cols []string, rows ...[]driver.Value) ([]string, [][]driver.Value, error) {
	return cols, rows, nil
}

func fail(err error) ([]string, [][]driver.Value, error) { return nil, nil, err }

func unexpected(q string) ([]string, [][]driver.Value, error) {
	return fail(errors.New("unexpected query: " + q))
}

// friendCols and friendRow are the columns(k), created_at, updated_at scanRow
// reads for the friend kind.
var friendCols = []string{"name", "slots", "tiers", "roles", "width", "mode", "config_dir", "token_cap", "streams", "kinds", "dir", "billing", "created_at", "updated_at"}

func friendRow(name string) []driver.Value {
	at := time.Unix(0, 0).UTC()
	return []driver.Value{name, int64(2), "flash", "", int64(8), "batch", nil, int64(6000000), "", "", nil, "subscription", at, at}
}

// TestPgCoverRedact: Redact names the user, host, port and database, and
// refuses to guess on a DSN pgconn cannot parse.
func TestPgCoverRedact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{"url with user and port", "postgres://bob:sekrit@nova:5432/nova", "bob@nova:5432/nova"},
		{"keyword with user", "host=nova port=5432 user=bob dbname=nova", "bob@nova:5432/nova"},
		{"unparsable", "postgres://%zz", "(unparsed dsn)"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Redact(tc.dsn))
		})
	}
}

// TestPgCoverQuoteIdent: an identifier is quoted, an embedded quote doubled.
func TestPgCoverQuoteIdent(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `"name"`, quoteIdent("name"))
	assert.Equal(t, `"a""b"`, quoteIdent(`a"b`))
}

// TestPgCoverColumns: columns is the quoted name then every field, in order.
func TestPgCoverColumns(t *testing.T) {
	t.Parallel()
	k := &Kind{Fields: []Field{{Name: "a"}, {Name: "b"}}}
	assert.Equal(t, `"name", "a", "b"`, columns(k))
	assert.Equal(t, `"name"`, columns(&Kind{}))
}

// TestPgCoverFieldArg: a nullable empty is NULL, an int is a number, a bool is
// a boolean, an empty optional ref is NULL, anything else is its text.
func TestPgCoverFieldArg(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		f    Field
		v    string
		want any
	}{
		{"nullable empty", Field{Type: TypeText, Nullable: true}, "", nil},
		{"int", Field{Type: TypeInt}, "3", int64(3)},
		{"int unparsable is zero", Field{Type: TypeInt}, "x", int64(0)},
		{"bool true", Field{Type: TypeBool}, "true", true},
		{"bool false", Field{Type: TypeBool}, "false", false},
		{"empty optional ref", Field{Type: TypeRef, Ref: KindFriend}, "", nil},
		{"text", Field{Type: TypeText}, "x", "x"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, fieldArg(tc.f, tc.v))
		})
	}
}

// TestPgCoverValues: the arguments are the name then every field, in column
// order.
func TestPgCoverValues(t *testing.T) {
	t.Parallel()
	k, ok := Lookup(KindFriend)
	require.True(t, ok)
	row := Row{Name: "f1", Fields: map[string]string{"slots": "2", "tiers": "flash", "roles": "", "width": "8", "mode": "one-shot", "billing": "api"}}
	assert.Equal(t, []any{"f1", int64(2), "flash", "", int64(8), "one-shot", nil, int64(0), "", "", nil, "api"}, values(k, row), "an unset config_dir is NULL, an unset token_cap binds as 0, and an unset dir is NULL")
	row.Fields["config_dir"] = "/accounts/heavy-a"
	assert.Equal(t, []any{"f1", int64(2), "flash", "", int64(8), "one-shot", "/accounts/heavy-a", int64(0), "", "", nil, "api"}, values(k, row))
	row.Fields["token_cap"] = "6000000"
	assert.Equal(t, []any{"f1", int64(2), "flash", "", int64(8), "one-shot", "/accounts/heavy-a", int64(6000000), "", "", nil, "api"}, values(k, row))
	row.Fields["dir"] = "/accounts/heavy-a/working"
	assert.Equal(t, []any{"f1", int64(2), "flash", "", int64(8), "one-shot", "/accounts/heavy-a", int64(6000000), "", "", "/accounts/heavy-a/working", "api"}, values(k, row))
}

// TestPgCoverKindOf: a known kind is returned, an unknown one refused.
func TestPgCoverKindOf(t *testing.T) {
	t.Parallel()
	k, err := kindOf(KindFriend)
	require.NoError(t, err)
	assert.Equal(t, KindFriend, k.Name)
	_, err = kindOf("nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown kind "nope"`)
}

// TestPgCoverNullJSON: nil stays SQL NULL, bytes become their string.
func TestPgCoverNullJSON(t *testing.T) {
	t.Parallel()
	assert.Nil(t, nullJSON(nil))
	assert.Equal(t, "{}", nullJSON([]byte("{}")))
}

// TestPgCoverSQLState: a pgx error's code is returned, any other error gives
// the empty string.
func TestPgCoverSQLState(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "23505", sqlState(&pgconn.PgError{Code: "23505"}))
	assert.Equal(t, "", sqlState(errors.New("plain")))
}

// TestPgCoverScanRowRefusesAScanError: scanRow returns the scan error.
func TestPgCoverScanRowRefusesAScanError(t *testing.T) {
	t.Parallel()
	k, _ := Lookup(KindFriend)
	_, err := scanRow(k, func(...any) error { return errors.New("scan") })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan")
}

// TestPgCoverScanRowReadsNullsAndBools: a NULL int is the empty value and a
// bool is true or false.
func TestPgCoverScanRowReadsNullsAndBools(t *testing.T) {
	t.Parallel()
	k := &Kind{Fields: []Field{{Name: "n", Type: TypeInt}, {Name: "b", Type: TypeBool}, {Name: "t", Type: TypeText}}}
	at := time.Unix(0, 0).UTC()
	scan := func(dest ...any) error {
		*(dest[0].(*string)) = "row"
		// n is a NULL int: left invalid
		*(dest[2].(*bool)) = true
		*(dest[3].(*sql.NullString)) = sql.NullString{String: "x", Valid: true}
		*(dest[4].(*time.Time)) = at
		*(dest[5].(*time.Time)) = at
		return nil
	}
	row, err := scanRow(k, scan)
	require.NoError(t, err)
	assert.Equal(t, "row", row.Name)
	assert.Equal(t, "", row.Fields["n"])
	assert.Equal(t, "true", row.Fields["b"])
	assert.Equal(t, "x", row.Fields["t"])
}

// TestPgCoverMigrations: the embedded migrations are numbered, sorted and
// named NNNN_<what>.sql.
func TestPgCoverMigrations(t *testing.T) {
	t.Parallel()
	all, err := Migrations()
	require.NoError(t, err)
	require.NotEmpty(t, all)
	for i, m := range all {
		assert.Positive(t, m.Version)
		assert.Contains(t, m.Name, "_")
		if i > 0 {
			assert.Less(t, all[i-1].Version, m.Version)
		}
	}
}

// TestPgCoverOpenPGRefusesABadDSN: a DSN pgconn cannot parse is refused before
// any connection is opened.
func TestPgCoverOpenPGRefusesABadDSN(t *testing.T) {
	t.Parallel()
	_, err := OpenPG(context.Background(), "postgres://%zz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres dsn")
}

// TestPgCoverOpenPGWithinRefusesABadDSN: the bounded opener refuses the same
// way, its fallback never reached.
func TestPgCoverOpenPGWithinRefusesABadDSN(t *testing.T) {
	t.Parallel()
	_, err := openPGWithin(context.Background(), "postgres://%zz", time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres dsn")
}

// TestPgCoverClose: Close closes the pool.
func TestPgCoverClose(t *testing.T) {
	t.Parallel()
	p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
		return unexpected(q)
	})
	require.NoError(t, p.Close())
}

// TestPgCoverVersion: the greatest applied version, 0 before the ledger table
// exists; a query failure is returned.
func TestPgCoverVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			switch {
			case strings.Contains(q, "information_schema.tables"):
				return reply([]string{"exists"}, []driver.Value{true})
			case strings.Contains(q, "max(version)"):
				return reply([]string{"max"}, []driver.Value{int64(5)})
			}
			return unexpected(q)
		})
		v, err := p.Version(ctx)
		require.NoError(t, err)
		assert.Equal(t, 5, v)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply([]string{"exists"}, []driver.Value{false})
		})
		v, err := p.Version(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, v)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("db down"))
		})
		_, err := p.Version(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read schema version")
	}
}

// TestPgCoverApplied: no ledger before the first migrate; every recorded
// version after it; a query failure is returned.
func TestPgCoverApplied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			switch {
			case strings.Contains(q, "information_schema.tables"):
				return reply([]string{"exists"}, []driver.Value{true})
			case strings.Contains(q, "max(version)"):
				return reply([]string{"max"}, []driver.Value{int64(2)})
			case strings.Contains(q, "ORDER BY version"):
				return reply([]string{"version"}, []driver.Value{int64(1)}, []driver.Value{int64(2)})
			}
			return unexpected(q)
		})
		got, err := p.Applied(ctx)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2}, got)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply([]string{"exists"}, []driver.Value{false})
		})
		got, err := p.Applied(ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	}
}

// TestPgCoverRev: the greatest history id of a kind; a query failure is
// returned.
func TestPgCoverRev(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply([]string{"max"}, []driver.Value{int64(7)})
		})
		got, err := p.Rev(ctx, KindFriend)
		require.NoError(t, err)
		assert.Equal(t, int64(7), got)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("db down"))
		})
		_, err := p.Rev(ctx, KindFriend)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rev friend")
	}
}

// TestPgCoverCounts: one count per kind that has many; a query failure is
// returned.
func TestPgCoverCounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply([]string{"count"}, []driver.Value{int64(2)})
		})
		got, err := p.Counts(ctx)
		require.NoError(t, err)
		for _, k := range Kinds {
			if !k.Singleton {
				assert.Equal(t, 2, got[k.Name], k.Name)
			}
		}
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("db down"))
		})
		_, err := p.Counts(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count")
	}
}

// TestPgCoverOwnership: the catalog row names the role, the schema owner, the
// create right and each table's owner; a query failure is returned.
func TestPgCoverOwnership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cols := []string{"role", "schema_owner", "create", "table", "owner"}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			if strings.Contains(q, "pg_namespace") {
				return reply(cols,
					[]driver.Value{"nova_config", "nova_admin", true, "fleet", "nova_admin"},
					[]driver.Value{"nova_config", "nova_admin", true, "", ""})
			}
			return unexpected(q)
		})
		o, err := p.Ownership(ctx)
		require.NoError(t, err)
		assert.Equal(t, "nova_config", o.Role)
		assert.Equal(t, "nova_admin", o.SchemaOwner)
		assert.True(t, o.Create)
		assert.Equal(t, map[string]string{"fleet": "nova_admin"}, o.Tables)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("db down"))
		})
		_, err := p.Ownership(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "owners of schema config")
	}
}

// TestPgCoverGet: a held row is read and decoded; no row is not found; an
// unknown kind and a query failure are returned.
func TestPgCoverGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply(friendCols, friendRow("f1"))
		})
		row, found, err := p.Get(ctx, KindFriend, "f1")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "f1", row.Name)
		assert.Equal(t, "2", row.Fields["slots"])
		assert.Equal(t, "8", row.Fields["width"])
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply(friendCols)
		})
		_, found, err := p.Get(ctx, KindFriend, "gone")
		require.NoError(t, err)
		assert.False(t, found)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return unexpected(q)
		})
		_, _, err := p.Get(ctx, "nope", "x")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown kind")
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("db down"))
		})
		_, _, err := p.Get(ctx, KindFriend, "f1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read friend f1")
	}
}

// TestPgCoverList: every row of a kind is read; an unknown kind and a query
// failure are returned.
func TestPgCoverList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply(friendCols, friendRow("f1"), friendRow("f2"))
		})
		rows, err := p.List(ctx, KindFriend)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		assert.Equal(t, "f1", rows[0].Name)
		assert.Equal(t, "f2", rows[1].Name)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return unexpected(q)
		})
		_, err := p.List(ctx, "nope")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown kind")
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("db down"))
		})
		_, err := p.List(ctx, KindFriend)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list friend")
	}
}

// TestPgCoverRecord: a history row is appended and its id returned; a query
// failure is returned.
func TestPgCoverRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply([]string{"id"}, []driver.Value{int64(11)})
		})
		tx, err := p.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		id, err := record(ctx, tx, KindFriend, "f1", OpAdd, nil, map[string]string{"slots": "2"}, "actor")
		require.NoError(t, err)
		assert.Equal(t, int64(11), id)
		require.NoError(t, tx.Rollback())
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("db down"))
		})
		tx, err := p.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = record(ctx, tx, KindFriend, "f1", OpAdd, nil, nil, "actor")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "record history")
		require.NoError(t, tx.Rollback())
	}
}

// TestPgCoverInsert: a row is added and its history id returned; a duplicate
// is ErrExists, a missing ref ErrNoRef, and a failed record is returned.
func TestPgCoverInsert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	row := Row{Name: "f1", Fields: map[string]string{"slots": "2", "tiers": "flash", "roles": "", "width": "8"}}
	okHandler := func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
		if strings.Contains(q, "INSERT INTO config.history") {
			return reply([]string{"id"}, []driver.Value{int64(9)})
		}
		return reply(nil)
	}
	{
		p := fakePG(t, okHandler)
		id, err := p.Insert(ctx, KindFriend, row, "actor")
		require.NoError(t, err)
		assert.Equal(t, int64(9), id)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(&pgconn.PgError{Code: "23505"})
		})
		_, err := p.Insert(ctx, KindFriend, row, "actor")
		require.ErrorIs(t, err, ErrExists)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(&pgconn.PgError{Code: "23503"})
		})
		_, err := p.Insert(ctx, KindFriend, row, "actor")
		require.ErrorIs(t, err, ErrNoRef)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			if strings.Contains(q, "INSERT INTO config.history") {
				return fail(errors.New("db down"))
			}
			return reply(nil)
		})
		_, err := p.Insert(ctx, KindFriend, row, "actor")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "add friend f1")
	}
	{
		p := fakePG(t, okHandler)
		_, err := p.Insert(ctx, "nope", row, "actor")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown kind")
	}
}

// TestPgCoverUpdate: a held row is changed and the row after returned; a
// missing row is ErrNotFound, a missing ref ErrNoRef.
func TestPgCoverUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	handler := func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
		switch {
		case strings.Contains(q, "WHERE name = $1"):
			return reply(friendCols, friendRow("f1"))
		case strings.Contains(q, "INSERT INTO config.history"):
			return reply([]string{"id"}, []driver.Value{int64(3)})
		}
		return reply(nil)
	}
	{
		p := fakePG(t, handler)
		row, id, err := p.Update(ctx, KindFriend, "f1", map[string]string{"width": "9"}, "actor")
		require.NoError(t, err)
		assert.Equal(t, "f1", row.Name)
		assert.Equal(t, int64(3), id)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply(friendCols)
		})
		_, _, err := p.Update(ctx, KindFriend, "gone", nil, "actor")
		require.ErrorIs(t, err, ErrNotFound)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			if strings.HasPrefix(q, "UPDATE ") {
				return fail(&pgconn.PgError{Code: "23503"})
			}
			if strings.Contains(q, "WHERE name = $1") {
				return reply(friendCols, friendRow("f1"))
			}
			if strings.Contains(q, "INSERT INTO config.history") {
				return reply([]string{"id"}, []driver.Value{int64(3)})
			}
			return unexpected(q)
		})
		_, _, err := p.Update(ctx, KindFriend, "f1", map[string]string{"width": "9"}, "actor")
		require.ErrorIs(t, err, ErrNoRef)
	}
	{
		p := fakePG(t, handler)
		_, _, err := p.Update(ctx, "nope", "f1", nil, "actor")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown kind")
	}
}

// TestPgCoverDelete: a held row is removed and its history id returned; a
// missing row is ErrNotFound, a referenced row ErrReferenced.
func TestPgCoverDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	handler := func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
		switch {
		case strings.Contains(q, "WHERE name = $1"):
			return reply(friendCols, friendRow("f1"))
		case strings.Contains(q, `FROM config."sprint"`):
			return reply(nil)
		case strings.Contains(q, "INSERT INTO config.history"):
			return reply([]string{"id"}, []driver.Value{int64(4)})
		}
		return reply(nil)
	}
	{
		p := fakePG(t, handler)
		id, err := p.Delete(ctx, KindFriend, "f1", "actor")
		require.NoError(t, err)
		assert.Equal(t, int64(4), id)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply(friendCols)
		})
		_, err := p.Delete(ctx, KindFriend, "gone", "actor")
		require.ErrorIs(t, err, ErrNotFound)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			if strings.HasPrefix(q, "DELETE ") {
				return fail(&pgconn.PgError{Code: "23503"})
			}
			if strings.Contains(q, "WHERE name = $1") {
				return reply(friendCols, friendRow("f1"))
			}
			if strings.Contains(q, `FROM config."sprint"`) {
				return reply(nil)
			}
			if strings.Contains(q, "INSERT INTO config.history") {
				return reply([]string{"id"}, []driver.Value{int64(4)})
			}
			return unexpected(q)
		})
		_, err := p.Delete(ctx, KindFriend, "f1", "actor")
		require.ErrorIs(t, err, ErrReferenced)
	}
}

// TestPgCoverHistory: the change rows are read and decoded; a query failure is
// returned.
func TestPgCoverHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cols := []string{"id", "kind", "name", "op", "before", "after", "actor", "at"}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			at := time.Unix(0, 0).UTC()
			return reply(cols,
				[]driver.Value{int64(1), KindFriend, "f1", OpAdd, nil, `{"slots":"2"}`, "actor", at},
				[]driver.Value{int64(2), KindFriend, "f1", OpSet, `{"slots":"2"}`, nil, "actor", at})
		})
		got, err := p.History(ctx, KindFriend, "f1")
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, OpAdd, got[0].Op)
		assert.Equal(t, "2", got[0].After["slots"])
		assert.Equal(t, "2", got[1].Before["slots"])
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			at := time.Unix(0, 0).UTC()
			return reply(cols, []driver.Value{int64(1), KindFriend, "f1", OpAdd, "{not json", nil, "actor", at})
		})
		_, err := p.History(ctx, KindFriend, "f1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "before")
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("db down"))
		})
		_, err := p.History(ctx, KindFriend, "f1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "history friend f1")
	}
}

// TestPgCoverApplyOne: one migration is applied and recorded; an exec failure
// is returned naming the migration.
func TestPgCoverApplyOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := Migration{Version: 1, Name: "0001_x.sql", SQL: "SELECT 1"}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply(nil)
		})
		require.NoError(t, p.applyOne(ctx, m))
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			if strings.Contains(q, "SELECT 1") {
				return fail(errors.New("syntax"))
			}
			return reply(nil)
		})
		err := p.applyOne(ctx, m)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "migration 1 (0001_x.sql)")
	}
}

// TestPgCoverGrantRead: the grant runs; an exec failure is returned.
func TestPgCoverGrantRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return reply(nil)
		})
		require.NoError(t, p.grantRead(ctx))
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("permission denied"))
		})
		err := p.grantRead(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "grant "+readRole)
	}
}

// TestPgCoverMigrate: with every migration already recorded migrate applies
// nothing and still grants; a version read failure is returned.
func TestPgCoverMigrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	all, err := Migrations()
	require.NoError(t, err)
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			switch {
			case strings.Contains(q, "information_schema.tables"):
				return reply([]string{"exists"}, []driver.Value{true})
			case strings.Contains(q, "max(version)"):
				return reply([]string{"max"}, []driver.Value{int64(len(all))})
			}
			return reply(nil)
		})
		from, to, applied, err := p.Migrate(ctx)
		require.NoError(t, err)
		assert.Equal(t, len(all), from)
		assert.Equal(t, len(all), to)
		assert.Empty(t, applied)
	}
	{
		p := fakePG(t, func(q string, _ []driver.NamedValue) ([]string, [][]driver.Value, error) {
			return fail(errors.New("db down"))
		})
		_, _, _, err := p.Migrate(ctx)
		require.Error(t, err)
	}
}
