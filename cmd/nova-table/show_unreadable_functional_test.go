//go:build functional

package main

import (
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// show is the record of the table: a cell that cannot be read prints as ?, and
// show says which key holds what, on stderr, and exits 1. render and watch keep
// drawing the ? and exit 0, as documented.
func TestShowNamesAnUnreadableCellAndExitsNonZero(t *testing.T) {
	t.Parallel()
	addr, _ := batchFixture(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	require.NoError(t, c.Set(t.Context(), "table:demo:cell:build:working", "s", 0).Err())
	code, stdout, stderr := runTable("show", "--redis", addr, "demo")
	require.EqualValues(t, 1, code, "exit %d, want 1\n%s%s", code, stdout, stderr)
	assert.Contains(t, stdout, "TABLE ROW table=demo row=build ready=1 working=? done=0", "show does not draw the unread cell as ?:\n%s", stdout)
	for _, w := range []string{`table "demo" row "build" column "working" cannot be read: key table:demo:cell:build:working is string, expected zset`, "1 cell(s) printed as ?", "; run: nova-table check 'demo'"} {
		assert.Contains(t, stderr, w, "stderr lacks %q:\n%s", w, stderr)
	}
	{
		code, _, _ := runTable("render", "--redis", addr, "demo")
		assert.EqualValues(t, 0, code, "render exits %d; it draws the ? and exits 0", code)
	}
	// a sound table is untouched
	require.NoError(t, c.Del(t.Context(), "table:demo:cell:build:working").Err())
	{
		code, _, stderr := runTable("show", "--redis", addr, "demo")
		assert.EqualValues(t, 0, code, "show of a sound table: exit %d %s", code, stderr)
		assert.Empty(t, stderr, "show of a sound table: exit %d %s", code, stderr)
	}
}
