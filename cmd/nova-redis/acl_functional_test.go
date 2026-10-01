//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

// TestACLVerbsOnARedisServer applies the rendered users to a real store and
// reads them back: check says MISSING, apply sets every user, check then
// says OK (the store's own words for the rules expand to the same sets),
// and each role can do what its tools do and nothing past it: the member
// writes a table through the library and the sprint's keys, the table
// reader reads and cannot write, and neither can touch a key outside its
// families, flush the store or load or flush the library.
func TestACLVerbsOnARedisServer(t *testing.T) {
	t.Parallel()
	const pw = "pw-from-nova-secrets"
	addr := testredis.Start(t, "--requirepass", pw)
	d := realDeps()
	d.getenv = func(k string) string {
		if k == PasswordEnv {
			return pw
		}
		return ""
	}
	acl := func(args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(append([]string{"acl"}, append(args, "--addr", addr)...), &out, &errb, d)
		return code, out.String() + errb.String()
	}
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: addr, Password: pw})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, fn.Load(ctx, admin))

	code, out := acl("check")
	require.Equal(t, 1, code, out)
	assert.Equal(t, 4, strings.Count(out, "ACL MISSING "), out)
	code, out = acl("apply")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "ACL APPLY OK users=4 set=4 saved=no-acl-file")
	code, out = acl("check")
	require.Equal(t, 0, code, out)
	assert.Equal(t, 4, strings.Count(out, "ACL OK "), out)

	// The tools' users get their seats' passwords by hand here; the plays
	// never set one.
	as := func(user string) *redis.Client {
		require.NoError(t, admin.Do(ctx, "ACL", "SETUSER", user, ">"+user+"-pw").Err())
		c := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: user + "-pw"})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	member, reader := as("bench"), as("ns-table")
	cols, err := ntable.ParseColumns("a,b")
	require.NoError(t, err)
	require.NoError(t, ntable.Create(ctx, member, ntable.Table{Name: "work", Columns: cols}, time.Now()))
	_, err = ntable.RowAdd(ctx, member, "work", "r1", ntable.RowSpec{})
	require.NoError(t, err)
	require.NoError(t, member.Set(ctx, "sprint:epoch-probe", "1", 0).Err())
	got, err := ntable.Read(ctx, reader, "work")
	require.NoError(t, err)
	assert.Len(t, got.Rows, 1)
	assert.Equal(t, "PONG", member.FCall(ctx, "ns_ping", nil).Val())

	refused := []struct {
		name string
		c    *redis.Client
		cmd  []any
	}{
		{"member outside its families", member, []any{"SET", "elsewhere:k", "1"}},
		{"member flushes", member, []any{"FLUSHALL"}},
		{"member flushes the library", member, []any{"FUNCTION", "FLUSH"}},
		{"member loads a library", member, []any{"FUNCTION", "LOAD", "REPLACE", "#!lua name=x\nredis.register_function('x', function() end)"}},
		{"member calls a coordinator function", member, []any{"FCALL", "ns_capacity_machine", "0"}},
		{"member evals", member, []any{"EVAL", "return 1", "0"}},
		{"reader writes a key", reader, []any{"SET", "sprint:x", "1"}},
		{"reader calls a writer", reader, []any{"FCALL", "ns_ping", "0"}},
		{"reader lists keys", reader, []any{"KEYS", "*"}},
	}
	for _, tc := range refused {
		err := tc.c.Do(ctx, tc.cmd...).Err()
		require.Error(t, err, tc.name)
		assert.Contains(t, err.Error(), "NOPERM", tc.name)
	}
	_, err = ntable.RowAdd(ctx, reader, "work", "r2", ntable.RowSpec{})
	assert.Error(t, err, "the reader added a row")

	// A user changed by hand drifts, and apply puts back only it.
	require.NoError(t, admin.Do(ctx, "ACL", "SETUSER", "bench", "~elsewhere:*").Err())
	code, out = acl("check")
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "ACL DRIFT user=bench role=member keys-=~elsewhere:*")
	code, out = acl("apply")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "ACL APPLY OK users=4 set=1 ")
	assert.Equal(t, "OK", member.Set(ctx, "sprint:after", "1", 0).Val(), "apply kept the user's password")
}
