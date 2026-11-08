//go:build functional

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// racingStore runs another session's statement on the store right after the
// tool's read, between the read and the write a set or remove makes.
type racingStore struct {
	Store
	other func()
}

func (r racingStore) List(ctx context.Context, k *kind) ([]Row, error) {
	rows, err := r.Store.List(ctx, k)
	r.other()
	return rows, err
}

// TestFunctionalStaleWrite (short: macOS caps the socket path under TMPDIR at 103 bytes): a set or remove whose row another
// session deleted or changed since the tool read it writes nothing, records no
// history and refuses, naming the row and the command to read it again.
func TestFunctionalStaleWrite(t *testing.T) {
	t.Parallel()
	dsn := startPG(t)
	env := map[string]string{"NOVA_PG_DSN": dsn}
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }() // ignored: closing the check connection
	for _, args := range [][]string{
		{"friend", "add", "f1", "--harness", "h", "--actor", "a1"},
		{"friend", "add", "f2", "--harness", "h", "--model", "a", "--actor", "a1"},
		{"friend", "add", "f3", "--harness", "h", "--actor", "a1"},
	} {
		code, _, errs := realRun(t, env, args...)
		require.Equal(t, 0, code, "%v: %s", args, errs)
	}
	racing := func(stmt string, args ...string) (int, string, string) {
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()
		var out, errs bytes.Buffer
		d := deps{getenv: func(k string) string { return env[k] }, hostname: func() (string, error) { return "m1", nil }, now: time.Now,
			openPG: func(ctx context.Context, dsn, password string) (Store, error) {
				st, err := openPG(ctx, dsn, password)
				if err != nil {
					return nil, err
				}
				return racingStore{st, func() { _, err := c.Exec(ctx, stmt); require.NoError(t, err, stmt) }}, nil
			}}
		code := run(ctx, args, d, &out, &errs)
		return code, out.String(), errs.String()
	}
	for _, tc := range []struct {
		stmt string
		args []string
		want string
	}{
		{"DELETE FROM config.friends WHERE name = 'f1'", []string{"friend", "set", "f1", "--width", "3", "--actor", "a1"},
			"nova-config friend set: friend f1 was changed or removed by another write since it was read; nothing written; run: nova-config friend show f1, then repeat the set\n"},
		{"UPDATE config.friends SET model = 'b' WHERE name = 'f2'", []string{"friend", "set", "f2", "--note", "hi", "--actor", "a1"},
			"nova-config friend set: friend f2 was changed or removed by another write since it was read; nothing written; run: nova-config friend show f2, then repeat the set\n"},
		{"UPDATE config.friends SET width = 5 WHERE name = 'f3'", []string{"friend", "remove", "f3", "--actor", "a1"},
			"nova-config friend remove: friend f3 was changed or removed by another write since it was read; nothing written; run: nova-config friend show f3, then repeat the remove\n"},
	} {
		code, out, errs := racing(tc.stmt, tc.args...)
		assert.Equal(t, 1, code, "%v: %s", tc.args, errs)
		assert.Empty(t, out, "%v", tc.args)
		assert.Equal(t, tc.want, errs, "%v", tc.args)
	}
	var model string
	require.NoError(t, c.QueryRow(ctx, "SELECT model FROM config.friends WHERE name = 'f2'").Scan(&model))
	assert.Equal(t, "b", model, "the other session's change stands")
	var n int
	require.NoError(t, c.QueryRow(ctx, "SELECT count(*) FROM config.friends WHERE name = 'f3'").Scan(&n))
	assert.Equal(t, 1, n, "a remove of a row changed since the read deletes nothing")
	require.NoError(t, c.QueryRow(ctx, "SELECT count(*) FROM config.history WHERE op <> 'add'").Scan(&n))
	assert.Equal(t, 0, n, "history records only writes that happened")
	code, _, errs := realRun(t, env, "friend", "set", "f2", "--note", "hi", "--actor", "a1")
	assert.Equal(t, 0, code, "after reading again the set goes through: %s", errs)
}
