//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// live is a throwaway redis-server with the nova_sprint library loaded
// (table.lua registers the table operations), as the default user.
func live(t *testing.T, extra ...string) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t, extra...)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, fn.Load(context.Background(), c))
	return addr, c
}

// TestMoveAndClearAreOneCallEach: cell move is one FCALL that keeps the
// member's score, refuses NOTMEMBER and writes nothing then; clear is one
// FCALL that empties every owned cell and removes every row, keeping the
// definition, and a table with any bound cell is refused whole.
func TestMoveAndClearAreOneCallEach(t *testing.T) {
	t.Parallel()

	_, c := live(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), time.Now()))
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "b1", 7); err != nil {
		t.Fatal(err)
	}
	if n, err := ntable.CellMove(ctx, c, "demo", "build", "ready", "working", "b1"); err != nil || n != 1 {
		t.Fatalf("cell move: n=%d err=%v", n, err)
	}
	if score, err := c.ZScore(ctx, ntable.CellKey("demo", "build", "working"), "b1").Result(); err != nil || score != 7 {
		t.Fatalf("the moved member's score = %v %v, want 7 kept", score, err)
	}
	if n, err := c.ZCard(ctx, ntable.CellKey("demo", "build", "ready")).Result(); err != nil || n != 0 {
		t.Fatalf("the member is still in ready: %d %v", n, err)
	}
	if _, err := ntable.CellMove(ctx, c, "demo", "build", "ready", "working", "b1"); !errors.Is(err, ntable.ErrNotMember) {
		t.Fatalf("second move: %v, want ErrNotMember", err)
	}
	if n, err := c.ZCard(ctx, ntable.CellKey("demo", "build", "working")).Result(); err != nil || n != 1 {
		t.Fatalf("a refused move wrote: %d %v", n, err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "test", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "test", "done", "t1", 1); err != nil {
		t.Fatal(err)
	}
	if n, err := ntable.Clear(ctx, c, "demo"); err != nil || n != 2 {
		t.Fatalf("clear: rows=%d err=%v", n, err)
	}
	for _, pattern := range []string{"table:demo:row:*", "table:demo:cell:*"} {
		if keys, err := c.Keys(ctx, pattern).Result(); err != nil || len(keys) != 0 {
			t.Fatalf("clear left owned keys %v: %v", keys, err)
		}
	}
	tb, err := ntable.Read(ctx, c, "demo")
	if err != nil || len(tb.Rows) != 0 || len(tb.Columns) != 4 {
		t.Fatalf("read after clear: %+v %v", tb, err)
	}
	_, err = ntable.Clear(ctx, c, "nope")
	require.ErrorIs(t, err, ntable.ErrNoTable, "clear of no table: %v, want ErrNoTable", err)
	// a bound cell refuses the whole clear, naming the owner, and nothing
	// is cleared
	_, err = ntable.RowAdd(ctx, c, "demo", "owned", ntable.RowSpec{})
	require.NoError(t, err)
	_, err = ntable.CellAdd(ctx, c, "demo", "owned", "ready", "o1", 1)
	require.NoError(t, err)
	_, err = ntable.RowAdd(ctx, c, "demo", "view", ntable.RowSpec{Binds: map[string]string{"ready": "ws:s:ready"}, Owner: "nova-sprint task move"})
	require.NoError(t, err)
	_, err = ntable.Clear(ctx, c, "demo")
	var bound *ntable.BoundError
	if !errors.As(err, &bound) || bound.Row != "view" || bound.Col != "ready" || bound.Key != "ws:s:ready" || bound.Owner != "nova-sprint task move" {
		t.Fatalf("clear over a bound cell: %v", err)
	}
	require.True(t, strings.HasSuffix(err.Error(), "demo.view.ready is bound to ws:s:ready, owned elsewhere; run: nova-sprint task move"), "bound refusal reads %q", err)
	if n, err := c.ZCard(ctx, ntable.CellKey("demo", "owned", "ready")).Result(); err != nil || n != 1 {
		t.Fatalf("a refused clear cleared the owned cell: %d %v", n, err)
	}
	if n, err := c.ZCard(ctx, ntable.RowsKey("demo")).Result(); err != nil || n != 2 {
		t.Fatalf("a refused clear removed rows: %d %v", n, err)
	}
	_, err = ntable.CellMove(ctx, c, "demo", "view", "ready", "working", "x")
	require.ErrorAs(t, err, &bound, "cell move out of a bound cell: %v, want BoundError", err)
}

// tableGrants are the ACL tokens a seat needs to write and read tables
// through ntable, exactly: the key roots (~table:* and the tables registry)
// and the function calls and underlying commands used by the library.
var tableGrants = []string{
	"~table:*", "~tables", "~ws:*",
	"+hdel", "+type", "+xadd", "+xinfo|stream", "+hset", "+hget", "+hgetall", "+del", "+exists",
	// a batch reads a member's head and the fields its entries name, by name
	"+hmget", "+hlen", "+hstrlen", "+hexists",
	"+sadd", "+srem", "+smembers", "+scard", "+sismember",
	"+zadd", "+zrem", "+zrange", "+zcard", "+zscore",
	"+fcall|" + ntable.FnMove, "+fcall|" + ntable.FnClear,
	"+fcall|ns_table_create", "+fcall|ns_table_drop", "+fcall|ns_table_row_add", "+fcall|ns_table_row_del", "+fcall|ns_table_cell_add", "+fcall|ns_table_cell_remove", "+fcall|ns_table_cell_move", "+fcall|ns_table_bind", "+fcall|ns_table_apply", "+fcall_ro|ns_table_read", "+fcall_ro|ns_table_read_set", "+fcall_ro|ns_table_list", "+fcall_ro|ns_table_members",
}

// TestTableGrantsAreExactlyWhatTheWriterNeeds runs every write and read of
// the library as a throwaway user holding tableGrants and nothing else
// (resetkeys -@all +ping, the shape of every ns-* row), and proves each
// clear grant is load-bearing: a user without it is refused that call
// while its other table operations still work.
func TestTableGrantsAreExactlyWhatTheWriterNeeds(t *testing.T) {
	t.Parallel()

	extra := []string{"--user", "default", "on", "nopass", "~*", "&*", "+@all"}
	full := append([]string{"--user", "ns-writer", "on", ">pw", "resetkeys", "resetchannels", "-@all", "+ping"}, tableGrants...)
	extra = append(extra, full...)
	noClear := []string{"--user", "ns-noclear", "on", ">pw", "resetkeys", "resetchannels", "-@all", "+ping"}
	for _, g := range tableGrants {
		if g != "+fcall|"+ntable.FnClear {
			noClear = append(noClear, g)
		}
	}
	extra = append(extra, noClear...)
	addr, _ := live(t, extra...)
	ctx := context.Background()
	as := func(user string) *redis.Client {
		c := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: "pw"})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	w := as("ns-writer")
	require.NoError(t, ntable.Create(ctx, w, demo(), time.Now()), "create as the writer")
	if _, err := ntable.RowAdd(ctx, w, "demo", "build", ntable.RowSpec{Label: "the build"}); err != nil {
		t.Fatalf("row add as the writer: %v", err)
	}
	if _, err := ntable.CellAdd(ctx, w, "demo", "build", "ready", "b1", 1); err != nil {
		t.Fatalf("cell add as the writer: %v", err)
	}
	if _, err := ntable.CellMove(ctx, w, "demo", "build", "ready", "working", "b1"); err != nil {
		t.Fatalf("cell move as the writer: %v", err)
	}
	if _, err := ntable.CellMembers(ctx, w, "demo", "build", "working"); err != nil {
		t.Fatalf("cell members as the writer: %v", err)
	}
	if names, err := ntable.List(ctx, w); err != nil || len(names) != 1 {
		t.Fatalf("list as the writer: %v %v", names, err)
	}
	tb, err := ntable.Read(ctx, w, "demo")
	require.NoError(t, err, "read as the writer")
	got := ntable.Render(tb, ntable.RenderOpts{})
	require.Contains(t, got, "the build |     0 |       1 |    0 | -\n", "render as the writer:\n%s", got)
	// a batch: create a member, then set and guard a field of it, as the writer
	rev := func() string { return w.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val() }
	_, err = ntable.ApplyBatch(ctx, w, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev(), OperationID: "grants-1", Members: []ntable.BatchMemberEntry{
		{ID: "bt", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 2}, Set: map[string]string{"k": "v"}}}})
	require.NoError(t, err, "batch create as the writer")
	_, err = ntable.ApplyBatch(ctx, w, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev(), OperationID: "grants-2", Members: []ntable.BatchMemberEntry{
		{ID: "bt", Expect: &ntable.MemberExpect{Fields: map[string]ntable.FieldGuard{"k": {Equals: strPtr("v")}}}, Set: map[string]string{"k": "w"}, Unset: []string{"gone"}}}})
	require.NoError(t, err, "batch set as the writer")
	bound := ntable.Table{Name: "views", Columns: tb.Columns[:2]}
	r := ntable.NewRow(bound, "v")
	r.Cells[0] = ntable.Cell{Key: "ws:elsewhere:ready", Bound: true}
	bound.Rows = []ntable.Row{r}
	require.NoError(t, ntable.Bind(ctx, w, bound, time.Now()), "bind as the writer")
	_, err = ntable.RowDel(ctx, w, "demo", "build")
	require.NoError(t, err, "row del as the writer")
	_, err = ntable.Clear(ctx, w, "demo")
	require.NoError(t, err, "clear as the writer")
	_, err = ntable.Drop(ctx, w, "views")
	require.NoError(t, err, "drop as the writer")
	// the same grants less the clear function: every other call works and
	// the clear is refused NOPERM
	n := as("ns-noclear")
	_, err = ntable.RowAdd(ctx, n, "demo", "build", ntable.RowSpec{})
	require.NoError(t, err, "row add without the clear grant")
	_, err = ntable.CellAdd(ctx, n, "demo", "build", "ready", "b2", 1)
	require.NoError(t, err, "cell add without the clear grant")
	_, err = ntable.CellMove(ctx, n, "demo", "build", "ready", "done", "b2")
	require.NoError(t, err, "cell move without the clear grant")
	_, err = ntable.Clear(ctx, n, "demo")
	require.ErrorContains(t, err, "NOPERM", "clear without its grant: %v, want NOPERM", err)
}
