package config

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRows are the text rows a query returns.
type fakeRows struct {
	rows [][]string
	at   int
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool                                   { r.at++; return r.at <= len(r.rows) }
func (r *fakeRows) Scan(...any) error                            { return errors.New("not used") }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) TypeMap() *pgtype.Map                         { return nil }
func (r *fakeRows) Values() ([]any, error) {
	var out []any
	for _, v := range r.rows[r.at-1] {
		out = append(out, v)
	}
	return out, nil
}

// fakeTx answers each table's query from its rows and records what happened.
type fakeTx struct {
	tables            map[string][][]string
	queries           []string
	committed, failed bool
	fail              string // a table whose query fails
}

func (f *fakeTx) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	f.queries = append(f.queries, sql)
	for t, rows := range f.tables {
		if strings.Contains(sql, " FROM config."+t+" ") {
			if t == f.fail {
				return nil, &pgconn.PgError{Code: "42P01"}
			}
			return &fakeRows{rows: rows}, nil
		}
	}
	return nil, errors.New("unknown table")
}
func (f *fakeTx) Commit(context.Context) error   { f.committed = true; return nil }
func (f *fakeTx) Rollback(context.Context) error { f.failed = !f.committed; return nil }

func snapshotStore(tx *fakeTx) *Store {
	return &Store{user: "u", begin: func(context.Context) (transaction, error) { return tx, nil }}
}

func TestSnapshotReadsAllThreeInOneTransaction(t *testing.T) {
	t.Parallel()
	stamp := []string{"2026-11-06T00:00:00Z", "2026-11-06T00:00:01Z"}
	tx := &fakeTx{tables: map[string][][]string{
		"machines": {append([]string{"m1", "", "nova", "linux", "amd64", "8", "bench", "0", ""}, stamp...)},
		"friends":  {append([]string{"emma", "claude", "opus", "4", "heavy,pro", "she/her", "", "true"}, stamp...)},
		"services": {append([]string{"s1", "m1", `["/p"]`, "", "", "60", "false", "true", "{}", ""}, stamp...)},
	}}
	snap, err := snapshotStore(tx).Snapshot(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Machines, 1)
	assert.Equal(t, 8, *snap.Machines[0].Cores)
	assert.Equal(t, Friend{Name: "emma", Harness: "claude", Model: "opus", Width: 4, Tiers: "heavy,pro", Pronouns: "she/her", Enabled: true, Created: stamp[0], Updated: stamp[1]}, snap.Friends[0])
	assert.Equal(t, []string{"/p"}, snap.Services[0].Argv)
	assert.Equal(t, []string{listSQL("machines", Columns[Machine]()), listSQL("friends", Columns[Friend]()), listSQL("services", Columns[Service]())}, tx.queries, "the scanners' own queries, all on the transaction")
	assert.True(t, tx.committed)
	assert.Equal(t, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, snapshotTx)
}

func TestSnapshotFailureRollsBack(t *testing.T) {
	t.Parallel()
	tx := &fakeTx{tables: map[string][][]string{"machines": nil, "friends": nil, "services": nil}, fail: "friends"}
	_, err := snapshotStore(tx).Snapshot(context.Background())
	assert.EqualError(t, err, SchemaMissing)
	assert.False(t, tx.committed)
	assert.True(t, tx.failed, "rolled back")
	st := &Store{user: "u", begin: func(context.Context) (transaction, error) { return nil, errors.New("down") }}
	_, err = st.Snapshot(context.Background())
	assert.EqualError(t, err, "postgres: connect failed")
}
